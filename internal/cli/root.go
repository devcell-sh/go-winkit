// Package cli implements the winkit command tree. Each command is a thin
// wrapper over one library entry point; anything that needs real logic
// belongs in the library packages, not here.
package cli

import (
	"github.com/spf13/cobra"

	"github.com/devcell-sh/go-winkit/iotrace"
)

// NewRootCmd builds the winkit command tree.
func NewRootCmd() *cobra.Command {
	var (
		debug     bool
		debugJSON bool
	)

	root := &cobra.Command{
		Use:   "winkit",
		Short: "Build Windows environments from code",
		Long: "winkit builds Windows environments from code: fetches install media,\n" +
			"turns it into something bootable, and prepares the answer files and\n" +
			"payloads needed to run the whole path unattended.\n\n" +
			"WIM-modifying commands (build, winpe build, wim ...) need wimlib:\n" +
			"build the CLI with -tags wimlib and have libwim installed.\n\n" +
			"Pass --debug to trace every IO operation: files read and written,\n" +
			"WIM images exported, images mastered, bytes downloaded. Building\n" +
			"Windows media is mostly opaque IO against large binaries, so a\n" +
			"trace is usually the fastest way to see what a failing step did.",
		SilenceUsage:  true,
		SilenceErrors: false,
		// Tracing is installed before any subcommand runs so it covers the
		// whole tree rather than each command opting in.
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if !debug && !debugJSON {
				return nil
			}
			// Traces go to stderr, keeping stdout the machine-readable
			// result (a path) that callers pipe.
			if debugJSON {
				iotrace.SetDefault(iotrace.NewJSONLogger(cmd.ErrOrStderr()))
			} else {
				iotrace.SetDefault(iotrace.NewLogger(cmd.ErrOrStderr()))
			}
			return nil
		},
	}

	root.PersistentFlags().BoolVar(&debug, "debug", false,
		"full debug output: timestamped debug-level logs plus a trace of every IO operation")
	root.PersistentFlags().BoolVar(&debugJSON, "debug-json", false,
		"trace every IO operation to stderr as JSONL")

	root.AddCommand(
		newInitCmd(),
		newBuildCmd(),
		newFetchCmd(),
		newISOCmd(),
		newUnattendCmd(),
		newWinPECmd(),
		newWimCmd(),
		newListFeaturesCmd(),
		newDiagCmd(),
		newStartCmd(),
		newStopCmd(),
		newStatusCmd(),
	)
	return root
}
