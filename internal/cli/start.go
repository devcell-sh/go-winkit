package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	winkit "github.com/devcell-sh/go-winkit"
	"github.com/devcell-sh/go-winkit/gosshd"
	"github.com/devcell-sh/go-winkit/vm/vmstate"
)

func newStartCmd() *cobra.Command {
	var (
		configFile string
		name       string
		hostname   string
		accel      string
		vnc        bool
		foreground bool
		cpus       uint
		memoryGB   uint64
		sshPort    uint16
		rdpPort    uint16
		stateDir   string
	)

	cmd := &cobra.Command{
		Use:   "start [image]",
		Short: "Boot a built Windows VM image",
		Long: "Start a VM from a previously built disk image.\n" +
			"When no image is given, discovers the build output in\n" +
			"the current directory (winkit-base.qcow2, etc.).\n" +
			"Loads defaults from winkit.yaml if present.\n" +
			"Prints connection info (SSH, RDP, VNC ports) and writes\n" +
			"state so `winkit status` and `winkit stop` can manage it.",
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

			var ctx context.Context
			var stop context.CancelFunc
			if foreground {
				ctx, stop = signal.NotifyContext(context.Background(), os.Interrupt)
			} else {
				ctx, stop = context.WithCancel(context.Background())
			}
			defer stop()

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
				Foreground: foreground,
				VNC:        vnc,
			})
			if err != nil {
				return err
			}
			outDir := machine.OutputDir()
			var vncPort uint16
			if vnc {
				vncPort = 5900
			}

			fmt.Fprintf(cmd.OutOrStdout(), "VM %q started (PID %d)\n", name, machine.PID())
			fmt.Fprintf(cmd.OutOrStdout(), "  Image:  %s\n", absImage)
			fmt.Fprintf(cmd.OutOrStdout(), "  SSH:    ssh -p %d %s@127.0.0.1\n", sshPort, gosshd.DefaultUser)
			fmt.Fprintf(cmd.OutOrStdout(), "  RDP:    127.0.0.1:%d\n", rdpPort)
			if vncPort > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "  VNC:    127.0.0.1:%d\n", vncPort)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "  Logs:   %s\n", outDir)
			fmt.Fprintf(cmd.OutOrStdout(), "  Stop:   winkit stop\n")

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
	cmd.Flags().BoolVar(&vnc, "vnc", false, "enable VNC display on port 5900")
	cmd.Flags().UintVar(&cpus, "cpus", 4, "number of vCPUs")
	cmd.Flags().Uint64Var(&memoryGB, "memory", 6, "memory in GB")
	cmd.Flags().Uint16Var(&sshPort, "ssh-port", 20022, "host SSH port")
	cmd.Flags().Uint16Var(&rdpPort, "rdp-port", 23389, "host RDP port")
	cmd.Flags().StringVar(&stateDir, "state-dir", "", "state directory (default ~/.winkit/run/)")

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
		"winkit-base.qcow2",
		"winkit-wsl.qcow2",
		"winkit-core.qcow2",
		"winkit-base.raw",
		"winkit-wsl.raw",
	} {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}
