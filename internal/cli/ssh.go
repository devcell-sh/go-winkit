package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/devcell-sh/go-winkit/gosshd"
	"github.com/devcell-sh/go-winkit/vm/vmstate"
)

func newSSHCmd() *cobra.Command {
	var stateDir string

	cmd := &cobra.Command{
		Use:   "ssh [name|image]",
		Short: "SSH into a running Windows VM",
		Long: "Open an SSH session to a VM started with `winkit start`.\n" +
			"With no argument, connects to the only running VM.\n" +
			"Extra arguments after `--` are passed to ssh.",
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			if stateDir == "" {
				stateDir = vmstate.DefaultDir()
			}

			// Separate [name] from extra ssh args after --.
			var target string
			var extraArgs []string
			if dash := cmd.ArgsLenAtDash(); dash >= 0 {
				if dash > 0 {
					target = args[0]
				}
				extraArgs = args[dash:]
			} else if len(args) > 0 {
				target = args[0]
			}

			var st *vmstate.State
			var err error
			if target != "" {
				st, err = resolveTarget(stateDir, target)
			} else {
				st, err = resolveOnlyVM(stateDir)
			}
			if err != nil {
				return err
			}

			sshArgs := []string{
				"ssh",
				"-o", "StrictHostKeyChecking=no",
				"-o", "UserKnownHostsFile=/dev/null",
				"-p", strconv.Itoa(int(st.SSHPort)),
			}
			sshArgs = append(sshArgs, extraArgs...)
			sshArgs = append(sshArgs, fmt.Sprintf("%s@127.0.0.1", gosshd.DefaultUser))

			sshBin, err := exec.LookPath("ssh")
			if err != nil {
				return fmt.Errorf("ssh not found in PATH: %w", err)
			}
			return syscall.Exec(sshBin, sshArgs, os.Environ())
		},
	}

	cmd.Flags().StringVar(&stateDir, "state-dir", "", "state directory (default ~/.winkit/run/)")
	return cmd
}
