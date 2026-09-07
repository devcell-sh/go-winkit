package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/devcell-sh/go-winkit/build"
	"github.com/devcell-sh/go-winkit/gosshd"
	"github.com/devcell-sh/go-winkit/unattend"
	"github.com/devcell-sh/go-winkit/vm"
	"github.com/devcell-sh/go-winkit/vm/qemu"
	"github.com/devcell-sh/go-winkit/vm/vmstate"
	"github.com/devcell-sh/go-winkit/winpe"
)

func newStartCmd() *cobra.Command {
	var (
		configFile string
		name       string
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

			existing, err := vmstate.FindByImage(stateDir, absImage)
			if err == nil && existing != nil && vmstate.IsAlive(existing.PID) {
				return fmt.Errorf("VM %q is already running (PID %d) from image %s",
					existing.Name, existing.PID, absImage)
			}

			if accel == "" {
				accel = qemu.DefaultAccel()
			}
			if cpus == 0 {
				cpus = 4
			}
			if memoryGB == 0 {
				memoryGB = 6
			}
			if sshPort == 0 {
				sshPort = 20022
			}
			if rdpPort == 0 {
				rdpPort = 23389
			}
			_ = cfg

			var ctx context.Context
			var stop context.CancelFunc
			if foreground {
				ctx, stop = signal.NotifyContext(context.Background(), os.Interrupt)
			} else {
				ctx, stop = context.WithCancel(context.Background())
			}
			defer stop()

			backend, backendName, err := build.ResolveBackend()
			if err != nil {
				return err
			}

			outDir := filepath.Join(filepath.Dir(absImage), ".winkit", "run", name)
			if err := os.MkdirAll(outDir, 0o755); err != nil {
				return fmt.Errorf("creating output dir: %w", err)
			}

			hostLogFile, err := os.Create(filepath.Join(outDir, "host.jsonl"))
			if err != nil {
				return fmt.Errorf("creating host log: %w", err)
			}
			defer hostLogFile.Close()
			logger := slog.New(winpe.NewGuestEventHandler(hostLogFile))

			secure := strings.HasPrefix(accel, "tcg")
			var varsPath string
			if !secure {
				varsPath = build.FindSiblingVars(absImage)
				if varsPath != "" {
					dst := filepath.Join(outDir, "vars.fd")
					if err := copyFile(varsPath, dst); err != nil {
						return fmt.Errorf("copying vars: %w", err)
					}
					varsPath = dst
				}
				// When varsPath is empty, StartRun uses -kernel firmware
				// which auto-discovers the ESP bootloader without NVRAM.
			}

			displayType := "none"
			var vncPort uint16
			if vnc {
				displayType = "vnc=:0"
				vncPort = 5900
			}

			runCfg := vm.VMRunConfig{
				DiskPath:        absImage,
				OutputDir:       outDir,
				VMName:          "winkit-" + name,
				CPUs:            cpus,
				MemoryGB:        memoryGB,
				Accel:           accel,
				SSHPort:         sshPort,
				SSHGuestPort:    2222,
				OpenSSHHostPort: sshPort + 100,
				RDPPort:         rdpPort,
				SMBIOSSerial:    unattend.DefaultConfig().Hostname,
			}

			structuredLog := filepath.Join(outDir, name+".jsonl")

			switch backendName {
			case "qemu":
				runCfg.BackendExtra = &qemu.RunOptions{
					VarsPath:          varsPath,
					Secure:            secure,
					DisplayType:       displayType,
					Detach:            !foreground,
					StructuredLogPath: structuredLog,
				}
			}

			logger.Info("starting VM",
				"image", absImage, "accel", accel, "backend", backendName,
				"cpus", cpus, "memory_gb", memoryGB)

			machine, err := backend.StartRun(ctx, runCfg)
			if err != nil {
				return fmt.Errorf("starting VM: %w", err)
			}

			logger.Info("VM started", "pid", machine.PID(), "name", name)

			st := &vmstate.State{
				Name:      name,
				ImagePath: absImage,
				PID:       machine.PID(),
				Backend:   backendName,
				StartedAt: time.Now(),
				SSHPort:   sshPort,
				RDPPort:   rdpPort,
				VNCPort:   vncPort,
				Accel:     accel,
				OutputDir: outDir,
			}
			if err := vmstate.Save(stateDir, st); err != nil {
				machine.Stop()
				return fmt.Errorf("saving state: %w", err)
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
					logger.Info("VM exited")
					fmt.Fprintln(cmd.ErrOrStderr(), "VM exited")
				case <-ctx.Done():
					logger.Info("interrupt received, stopping VM")
					fmt.Fprintln(cmd.ErrOrStderr(), "\nStopping VM...")
					machine.Stop()

					// Second Ctrl+C force-kills immediately.
					forceCh := make(chan os.Signal, 1)
					signal.Notify(forceCh, os.Interrupt)
					done := make(chan struct{})
					go func() { _ = machine.Wait(); close(done) }()
					select {
					case <-done:
						logger.Info("VM stopped gracefully")
					case <-forceCh:
						logger.Info("force killing VM")
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
		"wsl2.qcow2",
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
