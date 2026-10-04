package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	winkit "github.com/devcell-sh/go-winkit"
	"github.com/devcell-sh/go-winkit/internal/config"
	"github.com/devcell-sh/go-winkit/vm"
	"github.com/devcell-sh/go-winkit/vm/vmstate"
)

func newStartCmd() *cobra.Command {
	var (
		configFile string
		name       string
		hostname   string
		accel      string
		vncPort    uint16
		foreground bool
		cpus       uint
		memoryGB   uint64
		sshPort    uint16
		rdpPort    uint16
		stateDir   string
		noWait     bool
		waitFor    time.Duration
		forwards   []string
	)

	cmd := &cobra.Command{
		Use:   "start [image]",
		Short: "Boot a built Windows VM image",
		Long: "Start a VM from a previously built disk image.\n" +
			"When no image is given, discovers the build output in\n" +
			"the current directory (winkit-full.qcow2, etc.).\n" +
			"Loads defaults from winkit.yaml if present.\n" +
			"Waits until the guest accepts SSH, then prints connection\n" +
			"info (SSH, RDP, VNC ports) and writes state so `winkit status`\n" +
			"and `winkit stop` can manage it. --no-wait returns as soon as\n" +
			"the VM process is up.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var imagePath string
			if len(args) == 1 {
				imagePath = args[0]
			} else {
				found := discoverImage(".")
				if found == "" {
					return fmt.Errorf("no disk image found in current directory\n" +
						"  Run `winkit build` first, or pass the image path explicitly:\n" +
						"    winkit start ./path/to/disk.qcow2")
				}
				imagePath = found
			}
			absImage, err := filepath.Abs(imagePath)
			if err != nil {
				return fmt.Errorf("resolving image path: %w", err)
			}
			if _, err := os.Stat(absImage); err != nil {
				return fmt.Errorf("image not found: %w", err)
			}

			// Load config for defaults (accel, etc.).
			cfg, _, _ := loadBuildConfig(configFile)

			if name == "" {
				name = nameFromImage(absImage)
			}
			if stateDir == "" {
				stateDir = vmstate.DefaultDir()
			}

			// winkit.yaml supplies defaults; explicit flags win.
			if cfg.Ports != nil {
				if !cmd.Flags().Changed("ssh-port") && cfg.Ports.Gossh != 0 {
					sshPort = uint16(cfg.Ports.Gossh)
				}
				if !cmd.Flags().Changed("rdp-port") && cfg.Ports.RDP != 0 {
					rdpPort = uint16(cfg.Ports.RDP)
				}
			}
			if hostname == "" {
				hostname = cfg.Hostname
			}
			// Extra forwards: winkit.yaml ports.forward plus --forward flags.
			var extraForwards []vm.PortForward
			if cfg.Ports != nil {
				forwards = append(cfg.Ports.Forward, forwards...)
			}
			for _, spec := range forwards {
				h, g, err := config.ParseForward(spec)
				if err != nil {
					return err
				}
				extraForwards = append(extraForwards, vm.PortForward{Host: uint16(h), Guest: uint16(g)})
			}

			var ctx context.Context
			var stop context.CancelFunc
			if foreground {
				ctx, stop = signal.NotifyContext(context.Background(), os.Interrupt)
			} else {
				ctx, stop = context.WithCancel(context.Background())
			}
			defer stop()

			ui := newRunUI(cmd)
			ui.Logger.Info("starting VM", "name", name)

			machine, err := winkit.Start(ctx, winkit.StartOpts{
				Image:      absImage,
				Name:       name,
				Hostname:   hostname,
				Accel:      accel,
				StateDir:   stateDir,
				CPUs:       cpus,
				MemoryGB:   memoryGB,
				SSHPort:    sshPort,
				RDPPort:    rdpPort,
				VNCPort:    vncPort,
				Forwards:   extraForwards,
				Foreground: foreground,
			})
			if err != nil {
				ui.Finish(err)
				return err
			}
			outDir := machine.OutputDir()

			// Hold the terminal until the guest answers SSH. A detached VM
			// keeps running if the user interrupts the wait; only the wait
			// itself is abandoned.
			var waitErr error
			if !noWait {
				sshAddr := fmt.Sprintf("127.0.0.1:%d", sshPort)
				ui.Logger.Info("waiting for SSH", "addr", sshAddr)
				waitCtx, cancelWait := signal.NotifyContext(ctx, os.Interrupt)
				if waitFor > 0 {
					var cancelTimeout context.CancelFunc
					waitCtx, cancelTimeout = context.WithTimeout(waitCtx, waitFor)
					defer cancelTimeout()
				}
				waitErr = waitForSSH(waitCtx, sshAddr, machine.Done(), func(elapsed time.Duration) {
					ui.Progress(formatElapsed(elapsed))
				})
				cancelWait()
				switch {
				case waitErr == nil:
				case errors.Is(waitErr, errVMExited):
					vmstate.Remove(stateDir, name)
					ui.Finish(waitErr)
					return fmt.Errorf("%w; see %s", waitErr, outDir)
				case errors.Is(waitErr, context.DeadlineExceeded):
					ui.Logger.Warn("SSH not reachable yet; VM left running", "waited", waitFor.String())
				case errors.Is(waitErr, context.Canceled) && !foreground:
					ui.Logger.Warn("wait interrupted; VM left running")
				}
			}
			ui.Finish(nil)

			fmt.Fprintf(cmd.OutOrStdout(), "VM %q started (PID %d)\n", name, machine.PID())
			fmt.Fprintf(cmd.OutOrStdout(), "  Image:  %s\n", absImage)
			fmt.Fprintf(cmd.OutOrStdout(), "  SSH:    winkit ssh %s\n", name)
			fmt.Fprintf(cmd.OutOrStdout(), "  RDP:    127.0.0.1:%d\n", rdpPort)
			fmt.Fprintf(cmd.OutOrStdout(), "  VNC:    127.0.0.1:%d\n", vncPort)
			if st, err := vmstate.Load(stateDir, name); err == nil && st != nil {
				for _, spec := range st.Forwards {
					if h, g, perr := config.ParseForward(spec); perr == nil {
						fmt.Fprintf(cmd.OutOrStdout(), "  Fwd:    127.0.0.1:%d -> guest :%d\n", h, g)
					}
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "  Logs:   winkit logs %s\n", name)
			fmt.Fprintf(cmd.OutOrStdout(), "  Stop:   winkit stop %s\n", name)

			if waitErr != nil && !foreground {
				if errors.Is(waitErr, context.DeadlineExceeded) {
					return fmt.Errorf("SSH not reachable after %s (VM still running; use --wait-timeout to wait longer)", waitFor)
				}
				return nil
			}

			if foreground {
				fmt.Fprintf(cmd.OutOrStdout(), "\nVM running in foreground. Press Ctrl+C to stop.\n")
				select {
				case <-machine.Done():
					fmt.Fprintln(cmd.ErrOrStderr(), "VM exited")
				case <-ctx.Done():
					fmt.Fprintln(cmd.ErrOrStderr(), "\nStopping VM...")
					machine.Stop()

					// Second Ctrl+C force-kills immediately.
					forceCh := make(chan os.Signal, 1)
					signal.Notify(forceCh, os.Interrupt)
					done := make(chan struct{})
					go func() { _ = machine.Wait(); close(done) }()
					select {
					case <-done:
					case <-forceCh:
						fmt.Fprintln(cmd.ErrOrStderr(), "Force killing VM")
					}
				}
				vmstate.Remove(stateDir, name)
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&configFile, "file", "f", "", "config file (default: ./winkit.yaml)")
	cmd.Flags().StringVar(&name, "name", "", "VM name (defaults to image filename without extension)")
	cmd.Flags().StringVar(&hostname, "hostname", "", "guest computer/NetBIOS name via SMBIOS serial; renamed on next boot (default: winkit.yaml hostname, else winkit)")
	cmd.Flags().StringVar(&accel, "accel", "", "QEMU accelerator (kvm, hvf, tcg)")
	cmd.Flags().BoolVar(&foreground, "foreground", false, "run in foreground (default: background)")
	cmd.Flags().Uint16Var(&vncPort, "vnc-port", 5900, "host VNC port")
	cmd.Flags().UintVar(&cpus, "cpus", 4, "number of vCPUs")
	cmd.Flags().Uint64Var(&memoryGB, "memory", 6, "memory in GB")
	cmd.Flags().Uint16Var(&sshPort, "ssh-port", 20022, "host SSH port")
	cmd.Flags().Uint16Var(&rdpPort, "rdp-port", 23389, "host RDP port")
	cmd.Flags().StringVar(&stateDir, "state-dir", "", "state directory (default ~/.winkit/run/)")
	cmd.Flags().StringArrayVar(&forwards, "forward", nil, "extra host:guest TCP forward (repeatable; adds to winkit.yaml ports.forward)")
	cmd.Flags().BoolVar(&noWait, "no-wait", false, "return as soon as the VM process is up, without waiting for SSH")
	cmd.Flags().DurationVar(&waitFor, "wait-timeout", 10*time.Minute, "how long to wait for SSH before giving up (0 = forever)")

	return cmd
}

// nameFromImage derives a VM name from the image path by stripping the
// directory and extension.
func nameFromImage(path string) string {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	return strings.TrimSuffix(base, ext)
}

// discoverImage finds a winkit build output in dir. It checks the
// conventional names in order of most to least common stage.
func discoverImage(dir string) string {
	for _, name := range []string{
		"winkit-full.qcow2",
		"winkit-full-wsl.qcow2",
		"winkit-pe.qcow2",
		"winkit-pe-wsl.qcow2",
		"winkit-full.raw",
		"winkit-full-wsl.raw",
	} {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}
