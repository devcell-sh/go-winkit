package gosshd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"time"

	cryptossh "golang.org/x/crypto/ssh"
)

// Client connects to a gosshd server running inside a WinPE guest.
type Client struct {
	conn *cryptossh.Client
}

// Dial connects to a gosshd server at addr using the default credentials.
// The context deadline controls the connection timeout.
func Dial(ctx context.Context, addr string) (*Client, error) {
	return DialWith(ctx, addr, DefaultUser, DefaultPassword)
}

// DialWith connects with explicit credentials.
func DialWith(ctx context.Context, addr, user, password string) (*Client, error) {
	timeout := 10 * time.Second
	if dl, ok := ctx.Deadline(); ok {
		timeout = time.Until(dl)
		if timeout <= 0 {
			return nil, context.DeadlineExceeded
		}
	}

	cfg := &cryptossh.ClientConfig{
		User:            user,
		Auth:            []cryptossh.AuthMethod{cryptossh.Password(password)},
		HostKeyCallback: cryptossh.InsecureIgnoreHostKey(),
		Timeout:         timeout,
	}

	// Dial and handshake under one deadline. ClientConfig.Timeout only
	// bounds the TCP connect; a peer that accepts and then never speaks
	// SSH — QEMU's slirp hostfwd does exactly this while the guest is
	// wedged or shutting down — would hang the handshake forever.
	netConn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, fmt.Errorf("dialing gosshd at %s: %w", addr, err)
	}
	_ = netConn.SetDeadline(time.Now().Add(timeout))
	sshConn, chans, reqs, err := cryptossh.NewClientConn(netConn, addr, cfg)
	if err != nil {
		netConn.Close()
		return nil, fmt.Errorf("dialing gosshd at %s: %w", addr, err)
	}
	_ = netConn.SetDeadline(time.Time{})
	return &Client{conn: cryptossh.NewClient(sshConn, chans, reqs)}, nil
}

// Run executes a command and returns captured stdout, stderr, and exit code.
// Cancelling ctx closes the session and returns ctx.Err() instead of
// blocking on a guest that never answers.
func (c *Client) Run(ctx context.Context, cmd string) (stdout, stderr []byte, exitCode int, err error) {
	sess, err := c.conn.NewSession()
	if err != nil {
		return nil, nil, -1, fmt.Errorf("creating session: %w", err)
	}
	defer sess.Close()

	var outBuf, errBuf bytes.Buffer
	sess.Stdout = &outBuf
	sess.Stderr = &errBuf

	runErr := runSession(ctx, sess, cmd)
	if runErr != nil {
		if exitErr, ok := runErr.(*cryptossh.ExitError); ok {
			return outBuf.Bytes(), errBuf.Bytes(), exitErr.ExitStatus(), nil
		}
		return outBuf.Bytes(), errBuf.Bytes(), -1, runErr
	}
	return outBuf.Bytes(), errBuf.Bytes(), 0, nil
}

// runSession runs cmd on sess, unblocking on ctx cancellation. sess.Close
// is best-effort — a fully wedged transport may leak the goroutine, which
// is acceptable for the short-lived CLI callers this protects.
func runSession(ctx context.Context, sess *cryptossh.Session, cmd string) error {
	done := make(chan error, 1)
	go func() { done <- sess.Run(cmd) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		sess.Close()
		return ctx.Err()
	}
}

// RunStream executes a command and streams output to the provided writers.
// Cancelling ctx closes the session, like Run.
func (c *Client) RunStream(ctx context.Context, cmd string, stdout, stderr io.Writer) (int, error) {
	sess, err := c.conn.NewSession()
	if err != nil {
		return -1, fmt.Errorf("creating session: %w", err)
	}
	defer sess.Close()

	sess.Stdout = stdout
	sess.Stderr = stderr

	runErr := runSession(ctx, sess, cmd)
	if runErr != nil {
		if exitErr, ok := runErr.(*cryptossh.ExitError); ok {
			return exitErr.ExitStatus(), nil
		}
		return -1, runErr
	}
	return 0, nil
}

// Close closes the SSH connection.
func (c *Client) Close() error {
	return c.conn.Close()
}
