// Package gosshd is an SSH server for WinPE guests.
//
// Win32-OpenSSH cannot serve a session there. Per connection it spawns a
// pre-auth child as a virtual account — LsaManageSidNameMapping, then
// LogonUserExExW with LOGON32_PROVIDER_VIRTUAL — and authenticates the user
// with an S4U logon. WinPE supports no user logons at all, so both stages
// fail and the connection closes before authentication. Privilege separation
// has been mandatory upstream since 7.5, so there is no configuration that
// avoids it.
//
// This server answers with a password callback in its own process and starts
// children with a plain CreateProcess, so no part of a session touches the
// Windows logon subsystem. The guest is a throwaway PE behind a host-only
// NAT reached over a per-run forwarded port, which is what makes a fixed
// password acceptable here.
package gosshd

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os/exec"
	"time"

	"github.com/gliderlabs/ssh"
)

const (
	// DefaultAddr is the guest-side listen address. The host reaches it
	// through a per-run forwarded port, never directly.
	DefaultAddr = ":22"

	// DefaultUser and DefaultPassword are the credentials the guest is
	// built with. WinPE's Administrator has no password and no usable
	// account database, so these name nothing in Windows — they are only
	// this server's own shared secret with the harness.
	DefaultUser     = "admin"
	DefaultPassword = "admin"
)

// Server serves one WinPE guest.
type Server struct {
	// Addr is the listen address, ":22" when empty.
	Addr string
	// User and Password are the only accepted credentials. Both empty
	// means no credentials are accepted at all.
	User, Password string
	// Log receives connection events. Discarded when nil.
	Log *log.Logger

	// Shell selects the interpreter sessions run under. "" or "cmd" uses
	// cmd.exe — the WinPE-safe default, because full Windows PowerShell is not
	// guaranteed on WinPE's PATH (pwsh is staged elsewhere). "powershell" uses
	// Windows PowerShell (System32\...\powershell.exe), which the wsl
	// provisioning server runs under so the host can send PowerShell directly.
	Shell string

	// Events receives one structured JSON line per session (command, exit
	// code, captured stdout/stderr, duration). Discarded when nil. In the
	// guest this is the virtio-serial structured port whose host end is
	// build.jsonl, giving a durable 1:1 record of everything run over SSH.
	Events io.Writer

	// command builds a session's argv; SessionCommand when nil. Tests set
	// it to run a POSIX shell on the dev machine instead of cmd.exe.
	command func(request string) []string
}

// sessionEvent is one structured record emitted per SSH session. Its wire
// shape is a superset of winpe.GuestEvent (ts + event + typed extras), so
// the same build.jsonl parser reads it losslessly alongside build events.
type sessionEvent struct {
	TS         string `json:"ts"`
	Event      string `json:"event"`
	Remote     string `json:"remote"`
	Command    string `json:"command"`
	Exit       int    `json:"exit"`
	DurationMS int64  `json:"duration_ms"`
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	RunError   string `json:"run_error,omitempty"`
}

// maxCapture bounds the per-stream output retained for the structured
// event so a runaway command cannot exhaust guest memory; the live SSH
// stream to the client is never truncated.
const maxCapture = 256 * 1024

// emitSession writes one structured session record to Events. Emission
// never fails a session: a full or closed port must not break SSH.
func (s Server) emitSession(ev sessionEvent) {
	if s.Events == nil {
		return
	}
	line, err := json.Marshal(ev)
	if err != nil {
		return
	}
	s.Events.Write(append(line, '\n'))
}

// Authenticate reports whether the offered credentials are the configured
// ones. A Server with no credentials configured accepts nothing, so a
// zero-value Server cannot be reached with an empty password.
func (s Server) Authenticate(user, password string) bool {
	if s.User == "" || s.Password == "" {
		return false
	}
	// Constant-time so the answer does not depend on how much of the
	// credential matched.
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(s.User))
	passOK := subtle.ConstantTimeCompare([]byte(password), []byte(s.Password))
	return userOK&passOK == 1
}

// SessionCommand is the argv for a session under cmd.exe. An empty request
// means the client asked for a shell rather than a single command.
//
// cmd.exe rather than pwsh: WinPE always has it, and the harness stages
// pwsh at a path that is not on PATH.
func SessionCommand(request string) []string {
	return SessionCommandShell(request, "cmd")
}

// SessionCommandShell is the argv for a session under the named shell: "cmd"
// (default) or "powershell". Under powershell the request is passed as a single
// -Command argument, so the host can send a PowerShell one-liner verbatim with
// no cmd re-parsing — what the wsl provisioning path relies on.
func SessionCommandShell(request, shell string) []string {
	if shell == "powershell" {
		if request == "" {
			return []string{"powershell.exe", "-NoProfile"}
		}
		return []string{"powershell.exe", "-NoProfile", "-NonInteractive", "-Command", request}
	}
	if request == "" {
		return []string{"cmd.exe"}
	}
	return []string{"cmd.exe", "/c", request}
}

