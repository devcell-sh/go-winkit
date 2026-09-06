// Package webdavshare serves a host directory to VM guests over WebDAV.
//
// Windows ships a built-in WebDAV client (the WebClient service), so a plain
// HTTP server on the host loopback is enough to give a guest a mapped drive
// over QEMU user-mode networking: the guest reaches 127.0.0.1:<port> on the
// host as 10.0.2.2:<port> and runs
//
//	net use W: \\10.0.2.2@<port>\winkit
//
// No Samba on the host, no drivers in the guest, identical behavior on
// macOS, Linux, and Windows hosts. This is the same endpoint UTM's SPICE
// WebDAV sharing presents to Windows guests, minus the SPICE transport.
package webdavshare

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/net/webdav"
)

// DefaultPort is the conventional port for the share, matching the
// spice-webdav convention. Port 0 selects an ephemeral port.
const DefaultPort = 9843

// GuestHost is the address at which a QEMU user-net guest reaches the host.
const GuestHost = "10.0.2.2"

// DefaultShareName is the URL path prefix and the UNC share name visible
// to the guest (e.g. \\10.0.2.2@9843\winkit).
const DefaultShareName = "winkit"

// Config configures a Server.
type Config struct {
	// Root is the host directory to share. Required, must exist.
	Root string
	// Port to listen on (loopback only). 0 selects an ephemeral port.
	Port int
	// ShareName is the URL path prefix and UNC share name. Defaults to
	// DefaultShareName ("winkit") if empty.
	ShareName string
	// Logger receives request-level debug logs. Optional.
	Logger *slog.Logger
}

// Server is a loopback WebDAV server sharing a single directory tree.
type Server struct {
	cfg      Config
	listener net.Listener
	httpSrv  *http.Server
}

