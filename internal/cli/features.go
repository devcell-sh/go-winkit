package cli

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/devcell-sh/go-winkit/winpe"
)

func newListFeaturesCmd() *cobra.Command {
	var (
		image int
		path  string
	)
	cmd := &cobra.Command{
		Use:   "list-features <image.wim>",
		Short: "Show the capabilities baked into a WIM image",
		Long: "Inspects a WIM image and prints the winkit-relevant capabilities it\n" +
			"carries, grouped by area: the winkit payload, the event-log/ETW\n" +
			"stack. A ✓ means the binary, service,\n" +
			"or channel is present; – means absent.\n\n" +
			"--image selects the WIM image (2 = WinPE boot.wim, the default; 1 =\n" +
			"install.wim's first edition). --path <dir> switches to browse mode:\n" +
			"it lists that directory's children in the image instead of the\n" +
			"capability groups (e.g. --path \\Windows\\System32\\winevt\\Logs).\n\n" +
			"Requires a wimlib-enabled build (-tags wimlib).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireWimlib(); err != nil {
				return err
			}
			if path != "" {
				kids, err := winpe.ListImageDir(args[0], image, path)
				if err != nil {
					return err
				}
				out := cmd.OutOrStdout()
				fmt.Fprintf(out, "%s (image %d): %d entries\n", path, image, len(kids))
				for _, k := range kids {
					fmt.Fprintf(out, "  %s\n", k)
				}
				return nil
			}
			groups, err := winpe.InspectFeatures(args[0], image)
			if err != nil {
				return err
			}
			renderFeatures(cmd.OutOrStdout(), groups)
			return nil
		},
	}
	cmd.Flags().IntVar(&image, "image", 2, "WIM image index (2=WinPE boot.wim, 1=install.wim edition)")
	cmd.Flags().StringVar(&path, "path", "", "browse mode: list this directory's children instead of features")
	return cmd
}

// renderFeatures prints each group as a small aligned table.
func renderFeatures(w io.Writer, groups []winpe.FeatureGroup) {
	for _, g := range groups {
		present := 0
		for _, f := range g.Items {
			if f.Present {
				present++
			}
		}
		fmt.Fprintf(w, "\n%s  (%d/%d)\n", g.Name, present, len(g.Items))
		tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
		for _, f := range g.Items {
			mark := "–"
			if f.Present {
				mark = "✓"
			}
			fmt.Fprintf(tw, "  %s\t%s\t%s\n", mark, f.Name, f.Detail)
		}
		tw.Flush()
	}
}
