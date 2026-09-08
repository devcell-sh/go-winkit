package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/devcell-sh/go-wimlib"
	"github.com/devcell-sh/go-winkit/build"
	"github.com/devcell-sh/go-winkit/build/buildopts"
	"github.com/devcell-sh/go-winkit/build/imageformat"
	"github.com/devcell-sh/go-winkit/cache"
	"github.com/devcell-sh/go-winkit/internal/config"
	"github.com/devcell-sh/go-winkit/media/mctcatalog"
	"github.com/devcell-sh/go-winkit/media/uupdump"
	"github.com/devcell-sh/go-winkit/media/virtio"
	"github.com/devcell-sh/go-winkit/s6"
	"github.com/devcell-sh/go-winkit/vm/qemu"
	"github.com/devcell-sh/go-winkit/winpe"
)

// qcow2 capacity for the base image: the boot payload is ~700MB and the
// guest writes logs back to the volume.
const baseImageCapacity = 4 * 1024 * 1024 * 1024

// buildAccels lists accelerators valid for the build VM.
var buildAccels = []string{"hvf", "kvm", "tcg"}

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

// resolveStage maps BuildOpts to the internal stage name used by the build
// pipeline. This bridges the new config model to the existing pipeline.
func resolveStage(opts *buildopts.BuildOpts) string {
	if opts.PE {
		return "core"
	}
	if opts.WSL != nil {
		return "wsl"
	}
	return "base"
}

