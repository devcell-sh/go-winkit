package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/devcell-sh/go-winkit/gosshd"
	"github.com/devcell-sh/go-winkit/vm/vmstate"
)

func newStopCmd() *cobra.Command {
	var stateDir string

	cmd := &cobra.Command{
		Use:   "stop [name|image]",
		Short: "Stop a running Windows VM",
		Long: "Stop a VM started with `winkit start`.\n" +
			"With no argument, stops the only running VM (errors if\n" +
			"there are zero or more than one). Accepts a VM name or\n" +
			"image path. Attempts graceful SSH shutdown first, then\n" +
			"falls back to SIGTERM.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if stateDir == "" {
				stateDir = vmstate.DefaultDir()
			}

			var st *vmstate.State
			if len(args) == 1 {
				var err error
				st, err = resolveTarget(stateDir, args[0])
				if err != nil {
					return err
				}
			} else {
				var err error
				st, err = resolveOnlyVM(stateDir)
				if err != nil {
					return err
				}
			}

			if !vmstate.IsAlive(st.PID) {
				fmt.Fprintf(cmd.OutOrStdout(), "VM %q (PID %d) is not running, cleaning up state\n",
					st.Name, st.PID)
				return vmstate.Remove(stateDir, st.Name)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Stopping VM %q (PID %d)\n", st.Name, st.PID)

			if st.SSHPort > 0 {
				if gracefulShutdown(st) {
					fmt.Fprintln(cmd.OutOrStdout(), "Guest shutting down gracefully")
					if waitForExit(st.PID, 30*time.Second) {
						fmt.Fprintln(cmd.OutOrStdout(), "VM stopped")
						return vmstate.Remove(stateDir, st.Name)
					}
					fmt.Fprintln(cmd.ErrOrStderr(), "Graceful shutdown timed out, sending SIGTERM")
				}
			}

			p, err := os.FindProcess(st.PID)
			if err == nil {
				_ = p.Signal(syscall.SIGTERM)
				if waitForExit(st.PID, 10*time.Second) {
					fmt.Fprintln(cmd.OutOrStdout(), "VM stopped")
					return vmstate.Remove(stateDir, st.Name)
				}
				_ = p.Kill()
			}

			fmt.Fprintln(cmd.OutOrStdout(), "VM killed")
			return vmstate.Remove(stateDir, st.Name)
		},
	}

	cmd.Flags().StringVar(&stateDir, "state-dir", "", "state directory (default ~/.winkit/run/)")
	return cmd
}

func resolveOnlyVM(stateDir string) (*vmstate.State, error) {
	states, err := vmstate.List(stateDir)
	if err != nil {
		return nil, fmt.Errorf("listing VMs: %w", err)
	}
	// Filter to alive VMs.
	var alive []*vmstate.State
	for _, s := range states {
		if vmstate.IsAlive(s.PID) {
			alive = append(alive, s)
		}
	}
	switch len(alive) {
	case 0:
		return nil, fmt.Errorf("no running VMs found")
	case 1:
		return alive[0], nil
	default:
		names := make([]string, len(alive))
		for i, s := range alive {
			names[i] = s.Name
		}
		return nil, fmt.Errorf("multiple running VMs, specify which one: %s", fmt.Sprintf("%v", names))
	}
}

func resolveTarget(stateDir, target string) (*vmstate.State, error) {
	st, err := vmstate.Load(stateDir, target)
	if err == nil {
		return st, nil
	}

	absPath, pathErr := filepath.Abs(target)
	if pathErr == nil {
		st, findErr := vmstate.FindByImage(stateDir, absPath)
		if findErr == nil && st != nil {
			return st, nil
		}
	}

	return nil, fmt.Errorf("no VM found for %q (checked name and image path)", target)
}

// gracefulShutdown asks the guest to shut itself down over gosshd. It is
// hard-bounded: a wedged guest (or QEMU slirp accepting the forward while
// the guest never answers) must fall through to SIGTERM, not hang stop.
func gracefulShutdown(st *vmstate.State) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res := make(chan bool, 1)
	go func() {
		addr := fmt.Sprintf("127.0.0.1:%d", st.SSHPort)
		c, err := gosshd.DialWith(ctx, addr, gosshd.DefaultUser, gosshd.DefaultPassword)
		if err != nil {
			res <- false
			return
		}
		defer c.Close()
		_, _, _, err = c.Run(ctx, "shutdown /s /t 5")
		res <- err == nil
	}()
	select {
	case ok := <-res:
		return ok
	case <-ctx.Done():
		return false
	}
}

func waitForExit(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !vmstate.IsAlive(pid) {
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}
