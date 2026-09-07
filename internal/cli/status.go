package cli

import (
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/devcell-sh/go-winkit/vmstate"
)

func newStatusCmd() *cobra.Command {
	var stateDir string

	cmd := &cobra.Command{
		Use:   "status",
		Short: "List running Windows VMs",
		Long:  "Show all VMs started with `winkit start`, their PID, ports, and uptime.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if stateDir == "" {
				stateDir = vmstate.DefaultDir()
			}

			states, err := vmstate.List(stateDir)
			if err != nil {
				return fmt.Errorf("listing VMs: %w", err)
			}

			if len(states) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No running VMs")
				return nil
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tSTATUS\tPID\tBACKEND\tSSH\tRDP\tVNC\tUPTIME\tIMAGE")
			for _, s := range states {
				status := "running"
				if !vmstate.IsAlive(s.PID) {
					status = "stale"
				}

				uptime := formatUptime(time.Since(s.StartedAt))

				vnc := "-"
				if s.VNCPort > 0 {
					vnc = fmt.Sprintf(":%d", s.VNCPort)
				}

				fmt.Fprintf(w, "%s\t%s\t%d\t%s\t:%d\t:%d\t%s\t%s\t%s\n",
					s.Name, status, s.PID, s.Backend,
					s.SSHPort, s.RDPPort, vnc, uptime, s.ImagePath)
			}
			w.Flush()
			return nil
		},
	}

	cmd.Flags().StringVar(&stateDir, "state-dir", "", "state directory (default ~/.winkit/run/)")
	return cmd
}

func formatUptime(d time.Duration) string {
	d = d.Truncate(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh%dm", h, m)
	}
	if m > 0 {
		return fmt.Sprintf("%dm%ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}
