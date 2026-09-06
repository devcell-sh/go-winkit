// Package sftpshare serves a host directory to VM guests over SFTP.
//
// The guest runs rclone mount against this server (WinFsp-backed, fixed-disk
// mode), which presents the share as DRIVE_FIXED — the drive type WSL1's
// drvfs can stat/read through, unlike the WebClient's DRIVE_REMOTE mounts
// (CELL-532). A plain loopback listener is enough: the guest reaches
// 127.0.0.1:<port> on the host as 10.0.2.2:<port> over QEMU user-mode
// networking.
//
// SFTP over WebDAV, deliberately: rclone's SFTP backend streams uploads and
// reads ranges natively, while its WebDAV backend cannot stream and has a
// reported corruption bug under vfs-cache-mode full.
//
// Sessions are jailed to Root via os.Root: path traversal and symlink
// escapes cannot reach the rest of the host filesystem.
package sftpshare

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// DefaultPort is the conventional port for the share, one above the WebDAV
// share's 9843. Port 0 selects an ephemeral port.
const DefaultPort = 9844

// DirShare describes a host directory shared with the guest VM. Each share
// gets its own SFTP server (port), WinFsp drive letter, and optional WSL1
// symlink so the host path resolves identically inside the guest.
//
// Single-mount today; the slice form ([]DirShare) is the multi-mount API.
type DirShare struct {
	// HostPath is the absolute path on the macOS host, e.g.
	// /Users/dmitry/dev/devcell-sh/go-winkit.
	HostPath string
	// VMPath is the path inside the guest's WSL1 distro where the share
	// should appear. Typically the same as HostPath so configs work
	// unchanged across host and guest.
	VMPath string
	// Drive is the Windows drive letter (no colon) for the WinFsp mount.
	// Empty falls back to the next available letter starting from W.
	Drive string
	// Port is the SFTP server port on the host loopback. 0 selects
	// ephemeral; DefaultPort (9844) is used for the first share.
	Port int
}

// ServerConfig returns an SFTP server Config rooted at the share's HostPath.
// The share's Port is used if non-zero, otherwise DefaultPort.
func (s DirShare) ServerConfig(logger *slog.Logger) Config {
	port := s.Port
	if port == 0 {
		port = DefaultPort
	}
	return Config{Root: s.HostPath, Port: port, Logger: logger}
}

// GuestHost is the address at which a QEMU user-net guest reaches the host.
const GuestHost = "10.0.2.2"

// DefaultUser and DefaultPassword are the share credentials. The listener
// binds loopback only; the credentials exist because SFTP requires an
// authenticated user, not as a security boundary.
const (
	DefaultUser     = "winkit"
	DefaultPassword = "winkit"
)

// Config configures a Server.
type Config struct {
	// Root is the host directory to share. Required, must exist.
	Root string
	// Port to listen on (loopback only). 0 selects an ephemeral port.
	Port int
	// User and Password authenticate sessions. Default to DefaultUser and
	// DefaultPassword when empty.
	User     string
	Password string
	// Logger receives request-level debug logs. Optional.
	Logger *slog.Logger
}

// Server is a loopback SFTP server sharing a single directory tree.
type Server struct {
	cfg      Config
	listener net.Listener
	sshCfg   *ssh.ServerConfig

	mu     sync.Mutex
	closed bool
	conns  map[net.Conn]struct{}
}

