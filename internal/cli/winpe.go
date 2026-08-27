package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/devcell-sh/go-winkit/isokit"
	"github.com/devcell-sh/go-winkit/wim"
	"github.com/devcell-sh/go-winkit/winpe"
)

func newWinPECmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "winpe",
		Short: "Stage and build WinPE ISOs",
	}
	cmd.AddCommand(
		newWinPEStageCmd(),
		newWinPEBuildCmd(),
	)
	return cmd
}

func newWinPEStageCmd() *cobra.Command {
	var isoPath string
	cmd := &cobra.Command{
		Use:   "stage <stage-dir>",
		Short: "Extract EFI boot files and boot.wim from a Windows ISO into a stage tree",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := winpe.ExtractStage(isoPath, args[0]); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), args[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&isoPath, "iso", "", "source Windows ISO")
	cmd.MarkFlagRequired("iso")
	return cmd
}

func newWinPEBuildCmd() *cobra.Command {
	var (
		isoPath   string
		label     string
		withSSHD  bool
		gosshdExe string
		arch      string
		extraDir  string
		hyperv    bool
	)
	cmd := &cobra.Command{
		Use:   "build <out.iso>",
		Short: "Build a standalone WinPE ISO, optionally with gosshd running",
		Long: "Builds a bootable WinPE-only ISO from a Windows ISO: extracts the boot\n" +
			"stage, injects a payload into boot.wim, and masters the result.\n\n" +
			"With --gosshd, the payload runs winkit's SSH server as the WinPE shell:\n" +
			"it initialises WinPE (wpeinit), drops the firewall, and serves SSH in\n" +
			"the foreground so the guest stays up. Requires a wimlib-enabled build\n" +
			"(-tags wimlib).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			outISO := args[0]
			workDir, err := os.MkdirTemp("", "winkit-winpe-*")
			if err != nil {
				return err
			}
			defer os.RemoveAll(workDir)

			stageDir := filepath.Join(workDir, "stage")
			fmt.Fprintf(cmd.ErrOrStderr(), "staging %s\n", isoPath)
			if err := winpe.ExtractStage(isoPath, stageDir); err != nil {
				return err
			}

			payloadDir := filepath.Join(workDir, "payload")
			if err := os.MkdirAll(payloadDir, 0o755); err != nil {
				return err
			}

			if withSSHD || gosshdExe != "" {
				if gosshdExe == "" {
					gosshdExe = filepath.Join(workDir, "gosshd.exe")
					fmt.Fprintf(cmd.ErrOrStderr(), "cross-compiling gosshd for windows/%s\n", arch)
					if err := buildGosshd(gosshdExe, arch); err != nil {
						return fmt.Errorf("building gosshd (pass --gosshd-exe to use a prebuilt binary): %w", err)
					}
				}
				if err := copyFile(gosshdExe, filepath.Join(payloadDir, "gosshd.exe")); err != nil {
					return err
				}
				// gosshd.cmd is the WinPE shell: wpeinit brings up networking,
				// the firewall would otherwise silently drop port 22, and
				// running gosshd in the foreground keeps WinPE from rebooting
				// when the shell app exits.
				gosshdCmd := "@echo off\r\n" +
					"wpeinit\r\n" +
					"wpeutil DisableFirewall\r\n" +
					`X:\devcell\gosshd.exe X:\devcell\gosshd.log` + "\r\n"
				if err := os.WriteFile(filepath.Join(payloadDir, "gosshd.cmd"), []byte(gosshdCmd), 0o644); err != nil {
					return err
				}
				shellINI := "[LaunchApps]\r\n" + `X:\devcell\gosshd.cmd` + "\r\n"
				if err := os.WriteFile(filepath.Join(payloadDir, "winpeshl.ini"), []byte(shellINI), 0o644); err != nil {
					return err
				}
			} else {
				// Standard payload: cmd shim → pwsh bootstrap → control agent,
				// kept alive by the synchronous agent loop (standalone WinPE
				// reboots the moment winpeshl's apps return).
				pcfg := winpe.PayloadConfig{WPEInit: true, SyncAgent: true}
				files := map[string][]byte{
					"winpeshl.ini":  winpe.GenerateShellINI_NoSetup(),
					"bootstrap.cmd": winpe.GenerateBootstrapCmd(),
					"bootstrap.ps1": winpe.GenerateBootstrap(pcfg),
					"agent.ps1":     winpe.GenerateAgent(pcfg),
				}
				for name, data := range files {
					if err := os.WriteFile(filepath.Join(payloadDir, name), data, 0o644); err != nil {
						return err
					}
				}
			}

			if extraDir != "" {
				if err := copyTree(extraDir, payloadDir); err != nil {
					return fmt.Errorf("copying --extra files: %w", err)
				}
			}

			var patches []wim.RegistryPatch
			if hyperv {
				patches = append(patches, wim.HyperVBootPatches())
			}
			bootWim := filepath.Join(stageDir, "sources", "boot.wim")
			fmt.Fprintf(cmd.ErrOrStderr(), "injecting payload into %s\n", bootWim)
			if err := wim.InjectWinPEPayload(bootWim, payloadDir, patches...); err != nil {
				return err
			}

			fmt.Fprintf(cmd.ErrOrStderr(), "mastering %s\n", outISO)
			if err := isokit.CreateWindowsISO(outISO, stageDir, label); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), outISO)
			return nil
		},
	}
	cmd.Flags().StringVar(&isoPath, "iso", "", "source Windows ISO")
	cmd.Flags().StringVar(&label, "label", "WINKIT_PE", "volume label")
	cmd.Flags().BoolVar(&withSSHD, "gosshd", false, "run gosshd as the WinPE shell (cross-compiled on the fly)")
	cmd.Flags().StringVar(&gosshdExe, "gosshd-exe", "", "prebuilt gosshd.exe to inject (implies --gosshd)")
	cmd.Flags().StringVar(&arch, "arch", "arm64", "guest architecture for gosshd (arm64 or amd64)")
	cmd.Flags().StringVar(&extraDir, "extra", "", "directory of extra files to add to the payload")
	cmd.Flags().BoolVar(&hyperv, "hyperv", false, "apply Hyper-V boot registry patches to boot.wim")
	cmd.MarkFlagRequired("iso")
	return cmd
}

// buildGosshd cross-compiles the gosshd guest binary with the local Go
// toolchain, resolving the package source through the module cache so it
// works both inside the winkit repo and in any module that depends on it.
func buildGosshd(dst, arch string) error {
	pkg := "github.com/devcell-sh/go-winkit/gosshd/cmd/gosshd"
	build := exec.Command("go", "build", "-o", dst, pkg)
	build.Env = append(os.Environ(), "GOOS=windows", "GOARCH="+arch, "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("go build %s: %w\n%s", pkg, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

func copyTree(srcDir, dstDir string) error {
	return filepath.WalkDir(srcDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(dstDir, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		return copyFile(path, dst)
	})
}
