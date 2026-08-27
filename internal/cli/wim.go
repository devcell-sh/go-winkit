package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/devcell-sh/go-winkit/wim"
)

func newWimCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "wim",
		Short: "Patch and inject into WIM images (requires -tags wimlib)",
	}
	cmd.AddCommand(
		newWimPatchCmd(),
		newWimInjectCmd(),
	)
	return cmd
}

func newWimPatchCmd() *cobra.Command {
	var (
		imageNum int
		hyperv   bool
	)
	cmd := &cobra.Command{
		Use:   "patch <image.wim>",
		Short: "Apply offline registry patches to a WIM image",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var patches []wim.RegistryPatch
			if hyperv {
				patches = append(patches, wim.HyperVBootPatches())
			}
			if len(patches) == 0 {
				return fmt.Errorf("no patches selected (use --hyperv)")
			}
			if err := wim.PatchDevcellWim(args[0], imageNum, patches...); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), args[0])
			return nil
		},
	}
	cmd.Flags().IntVar(&imageNum, "image", 1, "WIM image number")
	cmd.Flags().BoolVar(&hyperv, "hyperv", false, "apply Hyper-V boot service patches")
	return cmd
}

func newWimInjectCmd() *cobra.Command {
	var (
		payloadDir string
		hyperv     bool
	)
	cmd := &cobra.Command{
		Use:   "inject <boot.wim>",
		Short: "Inject a WinPE payload directory into boot.wim image 2",
		Long: "Injects a payload directory into boot.wim image 2: winpeshl.ini goes to\n" +
			"System32, the whole directory lands at X:\\devcell. The WIM is modified\n" +
			"in place.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var patches []wim.RegistryPatch
			if hyperv {
				patches = append(patches, wim.HyperVBootPatches())
			}
			if err := wim.InjectWinPEPayload(args[0], payloadDir, patches...); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), args[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&payloadDir, "dir", "", "payload directory (must contain winpeshl.ini)")
	cmd.Flags().BoolVar(&hyperv, "hyperv", false, "also apply Hyper-V boot registry patches")
	cmd.MarkFlagRequired("dir")
	return cmd
}
