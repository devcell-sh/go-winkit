package gosshd

import (
	"bytes"
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testServerWithShell(t *testing.T) string {
	t.Helper()
	return startTestServer(t, Server{
		User:     DefaultUser,
		Password: DefaultPassword,
		command: func(request string) []string {
			if request == "" {
				return []string{"/bin/sh"}
			}
			return []string{"/bin/sh", "-c", request}
		},
	})
}

func TestClient_Dial(t *testing.T) {
	addr := testServerWithShell(t)
	c, err := Dial(context.Background(), addr)
	require.NoError(t, err)
	defer c.Close()
}

func TestClient_DialTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1)
	defer cancel()
	_, err := Dial(ctx, "127.0.0.1:1") // unreachable
	assert.Error(t, err)
}

// A peer that accepts TCP but never speaks SSH (QEMU slirp hostfwd with a
// wedged guest) must fail the handshake at the deadline, not hang forever.
func TestClient_DialSilentPeer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			defer conn.Close() // accept and stay silent
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	_, err = Dial(ctx, ln.Addr().String())
	assert.Error(t, err)
	assert.Less(t, time.Since(start), 10*time.Second, "dial must respect the deadline against a silent peer")
}

// Run must return when its context is cancelled instead of blocking on a
// command that never finishes.
func TestClient_RunContextCancelled(t *testing.T) {
	addr := testServerWithShell(t)
	c, err := Dial(context.Background(), addr)
	require.NoError(t, err)
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, _, err = c.Run(ctx, "sleep 30")
	assert.Error(t, err)
	assert.Less(t, time.Since(start), 5*time.Second, "Run must unblock on ctx cancellation")
}

func TestClient_Run_Echo(t *testing.T) {
	addr := testServerWithShell(t)
	c, err := Dial(context.Background(), addr)
	require.NoError(t, err)
	defer c.Close()

	stdout, _, code, err := c.Run(context.Background(), "echo hello")
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Contains(t, string(stdout), "hello")
}

func TestClient_Run_ExitCode(t *testing.T) {
	addr := testServerWithShell(t)
	c, err := Dial(context.Background(), addr)
	require.NoError(t, err)
	defer c.Close()

	_, _, code, err := c.Run(context.Background(), "exit 42")
	require.NoError(t, err)
	assert.Equal(t, 42, code)
}

func TestClient_RunStream(t *testing.T) {
	addr := testServerWithShell(t)
	c, err := Dial(context.Background(), addr)
	require.NoError(t, err)
	defer c.Close()

	var stdout, stderr bytes.Buffer
	code, err := c.RunStream(context.Background(), "echo streamed", &stdout, &stderr)
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout.String(), "streamed")
}
