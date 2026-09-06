package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/devcell-sh/go-wimlib"
	"github.com/devcell-sh/go-winkit/cache"
	"github.com/devcell-sh/go-winkit/imageformat"
	"github.com/devcell-sh/go-winkit/mctcatalog"
	"github.com/devcell-sh/go-winkit/uupdump"
	"github.com/devcell-sh/go-winkit/virtio"
	"github.com/devcell-sh/go-winkit/winpe"
	"github.com/devcell-sh/go-winkit/winpe/qemu"
)

// qcow2 capacity for the base image: the boot payload is ~700MB and the
// guest writes logs back to the volume.
const baseImageCapacity = 4 * 1024 * 1024 * 1024

// stageSpec declares per-stage accelerator constraints. BuildAccel lists
// accelerators valid for the install/build VM; RunAccel lists those valid
// for the verify/continue VM (where the produced disk is booted). A nil
// RunAccel means any accelerator works for running.
type stageSpec struct {
	buildAccel []string
	runAccel   []string
}

var stageSpecs = map[string]stageSpec{
	"core": {buildAccel: []string{"hvf", "kvm", "tcg"}},
	"base": {buildAccel: []string{"hvf", "kvm", "tcg"}},
	"wsl": {
		buildAccel: []string{"hvf", "kvm", "tcg"},
		runAccel:   []string{"tcg", "kvm"},
	},
}

// accelBase strips trailing options ("tcg,thread=multi" → "tcg") for
// constraint checking.
func accelBase(accel string) string {
	if i := strings.IndexByte(accel, ','); i >= 0 {
		return accel[:i]
	}
	return accel
}

// validAccel reports whether accel is in the allowed list.
func validAccel(accel string, allowed []string) bool {
	base := accelBase(accel)
	for _, a := range allowed {
		if a == base {
			return true
		}
	}
	return false
}

