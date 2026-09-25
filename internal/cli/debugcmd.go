package cli

import "github.com/spf13/cobra"

func newDebugGroupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "debug",
		Short: "Developer inspection and pre-caching tools",
		Long: "Commands for developers and CI: pre-cache install media,\n" +
			"read guest diagnostics, and inspect WIM capabilities.\n\n" +
			"These are not part of the normal build-run lifecycle.",
	}
	cmd.AddCommand(
		newFetchCmd(),
		newDiagCmd(),
		newFeaturesCmd(),
	)
	return cmd
}
