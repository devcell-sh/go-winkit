// Package build orchestrates full unattended Windows image builds: media
// prep, answer volume, VM install, provisioning over gosshd, verification,
// and clean shutdown. It is the library entry point (the winkit CLI is a
// thin caller): pass a Config and a *slog.Logger and drive it from any Go
// program.
package build

import (
	"context"
	"io"
	"log/slog"
	"os"

	"github.com/devcell-sh/go-wimlib"
	"github.com/devcell-sh/go-winkit/buildopts"
)

// wimlibAvailable is a seam for tests; the binding's answer is fixed at
// compile time (-tags wimlib).
var wimlibAvailable = wimlib.Available

// Config drives an image build. Dest, CacheDir, WindowsISO, VirtIOISO and
// WorkDir are required; zero-valued options get the same defaults the CLI
// uses.
type Config struct {
	// Dest is the produced disk image path (qcow2 for the qemu backend).
	Dest string
	// CacheDir holds downloaded media and built rootfs tarballs.
	CacheDir string
	// WindowsISO / VirtIOISO are the cached installer media paths.
	WindowsISO string
	VirtIOISO  string
	// WorkDir holds build intermediates (boot volumes, VM state).
	WorkDir string
	// Logger receives build progress; nil discards it.
	Logger *slog.Logger
	// NoCache forces rebuilds of cacheable intermediates.
	NoCache bool
	// Accel overrides the QEMU accelerator (default: best for the host).
	Accel string
	// DisplayType is the QEMU display option ("" = headless).
	DisplayType string

	// WSLImage selects the imported distro for WSL builds: "" or a docker
	// ref (alpine default), "nix", a published-image URL, or a local
	// tarball path. See the wsl package.
	WSLImage string
	// NixHome selects the home-manager config for WSLImage=nix.
	NixHome string

	// Opts carries features/hooks for base installs (see buildopts).
	Opts *buildopts.BuildOpts
}

func (c Config) logger() *slog.Logger {
	if c.Logger != nil {
		return c.Logger
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// WSL performs a full unattended install with WSL enabled and a distro
// imported, verifying SSH/RDP, the imported distro, and the SFTP share.
func WSL(ctx context.Context, c Config) error {
	return wslImage(ctx, c.Dest, c.CacheDir, c.WindowsISO, c.VirtIOISO, c.WorkDir,
		c.logger(), c.NoCache, c.Accel, c.WSLImage, c.NixHome, c.DisplayType)
}

// Base performs a full unattended install running the hooks and features
// from c.Opts after the OS boots.
func Base(ctx context.Context, c Config) error {
	return baseInstallImage(ctx, c.Dest, c.CacheDir, c.WindowsISO, c.VirtIOISO, c.WorkDir,
		c.Opts, c.logger(), c.NoCache, c.Accel, c.DisplayType)
}

// copyFile copies src to dst (0644).
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}
