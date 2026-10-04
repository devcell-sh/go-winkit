package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/devcell-sh/go-winkit/gosshd"
	"github.com/devcell-sh/go-winkit/vm/vmstate"
)

// guestRecvCommand is what the guest runs to receive a file: winkit-service
// reads the session's stdin into the destination. WinPE images carry the
// service on the X: ramdisk, full images at the root of C:. cmd.exe picks
// whichever exists. Quotes are avoided on purpose: gosshd hands the
// request to cmd.exe as one argument and Go's Windows quoting turns any
// embedded quote into \", which cmd then takes literally.
func guestRecvCommand(dst string) string {
	return fmt.Sprintf(`if exist X:\winkit\winkit-service.exe (X:\winkit\winkit-service.exe recv --to %s) else (C:\winkit-service.exe recv --to %s)`, dst, dst)
}

// splitCopyTarget parses "<vm>:<guest-path>". A guest path on its own
// ("E:\apps\x.exe") is recognised by its single-letter drive prefix and
// means "the only running VM".
func splitCopyTarget(target string) (vmName, guestPath string, err error) {
	i := strings.IndexByte(target, ':')
	if i < 0 {
		return "", "", fmt.Errorf("destination %q must be <vm>:<guest-path> or a guest path like E:\\apps\\tool.exe", target)
	}
	if i == 1 {
		return "", target, nil
	}
	vmName, guestPath = target[:i], target[i+1:]
	if guestPath == "" {
		return "", "", fmt.Errorf("destination %q has no guest path after the VM name", target)
	}
	return vmName, guestPath, nil
}

func newCpCmd() *cobra.Command {
	var stateDir string
	var timeout time.Duration

	cmd := &cobra.Command{
		Use:   "cp <local-file> [vm:]<guest-path>",
		Short: "Copy a file from the host into a running VM",
		Long: "Stream a local file over gosshd into a Windows path on the guest.\n" +
			"The destination is <vm>:<guest-path>, or just a guest path such as\n" +
			"E:\\apps\\tool.exe when only one VM is running. Parent directories are\n" +
			"created. Guest paths with spaces are not supported yet.\n\n" +
			"  winkit cp ./putty.exe winkit-pe-wsl:E:\\apps\\putty.exe",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if stateDir == "" {
				stateDir = vmstate.DefaultDir()
			}
			vmName, guestPath, err := splitCopyTarget(args[1])
			if err != nil {
				return err
			}
			if strings.ContainsAny(guestPath, " \"") {
				return fmt.Errorf("guest path %q: spaces and quotes are not supported", guestPath)
			}
			var st *vmstate.State
			if vmName != "" {
				st, err = resolveTarget(stateDir, vmName)
			} else {
				st, err = resolveOnlyVM(stateDir)
			}
			if err != nil {
				return err
			}

			f, err := os.Open(args[0])
			if err != nil {
				return err
			}
			defer f.Close()
			info, err := f.Stat()
			if err != nil {
				return err
			}
			if info.IsDir() {
				return fmt.Errorf("%s is a directory; cp copies single files", args[0])
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			addr := fmt.Sprintf("127.0.0.1:%d", st.SSHPort)
			client, err := gosshd.Dial(ctx, addr)
			if err != nil {
				return fmt.Errorf("connecting to %s: %w", st.Name, err)
			}
			defer client.Close()

			var out strings.Builder
			code, err := client.RunInput(ctx, guestRecvCommand(guestPath), f, &out, io.Discard)
			if err != nil {
				return fmt.Errorf("copying to %s: %w", st.Name, err)
			}
			if code != 0 {
				return fmt.Errorf("guest refused the copy (exit %d): %s", code, strings.TrimSpace(out.String()))
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s -> %s:%s (%d bytes)\n", args[0], st.Name, guestPath, info.Size())
			return nil
		},
	}

	cmd.Flags().StringVar(&stateDir, "state-dir", "", "state directory (default ~/.winkit/run/)")
	cmd.Flags().DurationVar(&timeout, "timeout", 10*time.Minute, "give up after this long")
	return cmd
}