func newBuildCmd() *cobra.Command {
	var (
		configFile    string
		peFlag        bool
		wslFlag       bool
		wslImage      string
		fromFlag      string
		imageTypeFlag string
		accel         string
		vncDisplay    int
		noCache       bool
		cacheDir      string
		force         bool
		vagrantFlag   bool
	)
	cmd := &cobra.Command{
		Use:   "build [dest]",
		Short: "Build a bootable Windows image",
		Long: "Builds a bootable Windows image. Configuration is loaded from\n" +
			"winkit.yaml (or -f path), with CLI flags as overrides.\n\n" +
			"Build modes:\n" +
			"  (default) — full disk install (~64GB qcow2, persistent OS)\n" +
			"  --pe      — WinPE boot volume (~4GB, ephemeral)\n" +
			"  --wsl     — full disk install + WSL distro imported\n\n" +
			"dest may be a directory (default ./) or a .qcow2 path. Requires a\n" +
			"wimlib-enabled build (-tags wimlib).",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireWimlib(); err != nil {
				return err
			}

			ui := newRunUI(cmd)

			// Load config from yaml (optional).
			cfg, cfgPath, err := loadBuildConfig(configFile)
			if err != nil {
				return err
			}
			if cfgPath != "" {
				ui.Logger.Debug("loaded config", "path", cfgPath)
			}

			// Apply CLI flag overrides.
			if cmd.Flags().Changed("from") {
				cfg.From = fromFlag
			}
			if cmd.Flags().Changed("pe") {
				cfg.PE = peFlag
			}
			if cmd.Flags().Changed("wsl") || cmd.Flags().Changed("wsl-image") {
				if wslFlag || wslImage != "" {
					image := wslImage
					if image == "" {
						image = "alpine"
					}
					// Override the image only; a yaml-declared services
					// dir survives the flag.
					services := ""
					if cfg.WSL != nil {
						services = cfg.WSL.Services
					}
					cfg.WSL = &config.WSLConfig{Image: image, Services: services}
				} else {
					cfg.WSL = nil
				}
			}

			if err := cfg.Validate(); err != nil {
				return err
			}

			// Convert to BuildOpts.
			opts, err := cfg.ToBuildOpts()
			if err != nil {
				return err
			}

			stage := resolveStage(opts)

			if accel != "" {
				if !validAccel(accel, buildAccels) {
					return fmt.Errorf("accelerator %q is not valid (want %s)",
						accel, strings.Join(buildAccels, ", "))
				}
			}

			displayType := ""
			if vncDisplay >= 0 {
				displayType = fmt.Sprintf("vnc=:%d", vncDisplay)
				ui.Logger.Info("VNC server enabled", "display", vncDisplay, "port", 5900+vncDisplay)
			}

			imgFmt, err := imageformat.ParseFormat(imageTypeFlag)
			if err != nil {
				return err
			}

			// --vagrant (or a vagrant: block in winkit.yaml) emits a
			// vagrant-qemu Vagrantfile next to the image. The flag wins
			// when set explicitly, so --vagrant=false silences the yaml.
			vagrantOn := cfg.Vagrant != nil
			if cmd.Flags().Changed("vagrant") {
				vagrantOn = vagrantFlag
			}
			if vagrantOn {
				if imgFmt == imageformat.UTM {
					return fmt.Errorf("--vagrant requires --image-type=qcow2 or box (.utm is not vagrant-bootable)")
				}
				if opts.PE {
					return fmt.Errorf("--vagrant is not supported with --pe (WinPE volumes are ephemeral)")
				}
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

			// build.jsonl is the unified structured log: host slog events
			// stream in live, and the guest's raw event stream (QEMU
			// chardev, work-dir guest.jsonl) is appended after the build.
			// With --debug it lands with the other debug artifacts under
			// .winkit/debug/<ts>-build (screenshots etc.); otherwise it
			// goes to the system temp dir so the project directory stays
			// clean — the TUI carries the progress, and the path is
			// printed if the build fails.
			debug, _ := cmd.Flags().GetBool("debug")
			buildStamp := time.Now().UTC().Format("20060102T150405")
			debugDir := filepath.Join(".winkit", "debug", buildStamp+"-build")
			logPath := filepath.Join(os.TempDir(), "winkit-build-"+buildStamp+".jsonl")
			if debug {
				logPath = filepath.Join(debugDir, "build.jsonl")
			}
			if err := ui.AttachLogFile(logPath); err != nil {
				return err
			}
			ui.Logger.Debug("debug artifacts", "dir", debugDir)
			ui.Logger.Debug("structured log", "path", logPath)

			// Resolve media spec from BuildOpts.From for the fetch pipeline.
			spec, err := specFromFrom(opts.From)
			if err != nil {
				return err
			}

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
				// Runs before workDir's RemoveAll (LIFO), on success and
				// failure alike — failed builds need the guest log most.
				defer ui.MergeGuestLog(workDir)

				// The build always produces a qcow2 first. For non-qcow2
				// formats the qcow2 lands in workDir and is packaged into
				// the final format at dest.
				qcow2Dest := dest
				if imgFmt != imageformat.Qcow2 {
					qcow2Dest = filepath.Join(workDir, "disk.qcow2")
				}

				// --debug: periodic VM screenshots into the debug dir, like
				// the e2e harness. The QMP socket path is deterministic; on
				// non-qemu backends it never appears and the capturer idles.
				stopShots := make(chan struct{})
				shotsDone := make(chan struct{})
				if debug && stage != "pe" {
					shotDir := filepath.Join(debugDir, "screenshots")
					if err := os.MkdirAll(shotDir, 0o755); err != nil {
						return err
					}
					qmpSock := qemu.QMPSocketPath(qemu.Spec{
						VMName: "winkit-install", QMPSocketDir: filepath.Join(workDir, "install")})
					go qemu.CaptureScreenshots(qmpSock, shotDir, stopShots, shotsDone, func(f string, a ...any) {
						logger.Debug(fmt.Sprintf(f, a...))
					})
					logger.Info("saving VM screenshots", "dir", shotDir)
				} else {
					close(shotsDone)
				}
				defer func() { close(stopShots); <-shotsDone }()

				buildCfg := build.Config{
					Dest:        qcow2Dest,
					CacheDir:    cacheDir,
					WindowsISO:  winISO,
					VirtIOISO:   virtioISO,
					WorkDir:     workDir,
					Logger:      ui.Logger,
					NoCache:     noCache,
					Accel:       accel,
					DisplayType: displayType,
					NixHome:     build.ResolveNixHome(""),
					Opts:        opts,
				}
				if opts.WSL != nil {
					buildCfg.WSLImage = opts.WSL.Image
					if opts.WSL.ServicesDir != "" {
						svcs, err := s6.LoadDir(opts.WSL.ServicesDir)
						if err != nil {
							return fmt.Errorf("wsl services %s: %w", opts.WSL.ServicesDir, err)
						}
						buildCfg.WSLServices = svcs
					}
				}

				switch stage {
				case "wsl":
					if err := build.WSL(cmd.Context(), buildCfg); err != nil {
						return err
					}
				case "base":
					if err := build.Base(cmd.Context(), buildCfg); err != nil {
						return err
					}
				default:
					// PE mode: build boot volume only (no VM install).
					arch := "arm64"
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

					logger.Info("building PE boot volume", "stage", stage)
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

				// Sanity: a real install writes many GB; anything near the
				// empty-qcow2 floor means the OS never landed even though
				// the build returned cleanly (same assertion as the e2e test).
				if stage != "pe" && os.Getenv("WINKIT_E2E_DISK") == "" {
					const minInstalledBytes = 4 * 1024 * 1024 * 1024
					if fi, err := os.Stat(qcow2Dest); err != nil {
						return fmt.Errorf("produced disk missing: %w", err)
					} else if fi.Size() < minInstalledBytes {
						return fmt.Errorf("produced disk too small (%.1f GB at %s) — install likely did not complete",
							float64(fi.Size())/(1<<30), qcow2Dest)
					}
				}

				if imgFmt == imageformat.Qcow2 {
					return nil
				}
				logger.Info("packaging image", "format", imgFmt)
				var pkgOpts *imageformat.PackageOpts
				if imgFmt == imageformat.Box {
					v := vagrantOptsFor(cfg, "")
					pkgOpts = &imageformat.PackageOpts{Vagrant: &v}
				}
				return imageformat.Package(imgFmt, qcow2Dest, dest, pkgOpts)
			}()
			ui.Finish(err)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "full build log: %s\n", logPath)
				return err
			}
			info, _ := os.Stat(dest)
			sz := float64(0)
			if info != nil {
				sz = float64(info.Size()) / (1024 * 1024)
			}
			if imgFmt == imageformat.Box {
				fmt.Fprintf(cmd.OutOrStdout(), "%s (%.0f MB) -- add with: vagrant box add %s --name winkit/%s\n",
					dest, sz, dest, stage)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "%s (%.0f MB) -- boot with: winkit start %s\n", dest, sz, dest)
			}

			if vagrantOn {
				vfPath := filepath.Join(filepath.Dir(dest), "Vagrantfile")
				if _, err := os.Stat(vfPath); err == nil && !force {
					if !confirm(cmd, fmt.Sprintf("%s exists — overwrite? [y/N] ", vfPath)) {
						return fmt.Errorf("aborted: %s exists", vfPath)
					}
				}
				if imgFmt == imageformat.Box {
					err = imageformat.WriteBoxVagrantfile(vfPath, filepath.Base(dest), "winkit/"+stage)
				} else {
					err = imageformat.WriteVagrantfile(vfPath, vagrantOptsFor(cfg, filepath.Base(dest)))
				}
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s -- boot with: vagrant up\n", vfPath)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&configFile, "file", "f", "", "config file path (default: ./winkit.yaml or ./winkit.yml)")
	cmd.Flags().BoolVar(&peFlag, "pe", false, "WinPE boot volume mode (~4GB, ephemeral)")
	cmd.Flags().BoolVar(&wslFlag, "wsl", false, "enable WSL in the built image")
	cmd.Flags().StringVar(&wslImage, "wsl-image", "", "WSL distro image (alpine, ubuntu, or path to .wsl tarball)")
	cmd.Flags().StringVar(&fromFlag, "from", "", "media source: windows/<edition>-<arch>, ./path.iso, or ./path.wim (default: windows/11-pro-arm64)")
	cmd.Flags().StringVar(&imageTypeFlag, "image-type", "qcow2", "output image format (qcow2, utm, box)")
	cmd.Flags().StringVar(&accel, "accel", "", "QEMU accelerator: hvf, kvm, or tcg")
	cmd.Flags().IntVar(&vncDisplay, "vnc", -1, "start a VNC server on display :N (port 5900+N) to watch the install")
	cmd.Flags().BoolVar(&noCache, "no-cache", false, "redownload the cached ISOs before building")
	cmd.Flags().StringVar(&cacheDir, "cache-dir", "", "cache directory (default: "+cache.DirEnv+", else user cache dir + /winkit)")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing image without asking")
	cmd.Flags().BoolVar(&vagrantFlag, "vagrant", false, "write a vagrant-qemu Vagrantfile next to the built image")
	return cmd
}

