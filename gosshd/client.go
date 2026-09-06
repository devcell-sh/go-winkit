package gosshd

import (
	"bytes"
	"context"
	"fmt"
	"io"
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

	conn, err := cryptossh.Dial("tcp", addr, cfg)
	if err != nil {
		return nil, fmt.Errorf("dialing gosshd at %s: %w", addr, err)
	}
	return &Client{conn: conn}, nil
}

// Run executes a command and returns captured stdout, stderr, and exit code.
func (c *Client) Run(_ context.Context, cmd string) (stdout, stderr []byte, exitCode int, err error) {
	sess, err := c.conn.NewSession()
	if err != nil {
		return nil, nil, -1, fmt.Errorf("creating session: %w", err)
	}
	defer sess.Close()

	var outBuf, errBuf bytes.Buffer
	sess.Stdout = &outBuf
	sess.Stderr = &errBuf

	runErr := sess.Run(cmd)
	if runErr != nil {
		if exitErr, ok := runErr.(*cryptossh.ExitError); ok {
			return outBuf.Bytes(), errBuf.Bytes(), exitErr.ExitStatus(), nil
		}
		return outBuf.Bytes(), errBuf.Bytes(), -1, runErr
	}
	return outBuf.Bytes(), errBuf.Bytes(), 0, nil
}

// RunStream executes a command and streams output to the provided writers.
func (c *Client) RunStream(_ context.Context, cmd string, stdout, stderr io.Writer) (int, error) {
	sess, err := c.conn.NewSession()
	if err != nil {
		return -1, fmt.Errorf("creating session: %w", err)
	}
	defer sess.Close()

	sess.Stdout = stdout
	sess.Stderr = stderr

	runErr := sess.Run(cmd)
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
