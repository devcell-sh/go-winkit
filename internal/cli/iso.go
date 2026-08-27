package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/devcell-sh/go-winkit/isokit"
)

func newISOCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "iso",
		Short: "Create and inspect bootable ISOs",
	}
	cmd.AddCommand(
		newISOCreateCmd(),
		newISOInspectCmd(),
		newISODiagnoseCmd(),
		newISOExtractCmd(),
	)
	return cmd
}

func newISOCreateCmd() *cobra.Command {
	var (
		stageDir string
		label    string
	)
	cmd := &cobra.Command{
		Use:   "create <out.iso>",
		Short: "Build a bootable Windows ISO from a stage directory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := isokit.CreateWindowsISO(args[0], stageDir, label); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), args[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&stageDir, "stage", "", "stage directory (from `winkit winpe stage` or an assembled tree)")
	cmd.Flags().StringVar(&label, "label", "WINKIT", "volume label")
	cmd.MarkFlagRequired("stage")
	return cmd
}

func newISOInspectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inspect <iso>",
		Short: "Show El Torito boot info and EFI bootability",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			info, err := isokit.InspectElTorito(args[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%+v\n", *info)
			if err := isokit.RequireEFIBootable(args[0]); err != nil {
				return fmt.Errorf("not EFI bootable: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "EFI bootable: yes")
			return nil
		},
	}
	return cmd
}

func newISODiagnoseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "diagnose <iso>",
		Short: "Print a low-level diagnosis of an ISO's structure",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), isokit.DiagnoseISO(args[0]))
			return nil
		},
	}
}

func newISOExtractCmd() *cobra.Command {
	var outPath string
	cmd := &cobra.Command{
		Use:   "extract <iso> <path-in-iso>",
		Short: "Extract a single file from an ISO",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := isokit.ReadFileFromISO(args[0], args[1])
			if err != nil {
				return err
			}
			if outPath == "-" {
				_, err = cmd.OutOrStdout().Write(data)
				return err
			}
			if outPath == "" {
				outPath = filepath.Base(args[1])
			}
			if err := os.WriteFile(outPath, data, 0o644); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), outPath)
			return nil
		},
	}
	cmd.Flags().StringVarP(&outPath, "out", "o", "", "output file (default: basename in cwd, '-' for stdout)")
	return cmd
}
