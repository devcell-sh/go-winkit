package cli

import "github.com/spf13/cobra"

func newExportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Generate build artifacts: ISOs, WIMs, answer files, Vagrantfiles",
		Long: "Commands that produce or modify build artifacts outside the\n" +
			"normal `winkit build` pipeline: standalone ISOs, WinPE images,\n" +
			"WIM patches, autounattend answer files, and Vagrantfiles.",
	}
	cmd.AddCommand(
		newVagrantCmd(),
		newUnattendCmd(),
		newISOCmd(),
		newWimCmd(),
		newWinPECmd(),
	)
	return cmd
}