// loadBuildConfig loads config from the given path, or discovers winkit.yaml/yml
// in the current directory. Returns defaults if no config file exists.
// The second return value is the resolved path (empty when using defaults).
func loadBuildConfig(path string) (*config.Config, string, error) {
	if path != "" {
		cfg, err := config.Load(path)
		if err != nil {
			return nil, "", err
		}
		return cfg, path, nil
	}

	discovered, err := config.Discover(".")
	if err != nil {
		return &config.Config{}, "", nil
	}
	cfg, err := config.Load(discovered)
	if err != nil {
		return nil, "", err
	}
	return cfg, discovered, nil
}

// specFromFrom converts a BuildOpts.From string to a uupdump.MediaSpec for
// the fetch pipeline. Local ISO/WIM paths are not supported by the fetch
// pipeline yet; this returns an error for them.
func specFromFrom(from string) (uupdump.MediaSpec, error) {
	kind := buildopts.DetectFrom(from)
	switch kind {
	case buildopts.FromISO, buildopts.FromWIM:
		return uupdump.MediaSpec{}, fmt.Errorf("local media sources (%s) are not yet supported in the build pipeline", from)
	}
	// Parse "windows/11-pro-arm64" format.
	spec := uupdump.MediaSpec{
		OS:   "windows",
		Arch: "arm64",
	}
	parts := strings.SplitN(from, "/", 2)
	if len(parts) == 2 {
		spec.OS = parts[0]
		// The descriptor after / is informational for now; the fetch
		// pipeline uses its own defaults. Future: parse edition/arch.
	}
	return spec, nil
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
