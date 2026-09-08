package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/devcell-sh/go-winkit/build/imageformat"
	"github.com/devcell-sh/go-winkit/internal/config"
	"github.com/devcell-sh/go-winkit/unattend"
)

func newVagrantfileCmd() *cobra.Command {
	var (
		configFile string
		force      bool
	)
	cmd := &cobra.Command{
		Use:   "vagrantfile [image.qcow2]",
		Short: "Generate a vagrant-qemu Vagrantfile for an already-built image",
		Long: "Generates a vagrant-qemu Vagrantfile next to an existing qcow2 image,\n" +
			"without rebuilding it (build --vagrant does both). Ports come from\n" +
			"winkit.yaml (or -f path); credentials match what the build provisioned.\n\n" +
			"Boot the result with:\n" +
			"  vagrant up",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			image := "./winkit-base.qcow2"
			if len(args) == 1 {
				image = args[0]
			}
			if !strings.HasSuffix(image, ".qcow2") {
				return fmt.Errorf("%s is not a .qcow2 image (vagrant-qemu boots a raw qcow2)", image)
			}

			cfg, _, err := loadBuildConfig(configFile)
			if err != nil {
				return err
			}
			if _, err := os.Stat(image); err != nil {
				return fmt.Errorf("image %s: %w (build one with: winkit build)", image, err)
			}

			vfPath := filepath.Join(filepath.Dir(image), "Vagrantfile")
			if _, err := os.Stat(vfPath); err == nil && !force {
				if !confirm(cmd, fmt.Sprintf("%s exists — overwrite? [y/N] ", vfPath)) {
					return fmt.Errorf("aborted: %s exists", vfPath)
				}
			}
			if err := imageformat.WriteVagrantfile(vfPath, vagrantOptsFor(cfg, filepath.Base(image))); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"%s -- boot with: vagrant up\n", vfPath)
			return nil
		},
	}
	cmd.Flags().StringVarP(&configFile, "file", "f", "", "config file path (default: ./winkit.yaml or ./winkit.yml)")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing Vagrantfile without asking")
	return cmd
}

// vagrantOptsFor maps the yaml config onto VagrantOpts, shared by
// `build --vagrant` and `vagrantfile`. The credentials are the same
// account the build provisions into the image. SSH port precedence:
// vagrant.ssh-port, then ports.openssh, then vagrant-qemu's 50022.
func vagrantOptsFor(cfg *config.Config, imageName string) imageformat.VagrantOpts {
	vopts := imageformat.VagrantOpts{
		ImageName: imageName,
		Username:  unattend.SessionUsername(),
		Password:  unattend.DefaultConfig().Password,
		Hostname:  cfg.Hostname,
	}
	if cfg.Ports != nil {
		vopts.SSHPort = cfg.Ports.OpenSSH
		vopts.RDPPort = cfg.Ports.RDP
	}
	if cfg.Vagrant != nil && cfg.Vagrant.SSHPort != 0 {
		vopts.SSHPort = cfg.Vagrant.SSHPort
	}
	return vopts
}