// New validates cfg and prepares a server. Call Start to begin serving.
func New(cfg Config) (*Server, error) {
	if cfg.Root == "" {
		return nil, fmt.Errorf("webdavshare: Root is required")
	}
	fi, err := os.Stat(cfg.Root)
	if err != nil {
		return nil, fmt.Errorf("webdavshare: root: %w", err)
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("webdavshare: root %s is not a directory", cfg.Root)
	}
	if cfg.ShareName == "" {
		cfg.ShareName = DefaultShareName
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Server{cfg: cfg}, nil
}

// Start binds the loopback listener and serves in a background goroutine.
func (s *Server) Start() error {
	davHandler := &webdav.Handler{
		FileSystem: webdav.Dir(s.cfg.Root),
		LockSystem: webdav.NewMemLS(),
		Logger: func(r *http.Request, err error) {
			if err != nil {
				s.cfg.Logger.Debug("webdav request failed", "method", r.Method, "path", r.URL.Path, "err", err)
			} else {
				s.cfg.Logger.Debug("webdav request", "method", r.Method, "path", r.URL.Path)
			}
		},
	}
	prefix := "/" + s.cfg.ShareName
	mux := http.NewServeMux()
	mux.Handle(prefix+"/", http.StripPrefix(prefix, davHandler))

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", s.cfg.Port))
	if err != nil {
		return fmt.Errorf("webdavshare: listen: %w", err)
	}
	s.listener = ln
	s.httpSrv = &http.Server{Handler: mux}
	go func() {
		if serveErr := s.httpSrv.Serve(ln); serveErr != nil && serveErr != http.ErrServerClosed {
			s.cfg.Logger.Warn("webdav server stopped", "err", serveErr)
		}
	}()
	s.cfg.Logger.Info("webdav share serving",
		"root", s.cfg.Root, "addr", ln.Addr().String(),
		"guestPath", fmt.Sprintf(`\\%s@%d\%s`, GuestHost, s.Port(), s.cfg.ShareName))
	return nil
}

// Port returns the bound port, or 0 before Start.
func (s *Server) Port() int {
	if s.listener == nil {
		return 0
	}
	return s.listener.Addr().(*net.TCPAddr).Port
}

// ShareName returns the UNC share name (URL path prefix).
func (s *Server) ShareName() string {
	return s.cfg.ShareName
}

// URL returns the host-side base URL including the share prefix, valid after Start.
func (s *Server) URL() string {
	return fmt.Sprintf("http://127.0.0.1:%d/%s", s.Port(), s.cfg.ShareName)
}

// Close stops the server and releases the listener. Safe to call once.
func (s *Server) Close() error {
	if s.httpSrv == nil {
		return nil
	}
	return s.httpSrv.Close()
}

// RunFunc executes a command on the guest and returns its output.
// The caller provides this so webdavshare does not import any SSH package.
type RunFunc func(ctx context.Context, cmd string) (stdout, stderr []byte, exitCode int, err error)

// VerifyConfig configures VerifyAndMount.
type VerifyConfig struct {
	// GuestRun executes a command on the guest. Required.
	GuestRun RunFunc
	// GuestHostAddr is the IP the guest uses to reach the host.
	// Defaults to GuestHost ("10.0.2.2") when empty.
	GuestHostAddr string
	// Drive is the letter (no colon) to map. Defaults to "W".
	Drive string
	// Logger receives progress messages.
	Logger *slog.Logger
}

// VerifyAndMount performs a three-step verification:
//  1. Host-local HTTP GET of a probe file (confirms the server is serving).
//  2. Guest HTTP GET through the network (confirms host reachability).
//  3. Starts WebClient and mounts the share as the configured drive letter.
//
// A probe file is written to the server's root before the check and removed
// afterward. The server must already be started. On failure the caller is
// responsible for closing the server.
func (s *Server) VerifyAndMount(ctx context.Context, vc VerifyConfig) error {
	if vc.GuestRun == nil {
		return fmt.Errorf("webdavshare: VerifyConfig.GuestRun is required")
	}
	if vc.GuestHostAddr == "" {
		vc.GuestHostAddr = GuestHost
	}
	if vc.Drive == "" {
		vc.Drive = "W"
	}
	logger := vc.Logger
	if logger == nil {
		logger = slog.Default()
	}

	probePath := filepath.Join(s.cfg.Root, "probe.txt")
	if err := os.WriteFile(probePath, []byte("webdav-probe-ok"), 0o644); err != nil {
		return fmt.Errorf("webdavshare: writing probe file: %w", err)
	}
	defer os.Remove(probePath)

	resp, err := http.Get(s.URL() + "/probe.txt")
	if err != nil {
		return fmt.Errorf("webdavshare: host-local sanity GET: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("webdavshare: host-local sanity GET: status %d, want 200", resp.StatusCode)
	}
	logger.Info("webdav verify: host-local GET OK", "port", s.Port())

	guestURL := fmt.Sprintf("http://%s:%d/%s/probe.txt", vc.GuestHostAddr, s.Port(), s.cfg.ShareName)
	psCmd := fmt.Sprintf(`(Invoke-WebRequest -Uri '%s' -UseBasicParsing -TimeoutSec 60).StatusCode`, guestURL)
	stdout, stderr, exit, err := vc.GuestRun(ctx, psCmd)
	if err != nil {
		return fmt.Errorf("webdavshare: guest Invoke-WebRequest: %w (stderr: %s)", err, stderr)
	}
	if exit != 0 {
		return fmt.Errorf("webdavshare: guest Invoke-WebRequest exited %d: stdout=%s stderr=%s", exit, stdout, stderr)
	}

	status := strings.TrimSpace(string(stdout))
	logger.Info("webdav verify: guest GET", "status", status, "url", guestURL)
	if status != "200" {
		return fmt.Errorf("webdavshare: guest GET status %q, want 200", status)
	}

	mountCmd := fmt.Sprintf(
		`Start-Service WebClient; `+
			`net use %s: \\%s@%d\%s /persistent:no 2>&1`,
		vc.Drive, vc.GuestHostAddr, s.Port(), s.cfg.ShareName)
	stdout, stderr, exit, err = vc.GuestRun(ctx, mountCmd)
	if err != nil || exit != 0 {
		logger.Warn("webdav mount failed (share still accessible via HTTP)",
			"drive", vc.Drive+":", "exit", exit,
			"stdout", strings.TrimSpace(string(stdout)),
			"stderr", strings.TrimSpace(string(stderr)), "err", err)
	} else {
		logger.Info("webdav share mounted", "drive", vc.Drive+":", "root", s.cfg.Root)
	}

	return nil
}