func newBuildCmd() *cobra.Command {
	var (
		stage         string
		imageTypeFlag string
		accel         string
		spec          uupdump.MediaSpec
		noCache       bool
		nixHome       string
		cacheDir      string
		force         bool
		noHypervisor  bool
	)
	cmd := &cobra.Command{
		Use:   "build [dest]",
		Short: "Build a bootable Windows image",
		Long: "Builds a bootable Windows image from the cached Windows and virtio-win\n" +
			"ISOs (downloading them if absent).\n\n" +
			"Stages (each includes everything from the previous stage):\n" +
			"  core — install Windows with gosshd and virtio drivers enabled.\n" +
			"  A minimal WinPE boot volume.\n" +
			"  base (default) — core plus OpenSSH, RDP, pwsh, and standard\n" +
			"  provisioning. Ready for general use.\n" +
			"  wsl — a full unattended Windows install to disk (not WinPE):\n" +
			"  installs Windows, brings up SSH+RDP, then enables VirtualMachine-\n" +
			"  Platform/WSL/Hyper-V online. The installed OS actually launches the\n" +
			"  hypervisor (unlike WinPE). Long-running; requires the MCT lane.\n\n" +
			"dest may be a directory (default ./) or a .qcow2 path. Requires a\n" +
			"wimlib-enabled build (-tags wimlib).",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireWimlib(); err != nil {
				return err
			}
			switch stage {
			case "core":
				stage = "core"
			case "base", "default", "":
				stage = "base"
			case "wsl", "wsl2": // "wsl2" is the deprecated old name
				stage = "wsl"
			default:
				return fmt.Errorf("unknown stage %q (want core, base, or wsl)", stage)
			}
			if accel != "" {
				ss := stageSpecs[stage]
				if !validAccel(accel, ss.buildAccel) {
					return fmt.Errorf("accelerator %q is not valid for stage %q (want %s)",
						accel, stage, strings.Join(ss.buildAccel, ", "))
				}
			}

			imgFmt, err := imageformat.ParseFormat(imageTypeFlag)
			if err != nil {
				return err
			}

			dest := "./"
			if len(args) == 1 {
				dest = args[0]
			}
			if info, err := os.Stat(dest); (err == nil && info.IsDir()) || strings.HasSuffix(dest, "/") {
				dest = filepath.Join(dest, imageformat.DefaultOutputName(stage, imgFmt))
			}
			if _, err := os.Stat(dest); err == nil && !force {
				if !confirm(cmd, fmt.Sprintf("%s exists — overwrite? [y/N] ", dest)) {
					return fmt.Errorf("aborted: %s exists", dest)
				}
			}

			if cacheDir == "" {
				cacheDir = cache.Dir()
			}
			ui := newRunUI(cmd)
			err = func() error {
				logger := ui.Logger

				winISO, virtioISO, err := ensureCachedISOs(cmd, cacheDir, spec, noCache, ui)
				if err != nil {
					return err
				}

				workDir, err := os.MkdirTemp("", "winkit-build-*")
				if err != nil {
					return err
				}
				defer os.RemoveAll(workDir)

				// The build always produces a qcow2 first. For non-qcow2
				// formats the qcow2 lands in workDir and is packaged into
				// the final format at dest.
				qcow2Dest := dest
				if imgFmt != imageformat.Qcow2 {
					qcow2Dest = filepath.Join(workDir, "disk.qcow2")
				}

				if stage == "wsl" {
					if err := buildWSLImage(cmd.Context(), qcow2Dest, cacheDir, winISO, virtioISO, workDir, ui, noCache, accel, resolveNixHome(nixHome)); err != nil {
						return err
					}
				} else {
					arch := spec.Arch
					if arch == "" {
						arch = "arm64"
					}
					gosshdExe := filepath.Join(workDir, "gosshd.exe")
					logger.Info("cross-compiling gosshd", "target", "windows/"+arch)
					if err := winpe.CrossCompileGosshd(gosshdExe, arch); err != nil {
						return err
					}

					pwshFiles, err := winpe.FetchPwshFiles(cacheDir, func(f string, a ...any) {
						logger.Info(fmt.Sprintf(f, a...))
					})
					if err != nil {
						return err
					}

					logger.Info("building image", "stage", stage)
					logger.Debug("image sources", "windows", winISO, "virtio", virtioISO)
					baseCfg := winpe.BaseImageConfig{
						WindowsISO: winISO,
						VirtIOISO:  virtioISO,
						GosshdExe:  gosshdExe,
						PwshFiles:  pwshFiles,
						WorkDir:    workDir,
					}

					var files map[string][]byte
					files, err = winpe.BuildBaseImageFiles(baseCfg)
					if err != nil {
						return err
					}

					if err := qemu.CreateFATQcow2(qcow2Dest, files, baseImageCapacity); err != nil {
						return err
					}
				}

				if imgFmt == imageformat.Qcow2 {
					return nil
				}
				logger.Info("packaging image", "format", imgFmt)
				return imageformat.Package(imgFmt, qcow2Dest, dest, nil)
			}()
			ui.Finish(err)
			if err != nil {
				return err
			}
			info, _ := os.Stat(dest)
			sz := float64(0)
			if info != nil {
				sz = float64(info.Size()) / (1024 * 1024)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s (%.0f MB) — boot with: winkit run %s\n", dest, sz, dest)
			return nil
		},
	}
	cmd.Flags().StringVar(&stage, "stage", "base", "image stage: core, base (default), or wsl (wsl.exe ready, nix distro imported; WINKIT_WSL2=true adds the hypervisor stack)")
	cmd.Flags().StringVar(&imageTypeFlag, "image-type", "qcow2", "output image format (qcow2, utm)")
	cmd.Flags().StringVar(&nixHome, "nixhome", "", "home-manager flake for the nix distro: a local directory or a flake ref (github:owner/repo, git+https://…); default: embedded flake, env: WINKIT_NIXHOME")
	cmd.Flags().StringVar(&accel, "accel", "", "QEMU accelerator: hvf, kvm, or tcg (build defaults to best available; wsl continue/verify defaults to tcg)")
	addMediaSpecFlags(cmd, &spec)
	cmd.Flags().BoolVar(&noCache, "no-cache", false, "redownload the cached ISOs before building")
	cmd.Flags().StringVar(&cacheDir, "cache-dir", "", "cache directory (default: "+cache.DirEnv+", else user cache dir + /winkit)")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing image without asking")
	cmd.Flags().BoolVar(&noHypervisor, "no-hypervisor", false,
		"core/base only: transplant VMP but leave the hypervisor disabled "+
			"(no hypervisorlaunchtype, no first-boot hook) — the control image")
	return cmd
}

// ensureCachedISOs returns the cached Windows and virtio-win ISO paths,
// downloading whatever is missing. With noCache the cached copies are
// removed first so the fetchers redownload.
//
// Lane routing: the default (no --build pin) fetches from the MCT catalog,
// which is the only source that produces a complete, self-contained install
// image. A --build pin routes to UUP dump in boot-only mode (the pinned
// build's ESDs cannot produce a complete install.wim, but boot.wim always
// exports clean). If the MCT catalog is unreachable, the default lane falls
// back to UUP dump boot-only with a warning.
func ensureCachedISOs(cmd *cobra.Command, cacheDir string, spec uupdump.MediaSpec, noCache bool, ui *runUI) (winISO, virtioISO string, err error) {
	cfg := cache.Config{Dir: cacheDir}
	virtioISO = cfg.VirtIOISO()

	if noCache {
		clearCache(cacheDir, func(format string, a ...any) {
			ui.Logger.Info(fmt.Sprintf(format, a...))
		})
	}

	winISO, err = fetchWindowsISO(cmd, cacheDir, spec, ui)
	if err != nil {
		return "", "", err
	}

	if _, err := os.Stat(virtioISO); err != nil {
		ui.Logger.Info("virtio-win ISO not cached — fetching")
		virtioISO, err = virtio.FetchISO(cmd.Context(), virtio.FetchConfig{
			CacheDir: cacheDir,
			Logger:   ui.Logger,
		})
		if err != nil {
			return "", "", fmt.Errorf("fetching virtio-win ISO: %w", err)
		}
	}
	return winISO, virtioISO, nil
}

