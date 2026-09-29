package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/devcell-sh/go-winkit/vm/vmstate"
)

func newLogsCmd() *cobra.Command {
	var (
		stateDir string
		follow   bool
		source   string
	)

	cmd := &cobra.Command{
		Use:   "logs [name|image]",
		Short: "Stream logs from a running VM",
		Long: "Print guest logs from a running (or recently stopped) VM.\n" +
			"With --follow (-f), tails the log file continuously.\n" +
			"Sources: guest (default, structured JSONL from COM2),\n" +
			"serial (firmware/console), run (host-side events).",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if stateDir == "" {
				stateDir = vmstate.DefaultDir()
			}

			var st *vmstate.State
			if len(args) == 1 {
				var err error
				st, err = resolveTarget(stateDir, args[0])
				if err != nil {
					return err
				}
			} else {
				var err error
				st, err = resolveOnlyVM(stateDir)
				if err != nil {
					return err
				}
			}

			if st.OutputDir == "" {
				return fmt.Errorf("VM %q has no output directory", st.Name)
			}

			var logFile string
			switch source {
			case "guest":
				logFile = filepath.Join(st.OutputDir, "guest.jsonl")
			case "serial":
				logFile = filepath.Join(st.OutputDir, "serial.log")
			case "run":
				logFile = filepath.Join(st.OutputDir, "run.jsonl")
			default:
				return fmt.Errorf("unknown source %q (want guest, serial, or run)", source)
			}

			if !follow {
				return catFile(cmd.OutOrStdout(), logFile)
			}
			return tailFollow(cmd.OutOrStdout(), logFile, st.PID)
		},
	}

	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "follow log output (like tail -f)")
	cmd.Flags().StringVarP(&source, "source", "s", "guest", "log source: guest, serial, or run")
	cmd.Flags().StringVar(&stateDir, "state-dir", "", "state directory (default ~/.winkit/run/)")
	return cmd
}

func catFile(w io.Writer, path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no logs yet at %s", path)
		}
		return err
	}
	defer f.Close()
	_, err = io.Copy(w, f)
	return err
}

func tailFollow(w io.Writer, path string, pid int) error {
	for {
		if _, err := os.Stat(path); err == nil {
			break
		}
		if !vmstate.IsAlive(pid) {
			return fmt.Errorf("VM exited and no log found at %s", path)
		}
		time.Sleep(500 * time.Millisecond)
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	reader := bufio.NewReader(f)
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			fmt.Fprint(w, line)
		}
		if err == io.EOF {
			if !vmstate.IsAlive(pid) {
				return nil
			}
			time.Sleep(200 * time.Millisecond)
			continue
		}
		if err != nil {
			return err
		}
	}
}
