package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/devcell-sh/go-winkit/diag"
	"github.com/devcell-sh/go-winkit/unattend"
)

func newDiagCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "diag",
		Short: "Read diagnostics back from a guest",
	}
	cmd.AddCommand(
		newDiagGuestCmd(),
		newDiagPantherCmd(),
	)
	return cmd
}

func newDiagGuestCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "guest <answer.img>",
		Short: "Read the guest's diagnostic report off the answer volume image",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			report, err := unattend.ReadGuestDiagnostics(args[0])
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), report)
			return nil
		},
	}
}

func newDiagPantherCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "panther",
		Short: "List the Windows Setup log paths worth recovering post-mortem",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), "# Setup (Panther) logs:")
			for _, p := range diag.PantherLogPaths() {
				fmt.Fprintln(cmd.OutOrStdout(), p)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "# Guest provisioning logs:")
			for _, p := range diag.GuestLogPaths() {
				fmt.Fprintln(cmd.OutOrStdout(), p)
			}
			return nil
		},
	}
}
