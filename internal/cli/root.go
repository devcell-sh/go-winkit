// Package cli implements the winkit command tree. Each command is a thin
// wrapper over one library entry point; anything that needs real logic
// belongs in the library packages, not here.
package cli

import (
	"github.com/spf13/cobra"
)

// NewRootCmd builds the winkit command tree.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "winkit",
		Short: "Build Windows environments from code",
		Long: "winkit builds Windows environments from code: fetches install media,\n" +
			"turns it into something bootable, and prepares the answer files and\n" +
			"payloads needed to run the whole path unattended.\n\n" +
			"WIM-modifying commands (winpe build, wim ...) need wimlib:\n" +
			"build the CLI with -tags wimlib and have libwim installed.",
		SilenceUsage:  true,
		SilenceErrors: false,
	}

	root.AddCommand(
		newFetchCmd(),
		newISOCmd(),
		newUnattendCmd(),
		newWinPECmd(),
		newWimCmd(),
		newDiagCmd(),
	)
	return root
}
