// Package winkit builds Windows VM images from code. This root package
// is the library entry point consumers embed (devcell, CI pipelines):
// fill a build.Config, call Build, get a disk image. The winkit CLI is a
// thin frontend over the same call.
//
// The contract: winkit owns the verbs (fetch, service, install, import,
// execute hooks, package); the consumer owns the nouns (which WSL image,
// which services, which packages) via buildopts.BuildOpts and hooks. See
// the examples/ directory for both YAML and library consumers.
package winkit

import (
	"context"
	"fmt"
	"os"

	"github.com/devcell-sh/go-winkit/build"
	"github.com/devcell-sh/go-winkit/build/buildopts"
	"github.com/devcell-sh/go-winkit/s6"
)

// Build produces a Windows image from cfg, dispatching on cfg.Opts: PE
// mode assembles a WinPE boot volume, a WSL config runs a full install
// with the distro imported, otherwise a base install runs. cfg.Opts may
// be nil (defaults apply). Dest, WindowsISO and VirtIOISO are required;
// an empty WorkDir gets a temp dir that is removed when Build returns.
func Build(ctx context.Context, cfg build.Config) error {
	if cfg.Opts == nil {
		cfg.Opts = &buildopts.BuildOpts{}
	}
	cfg.Opts.ApplyDefaults()
	if err := cfg.Opts.Validate(); err != nil {
		return err
	}

	switch {
	case cfg.Dest == "":
		return fmt.Errorf("build.Config.Dest is required")
	case cfg.WindowsISO == "":
		return fmt.Errorf("build.Config.WindowsISO is required")
	case cfg.VirtIOISO == "":
		return fmt.Errorf("build.Config.VirtIOISO is required")
	}

	if cfg.WorkDir == "" {
		workDir, err := os.MkdirTemp("", "winkit-build-*")
		if err != nil {
			return err
		}
		defer os.RemoveAll(workDir)
		cfg.WorkDir = workDir
	} else if err := os.MkdirAll(cfg.WorkDir, 0o755); err != nil {
		return fmt.Errorf("creating work dir: %w", err)
	}

	switch cfg.Opts.Stage() {
	case buildopts.StagePE:
		return build.PE(ctx, cfg)
	case buildopts.StageWSL:
		if cfg.WSLImage == "" {
			cfg.WSLImage = cfg.Opts.WSL.Image
		}
		if cfg.NixHome == "" {
			cfg.NixHome = cfg.Opts.WSL.NixHome
		}
		if dir := cfg.Opts.WSL.ServicesDir; dir != "" && cfg.WSLServices == nil {
			svcs, err := s6.LoadDir(dir)
			if err != nil {
				return fmt.Errorf("wsl services %s: %w", dir, err)
			}
			cfg.WSLServices = svcs
		}
		return build.WSL(ctx, cfg)
	default:
		return build.Base(ctx, cfg)
	}
}