func (s Server) logf(format string, args ...any) {
	if s.Log != nil {
		s.Log.Printf(format, args...)
	}
}

// handle runs one session's command and reports its exit status to the
// client. A command that fails to start is distinct from one that runs and
// exits non-zero, and the client can only tell them apart from stderr.
func (s Server) handle(sess ssh.Session) {
	buildArgv := s.command
	if buildArgv == nil {
		buildArgv = func(request string) []string { return SessionCommandShell(request, s.Shell) }
	}
	request := sess.RawCommand()
	argv := buildArgv(request)
	s.logf("session from %s: %v", sess.RemoteAddr(), argv)

	// Tee stdout/stderr: the client still gets the full live stream, and a
	// bounded copy is retained for the structured build.jsonl record.
	var outCap, errCap capBuffer
	outCap.limit, errCap.limit = maxCapture, maxCapture
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout = io.MultiWriter(sess, &outCap)
	cmd.Stderr = io.MultiWriter(sess.Stderr(), &errCap)

	ev := sessionEvent{
		Event:   "ssh_session",
		Remote:  sess.RemoteAddr().String(),
		Command: request,
	}
	start := time.Now()
	finish := func(exit int) {
		ev.TS = time.Now().UTC().Format(time.RFC3339Nano)
		ev.Exit = exit
		ev.DurationMS = time.Since(start).Milliseconds()
		ev.Stdout = outCap.String()
		ev.Stderr = errCap.String()
		s.emitSession(ev)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		fmt.Fprintln(sess.Stderr(), "winkit: stdin pipe:", err)
		ev.RunError = err.Error()
		finish(1)
		sess.Exit(1)
		return
	}
	go func() {
		io.Copy(stdin, sess)
		stdin.Close()
	}()

	err = cmd.Run()
	if err == nil {
		finish(0)
		sess.Exit(0)
		return
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		finish(exitErr.ExitCode())
		sess.Exit(exitErr.ExitCode())
		return
	}
	s.logf("session from %s failed to run: %v", sess.RemoteAddr(), err)
	fmt.Fprintln(sess.Stderr(), "winkit: run:", err)
	ev.RunError = err.Error()
	finish(127)
	sess.Exit(127)
}

// capBuffer is an io.Writer that retains at most limit bytes, dropping the
// overflow. It records how much it dropped so the structured record can say
// so rather than silently lie about completeness.
type capBuffer struct {
	buf     []byte
	limit   int
	dropped int
}

func (c *capBuffer) Write(p []byte) (int, error) {
	if room := c.limit - len(c.buf); room > 0 {
		if len(p) <= room {
			c.buf = append(c.buf, p...)
		} else {
			c.buf = append(c.buf, p[:room]...)
			c.dropped += len(p) - room
		}
	} else {
		c.dropped += len(p)
	}
	return len(p), nil
}

func (c *capBuffer) String() string {
	if c.dropped == 0 {
		return string(c.buf)
	}
	return string(c.buf) + fmt.Sprintf("\n...[%d bytes truncated]", c.dropped)
}

func (s Server) server() *ssh.Server {
	return &ssh.Server{
		Handler: s.handle,
		PasswordHandler: func(ctx ssh.Context, password string) bool {
			ok := s.Authenticate(ctx.User(), password)
			s.logf("password auth user=%q from %s ok=%v", ctx.User(), ctx.RemoteAddr(), ok)
			return ok
		},
		// Sessions only ever get pipes — there is no ConPTY in WinPE to
		// put behind a granted PTY. gliderlabs grants pty-req by default,
		// which flips the client's terminal into raw mode (no local echo,
		// Enter sends \r) while cmd.exe waits for a \n that never comes.
		// Refusing makes clients fall back to cooked line mode, which the
		// pipe handles.
		PtyCallback: func(ctx ssh.Context, pty ssh.Pty) bool {
			s.logf("pty request from %s refused: sessions have no terminal", ctx.RemoteAddr())
			return false
		},
	}
}

// Serve serves connections from l until it fails.
func (s Server) Serve(l net.Listener) error {
	s.logf("listening on %s", l.Addr())
	return s.server().Serve(l)
}

// ListenAndServe serves until the listener fails.
func (s Server) ListenAndServe() error {
	addr := s.Addr
	if addr == "" {
		addr = ":22"
	}
	srv := s.server()
	srv.Addr = addr
	s.logf("listening on %s", addr)
	return srv.ListenAndServe()
}