// New validates cfg and prepares a server. Call Start to begin serving.
func New(cfg Config) (*Server, error) {
	if cfg.Root == "" {
		return nil, fmt.Errorf("sftpshare: Root is required")
	}
	fi, err := os.Stat(cfg.Root)
	if err != nil {
		return nil, fmt.Errorf("sftpshare: root: %w", err)
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("sftpshare: root %s is not a directory", cfg.Root)
	}
	if cfg.User == "" {
		cfg.User = DefaultUser
	}
	if cfg.Password == "" {
		cfg.Password = DefaultPassword
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Server{cfg: cfg, conns: make(map[net.Conn]struct{})}, nil
}

// Start binds the loopback listener and serves in a background goroutine.
func (s *Server) Start() error {
	sshCfg := &ssh.ServerConfig{
		PasswordCallback: func(meta ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if meta.User() == s.cfg.User && string(pass) == s.cfg.Password {
				return nil, nil
			}
			return nil, fmt.Errorf("sftpshare: auth failed for %q", meta.User())
		},
	}
	// The host key is ephemeral by design: guests connect with host-key
	// checking off (rclone's default), and a fresh key per run leaves no
	// state to manage or leak.
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("sftpshare: generating host key: %w", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		return fmt.Errorf("sftpshare: host key signer: %w", err)
	}
	sshCfg.AddHostKey(signer)
	s.sshCfg = sshCfg

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", s.cfg.Port))
	if err != nil {
		return fmt.Errorf("sftpshare: listen: %w", err)
	}
	s.listener = ln
	go s.acceptLoop()
	s.cfg.Logger.Info("sftp share serving",
		"root", s.cfg.Root, "addr", ln.Addr().String(),
		"guestAddr", fmt.Sprintf("%s:%d", GuestHost, s.Port()))
	return nil
}

// Port returns the bound port, or 0 before Start.
func (s *Server) Port() int {
	if s.listener == nil {
		return 0
	}
	return s.listener.Addr().(*net.TCPAddr).Port
}

// User returns the configured username.
func (s *Server) User() string { return s.cfg.User }

// Password returns the configured password.
func (s *Server) Password() string { return s.cfg.Password }

// Close stops the listener and tears down active sessions.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	conns := make([]net.Conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()

	var err error
	if s.listener != nil {
		err = s.listener.Close()
	}
	for _, c := range conns {
		c.Close()
	}
	return err
}

func (s *Server) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return // listener closed
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			conn.Close()
			return
		}
		s.conns[conn] = struct{}{}
		s.mu.Unlock()
		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	defer func() {
		conn.Close()
		s.mu.Lock()
		delete(s.conns, conn)
		s.mu.Unlock()
	}()

	sshConn, chans, reqs, err := ssh.NewServerConn(conn, s.sshCfg)
	if err != nil {
		s.cfg.Logger.Debug("sftp handshake failed", "err", err)
		return
	}
	defer sshConn.Close()
	go ssh.DiscardRequests(reqs)
	s.cfg.Logger.Debug("sftp session open", "user", sshConn.User(), "remote", conn.RemoteAddr().String())

	for newChan := range chans {
		if newChan.ChannelType() != "session" {
			newChan.Reject(ssh.UnknownChannelType, "only session channels are supported")
			continue
		}
		channel, requests, err := newChan.Accept()
		if err != nil {
			continue
		}
		go func(in <-chan *ssh.Request) {
			// Accept only the sftp subsystem; refuse shells and execs.
			for req := range in {
				ok := req.Type == "subsystem" && len(req.Payload) >= 4 &&
					string(req.Payload[4:]) == "sftp"
				req.Reply(ok, nil)
			}
		}(requests)
		go s.serveChannel(channel)
	}
}

func (s *Server) serveChannel(channel ssh.Channel) {
	defer channel.Close()
	root, err := os.OpenRoot(s.cfg.Root)
	if err != nil {
		s.cfg.Logger.Warn("sftp: opening share root", "err", err)
		return
	}
	defer root.Close()

	h := &rootedHandler{root: root, logger: s.cfg.Logger}
	srv := sftp.NewRequestServer(channel, sftp.Handlers{
		FileGet:  h,
		FilePut:  h,
		FileCmd:  h,
		FileList: h,
	})
	if err := srv.Serve(); err != nil && !errors.Is(err, io.EOF) {
		s.cfg.Logger.Debug("sftp session ended", "err", err)
	}
	srv.Close()
}

// rootedHandler implements the four sftp request handlers on top of os.Root,
// which enforces the jail: lexical traversal and symlink escapes both fail
// inside the kernel-backed root, not in hand-rolled path checks.
type rootedHandler struct {
	root   *os.Root
	logger *slog.Logger
}

// rel converts an SFTP absolute path to one relative to the share root.
func rel(p string) string {
	p = strings.TrimPrefix(path.Clean("/"+p), "/")
	if p == "" {
		return "."
	}
	return p
}

