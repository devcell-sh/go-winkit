package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/devcell-sh/go-winkit/unattend"
)

// unattendFlags binds the AutounattendConfig fields a CLI user can set.
type unattendFlags struct {
	username     string
	password     string
	hostname     string
	locale       string
	timeZone     string
	sshPubKey    string
	imageName    string
	agentCommand string
	enableRDP    bool
	winPEAgent   bool
}

func (f *unattendFlags) register(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.username, "user", "", "guest account name (default: host $USER)")
	cmd.Flags().StringVar(&f.password, "password", "", "guest account password")
	cmd.Flags().StringVar(&f.hostname, "hostname", "", "guest hostname")
	cmd.Flags().StringVar(&f.locale, "locale", "", "input/system/user locale")
	cmd.Flags().StringVar(&f.timeZone, "timezone", "", "guest time zone")
	cmd.Flags().StringVar(&f.sshPubKey, "ssh-pubkey", "", "SSH public key, or @/path/to/key.pub")
	cmd.Flags().StringVar(&f.imageName, "image-name", "", "install.wim image name to select")
	cmd.Flags().StringVar(&f.agentCommand, "agent-command", "", "command pre-baked for the WinPE agent")
	cmd.Flags().BoolVar(&f.enableRDP, "rdp", false, "enable RDP in the guest")
	cmd.Flags().BoolVar(&f.winPEAgent, "winpe-agent", false, "ship the WinPE control agent on the answer volume")
}

func (f *unattendFlags) config() (unattend.Config, error) {
	cfg := unattend.DefaultConfig()
	if f.username != "" {
		cfg.Username = f.username
	}
	if f.password != "" {
		cfg.Password = f.password
	}
	if f.hostname != "" {
		cfg.Hostname = f.hostname
	}
	if f.locale != "" {
		cfg.Locale = f.locale
	}
	if f.timeZone != "" {
		cfg.TimeZone = f.timeZone
	}
	if f.imageName != "" {
		cfg.ImageName = f.imageName
	}
	cfg.AgentCommand = f.agentCommand
	cfg.EnableRDP = f.enableRDP
	cfg.WinPEAgent = f.winPEAgent

	key := f.sshPubKey
	if len(key) > 1 && key[0] == '@' {
		data, err := os.ReadFile(key[1:])
		if err != nil {
			return cfg, fmt.Errorf("reading SSH public key: %w", err)
		}
		key = string(data)
	}
	if key != "" {
		cfg.SSHPubKey = key
	}
	return cfg, nil
}

func newUnattendCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unattend",
		Short: "Generate and validate autounattend answer files",
	}
	cmd.AddCommand(
		newUnattendGenerateCmd(),
		newUnattendValidateCmd(),
		newUnattendVolumeCmd(),
	)
	return cmd
}

func newUnattendGenerateCmd() *cobra.Command {
	var (
		flags   unattendFlags
		outPath string
	)
	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate autounattend.xml",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := flags.config()
			if err != nil {
				return err
			}
			xml := unattend.GenerateXML(cfg)
			for _, verr := range unattend.Validate(xml) {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: %v\n", verr)
			}
			if outPath == "" || outPath == "-" {
				_, err = cmd.OutOrStdout().Write(xml)
				return err
			}
			return os.WriteFile(outPath, xml, 0o644)
		},
	}
	flags.register(cmd)
	cmd.Flags().StringVarP(&outPath, "out", "o", "", "output file (default: stdout)")
	return cmd
}

func newUnattendValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate <autounattend.xml>",
		Short: "Validate an answer file against the unattend schema rules",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := os.ReadFile(args[0])
			if err != nil {
				return err
			}
			errs := unattend.Validate(data)
			for _, verr := range errs {
				fmt.Fprintf(cmd.OutOrStdout(), "%v\n", verr)
			}
			if len(errs) > 0 {
				return fmt.Errorf("%d validation error(s)", len(errs))
			}
			fmt.Fprintln(cmd.OutOrStdout(), "valid")
			return nil
		},
	}
}

func newUnattendVolumeCmd() *cobra.Command {
	var flags unattendFlags
	cmd := &cobra.Command{
		Use:   "volume <out.img>",
		Short: "Build the FAT answer volume (autounattend.xml + provisioning payload)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := flags.config()
			if err != nil {
				return err
			}
			if err := unattend.BuildAnswerVolume(cfg, args[0]); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), args[0])
			return nil
		},
	}
	flags.register(cmd)
	return cmd
}