// fetchWindowsISO routes to the appropriate media source based on the spec.
func fetchWindowsISO(cmd *cobra.Command, cacheDir string, spec uupdump.MediaSpec, ui *runUI) (string, error) {
	r, err := spec.Resolve()
	if err != nil {
		return "", fmt.Errorf("resolving media spec: %w", err)
	}

	if r.BuildPinned {
		ui.Logger.Info("build pinned: using uupdump lane (boot-only)",
			"build", spec.Build)
		return fetchViaUUPDump(cmd, cacheDir, spec, ui, true)
	}

	// Default lane: MCT catalog (complete media, GA discovery built in).
	ui.Logger.Info("using mct lane (complete install media)")
	lang := spec.Language
	if lang == "" {
		lang = "en-us"
	}
	edition := spec.Edition
	if edition == "" {
		edition = "Professional"
	}
	isoPath, err := mctcatalog.FetchWindowsISO(cmd.Context(), mctcatalog.FetchConfig{
		CacheDir: cacheDir,
		Language: lang,
		Edition:  edition,
		LogFunc: func(format string, a ...any) {
			ui.Logger.Info(fmt.Sprintf(format, a...))
		},
	})
	if err == nil {
		return isoPath, nil
	}

	ui.Logger.Warn("mct catalog unavailable, falling back to uupdump boot-only",
		"error", err)
	return fetchViaUUPDump(cmd, cacheDir, spec, ui, true)
}

func fetchViaUUPDump(cmd *cobra.Command, cacheDir string, spec uupdump.MediaSpec, ui *runUI, bootOnly bool) (string, error) {
	isoPath, err := uupdump.FetchWindowsISO(cmd.Context(), uupdump.FetchConfig{
		CacheDir:    cacheDir,
		Spec:        spec,
		Logger:      ui.Logger,
		OnFileStart: func(name string) { ui.ItemStart(itemName(name)) },
		OnFileDone:  func(name string, _ int64) { ui.ItemDone(itemName(name)) },
		BootOnly:    bootOnly,
	})
	if err != nil {
		return "", fmt.Errorf("fetching Windows ISO: %w", err)
	}
	return isoPath, nil
}

// clearCache removes every fetch artifact so the next build redownloads
// from scratch: the assembled ISOs AND the intermediate ESD/work trees —
// their .done markers would otherwise short-circuit a --no-cache fetch
// back onto the very files the user is trying to replace.
func clearCache(cacheDir string, logf func(string, ...any)) {
	cfg := cache.Config{Dir: cacheDir}
	logf("--no-cache: clearing %s", cacheDir)
	// Installer ISOs are named per spec (windows-11-24h2-arm64-en-us.iso
	// and friends), so clear them by pattern.
	isos, _ := filepath.Glob(filepath.Join(cfg.Resolve(), "windows-*.iso"))
	for _, p := range append(isos, cfg.WindowsISO(), cfg.VirtIOISO()) {
		os.Remove(p)
	}
	for _, d := range []string{
		"uupdump-download", "uupdump-work",
		"mct-download", "mct-work",
	} {
		os.RemoveAll(filepath.Join(cacheDir, d))
	}
}

// wimlibAvailable is a seam for tests; the binding's answer is fixed at
// build time.
var wimlibAvailable = wimlib.Available

// requireWimlib fails fast when the binary was built without libwim. The
// wimlib-imagex CLI on PATH does not help: the Go binding links the C
// library at build time.
func requireWimlib() error {
	if wimlibAvailable() {
		return nil
	}
	return fmt.Errorf("wimlib support is not compiled into this binary.\n" +
		"Rebuild with:  task build WINKIT_BUILD_TAGS=wimlib\n" +
		"         or:  go install -tags wimlib ./cmd/winkit\n" +
		"Requires libwim headers at build time (brew install wimlib, or\n" +
		"nix profile install nixpkgs#wimlib). The wimlib-imagex CLI alone\n" +
		"is not used — the Go binding links libwim when compiled.")
}

func confirm(cmd *cobra.Command, prompt string) bool {
	fmt.Fprint(cmd.ErrOrStderr(), prompt)
	line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}
