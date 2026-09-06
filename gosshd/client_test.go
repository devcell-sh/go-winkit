package gosshd

import (
	"bytes"
	"context"
	"testing"

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
