package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/devcell-sh/go-winkit/winpe"
)

func newWimCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "wim",
		Short: "Patch and inject into WIM images (requires -tags wimlib)",
	}
	cmd.AddCommand(
		newWimInjectCmd(),
	)
	return cmd
}

func newWimInjectCmd() *cobra.Command {
	var payloadDir string
	cmd := &cobra.Command{
		Use:   "inject <boot.wim>",
		Short: "Inject a WinPE payload directory into boot.wim image 2",
		Long: "Injects a payload directory into boot.wim image 2: winpeshl.ini goes to\n" +
			"System32, the whole directory lands at X:\\winkit. The WIM is modified\n" +
			"in place.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := winpe.InjectWinPEPayload(args[0], payloadDir); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), args[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&payloadDir, "dir", "", "payload directory (must contain winpeshl.ini)")
	cmd.MarkFlagRequired("dir")
	return cmd
}
