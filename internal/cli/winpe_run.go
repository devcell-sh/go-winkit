package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/spf13/cobra"

	"github.com/devcell-sh/go-winkit/winpe"
	"github.com/devcell-sh/go-winkit/winpe/qemu"
)

func newWinPERunCmd() *cobra.Command {
	var (
		isoPath      string
		virtioISO    string
		accel        string
		timeout      time.Duration
		pollInterval time.Duration
		outputDir    string
	)
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Build WinPE, boot via QEMU, run DISM, and extract winkit.wim",
		Long: "End-to-end WIM builder: builds a WinPE ISO, boots it in QEMU,\n" +
			"waits for the guest to finish DISM servicing, and extracts\n" +
			"the resulting winkit.wim.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			defer stop()

			if outputDir == "" {
				dir, err := os.MkdirTemp("", "winkit-run-*")
				if err != nil {
					return err
				}
				outputDir = dir
			}

			runner := qemu.NewRunner("", accel)

			cfg := winpe.RunConfig{
				Build: winpe.BuildConfig{
					WindowsISO: isoPath,
					VirtIOISO:  virtioISO,
					OutputDir:  outputDir,
					HyperV:     true,
					WSL2:       true,
					OpenSSH:    true,
					VirtIO:     true,
				},
				PollInterval: pollInterval,
				Timeout:      timeout,
			}

			fmt.Fprintf(cmd.ErrOrStderr(), "building WinPE artifacts\n")
			result, err := winpe.Run(ctx, runner, cfg)
			if err != nil {
				return err
			}

			wimPath := fmt.Sprintf("%s/winkit.wim", outputDir)
			if err := os.WriteFile(wimPath, result.DevcellWim, 0o644); err != nil {
				return fmt.Errorf("writing winkit.wim: %w", err)
			}

			fmt.Fprintln(cmd.OutOrStdout(), wimPath)
			return nil
		},
	}
	cmd.Flags().StringVar(&isoPath, "iso", "", "source Windows ISO (required)")
	cmd.Flags().StringVar(&virtioISO, "virtio-iso", "", "VirtIO drivers ISO")
	cmd.Flags().StringVar(&accel, "accel", "", "QEMU accelerator (kvm, hvf, tcg)")
	cmd.Flags().DurationVar(&timeout, "timeout", 15*time.Minute, "maximum wait for guest completion")
	cmd.Flags().DurationVar(&pollInterval, "poll-interval", 15*time.Second, "how often to poll the guest")
	cmd.Flags().StringVar(&outputDir, "output-dir", "", "directory for artifacts (defaults to temp)")
	cmd.MarkFlagRequired("iso")
	return cmd
}
