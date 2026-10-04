package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/devcell-sh/go-winkit/gosshd"
)

// errVMExited reports that the VM process ended before its SSH port
// accepted a handshake.
var errVMExited = errors.New("VM exited before SSH became reachable")

// sshReadyPollInterval is the pause between gosshd dial attempts.
const sshReadyPollInterval = 2 * time.Second

// waitForSSH blocks until a gosshd handshake succeeds at addr, the VM
// signals exit via vmDone, or ctx ends. It returns nil on success,
// errVMExited when vmDone closes first, and ctx.Err() on cancellation
// or deadline. onTick, when set, receives the elapsed time once per
// second so a UI can show that the wait is alive.
//
// Each dial is bounded by its own short deadline: during boot QEMU's
// hostfwd accepts TCP and then says nothing until the guest service is
// up, so an unbounded dial would hang the whole wait.
func waitForSSH(ctx context.Context, addr string, vmDone <-chan struct{}, onTick func(elapsed time.Duration)) error {
	start := time.Now()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	try := func() bool {
		dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		client, err := gosshd.Dial(dialCtx, addr)
		if err != nil {
			return false
		}
		client.Close()
		return true
	}

	next := time.After(0)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-vmDone:
			return errVMExited
		case <-ticker.C:
			if onTick != nil {
				onTick(time.Since(start).Truncate(time.Second))
			}
		case <-next:
			if try() {
				return nil
			}
			next = time.After(sshReadyPollInterval)
		}
	}
}

// formatElapsed renders a wait duration as the compact "1m05s" / "42s"
// form used in the checklist progress suffix.
func formatElapsed(d time.Duration) string {
	d = d.Truncate(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}
