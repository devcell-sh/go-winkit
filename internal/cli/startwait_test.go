package cli

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/devcell-sh/go-winkit/gosshd"
)

func TestWaitForSSH_ReturnsWhenHandshakeSucceeds(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()
	srv := gosshd.Server{User: gosshd.DefaultUser, Password: gosshd.DefaultPassword}
	go func() { _ = srv.Serve(l) }()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var ticks int
	err = waitForSSH(ctx, l.Addr().String(), make(chan struct{}), func(time.Duration) { ticks++ })
	require.NoError(t, err)
	assert.LessOrEqual(t, ticks, 2, "a listening server should be detected on the first attempt")
}

func TestWaitForSSH_VMExitEndsTheWait(t *testing.T) {
	// Nothing listens on the reserved port: only vmDone can end the wait.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	l.Close()

	vmDone := make(chan struct{})
	close(vmDone)
	err = waitForSSH(context.Background(), addr, vmDone, nil)
	assert.True(t, errors.Is(err, errVMExited), "got %v", err)
}

func TestWaitForSSH_DeadlineEndsTheWait(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	l.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	err = waitForSSH(ctx, addr, make(chan struct{}), nil)
	assert.True(t, errors.Is(err, context.DeadlineExceeded), "got %v", err)
}

func TestWaitForSSH_AcceptingButSilentPeerDoesNotHang(t *testing.T) {
	// QEMU's hostfwd accepts TCP before the guest service is up. A dial
	// that accepts and never speaks SSH must time out per attempt rather
	// than block the wait, so vmDone can still end it.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()

	vmDone := make(chan struct{})
	time.AfterFunc(200*time.Millisecond, func() { close(vmDone) })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err = waitForSSH(ctx, l.Addr().String(), vmDone, nil)
	assert.True(t, errors.Is(err, errVMExited), "got %v", err)
}

func TestFormatElapsed(t *testing.T) {
	assert.Equal(t, "0s", formatElapsed(0))
	assert.Equal(t, "42s", formatElapsed(42*time.Second))
	assert.Equal(t, "1m05s", formatElapsed(65*time.Second))
	assert.Equal(t, "12m00s", formatElapsed(12*time.Minute))
}

func TestStartCmd_HasWaitFlags(t *testing.T) {
	cmd := newStartCmd()
	assert.NotNil(t, cmd.Flags().Lookup("no-wait"))
	f := cmd.Flags().Lookup("wait-timeout")
	require.NotNil(t, f)
	assert.Equal(t, "10m0s", f.DefValue)
}