func (h *rootedHandler) Fileread(r *sftp.Request) (io.ReaderAt, error) {
	h.logger.Debug("sftp request", "method", r.Method, "path", r.Filepath)
	return h.root.Open(rel(r.Filepath))
}

func (h *rootedHandler) Filewrite(r *sftp.Request) (io.WriterAt, error) {
	h.logger.Debug("sftp request", "method", r.Method, "path", r.Filepath)
	flags := r.Pflags()
	osFlags := 0
	switch {
	case flags.Read && flags.Write:
		osFlags = os.O_RDWR
	case flags.Write:
		osFlags = os.O_WRONLY
	}
	if flags.Creat {
		osFlags |= os.O_CREATE
	}
	if flags.Trunc {
		osFlags |= os.O_TRUNC
	}
	if flags.Excl {
		osFlags |= os.O_EXCL
	}
	return h.root.OpenFile(rel(r.Filepath), osFlags, 0o644)
}

func (h *rootedHandler) Filecmd(r *sftp.Request) error {
	h.logger.Debug("sftp request", "method", r.Method, "path", r.Filepath)
	p := rel(r.Filepath)
	switch r.Method {
	case "Setstat":
		attrs := r.Attributes()
		flags := r.AttrFlags()
		if flags.Acmodtime {
			at := time.Unix(int64(attrs.Atime), 0)
			mt := time.Unix(int64(attrs.Mtime), 0)
			if err := h.root.Chtimes(p, at, mt); err != nil {
				return err
			}
		}
		if flags.Permissions {
			if err := h.root.Chmod(p, attrs.FileMode().Perm()); err != nil {
				return err
			}
		}
		if flags.Size {
			f, err := h.root.OpenFile(p, os.O_WRONLY, 0)
			if err != nil {
				return err
			}
			defer f.Close()
			return f.Truncate(int64(attrs.Size))
		}
		return nil
	case "Rename":
		return h.root.Rename(p, rel(r.Target))
	case "Rmdir":
		return h.root.Remove(p)
	case "Remove":
		return h.root.Remove(p)
	case "Mkdir":
		return h.root.Mkdir(p, 0o755)
	case "Symlink":
		return h.root.Symlink(rel(r.Target), p)
	case "Link":
		return h.root.Link(rel(r.Target), p)
	default:
		return fmt.Errorf("sftpshare: unsupported method %q", r.Method)
	}
}

func (h *rootedHandler) Filelist(r *sftp.Request) (sftp.ListerAt, error) {
	h.logger.Debug("sftp request", "method", r.Method, "path", r.Filepath)
	p := rel(r.Filepath)
	switch r.Method {
	case "List":
		f, err := h.root.Open(p)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		infos, err := f.Readdir(-1)
		if err != nil {
			return nil, err
		}
		return listerAt(infos), nil
	case "Stat":
		fi, err := h.root.Stat(p)
		if err != nil {
			return nil, err
		}
		return listerAt{fi}, nil
	case "Lstat":
		fi, err := h.root.Lstat(p)
		if err != nil {
			return nil, err
		}
		return listerAt{fi}, nil
	case "Readlink":
		target, err := h.root.Readlink(p)
		if err != nil {
			return nil, err
		}
		return listerAt{linkInfo(target)}, nil
	default:
		return nil, fmt.Errorf("sftpshare: unsupported method %q", r.Method)
	}
}

// listerAt adapts a FileInfo slice to sftp.ListerAt.
type listerAt []os.FileInfo

func (l listerAt) ListAt(dst []os.FileInfo, offset int64) (int, error) {
	if offset >= int64(len(l)) {
		return 0, io.EOF
	}
	n := copy(dst, l[offset:])
	if offset+int64(n) >= int64(len(l)) {
		return n, io.EOF
	}
	return n, nil
}

// linkInfo carries a symlink target through the Readlink response, which
// (per pkg/sftp convention) travels as a FileInfo whose Name is the target.
type linkInfo string

func (l linkInfo) Name() string       { return string(l) }
func (l linkInfo) Size() int64        { return 0 }
func (l linkInfo) Mode() os.FileMode  { return os.ModeSymlink }
func (l linkInfo) ModTime() time.Time { return time.Time{} }
func (l linkInfo) IsDir() bool        { return false }
func (l linkInfo) Sys() any           { return nil }
