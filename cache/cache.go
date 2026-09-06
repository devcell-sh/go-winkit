// Package cache resolves where winkit stores large downloaded artifacts:
// installer ISOs, driver ISOs and the ESD files they are assembled from.
//
// One resolver is shared by the CLI and the tests so that `winkit fetch`
// seeds exactly the paths the integration tests later read. When they
// disagree, fetch appears to succeed while every boot test skips.
package cache

import (
	"os"
	"path/filepath"
)

// DirEnv overrides the cache location. Set it to keep artifacts on a
// volume that survives container rebuilds, or to share one download
// between checkouts.
const DirEnv = "WINKIT_CACHE_DIR"

const (
	// WindowsISOName is the assembled Windows installer, named by
	// language so several can coexist.
	WindowsISOName = "windows-arm64-en-us.iso"

	// VirtIOISOName is the virtio-win driver ISO.
	VirtIOISOName = "virtio-win.iso"
)

// Config lets a caller embedding winkit as a library choose the cache
// location explicitly, instead of inheriting the process environment.
//
// An embedding application usually has its own idea of where large
// artifacts belong and cannot set environment variables on behalf of the
// process, so Dir is the authoritative setting when non-empty and the
// shared default applies otherwise.
type Config struct {
	// Dir is the cache directory. Empty means use the shared default.
	Dir string
}

// Resolve returns the cache directory this config selects.
func (c Config) Resolve() string {
	if c.Dir != "" {
		return c.Dir
	}
	return Dir()
}

// WindowsISO returns the Windows installer path within this config's cache.
func (c Config) WindowsISO() string {
	return filepath.Join(c.Resolve(), WindowsISOName)
}

// VirtIOISO returns the virtio-win driver ISO path within this config's cache.
func (c Config) VirtIOISO() string {
	return filepath.Join(c.Resolve(), VirtIOISOName)
}

// Dir returns the process-wide cache directory: the DirEnv override when
// set, else $XDG_CACHE_HOME/winkit, else ~/.cache/winkit — on every
// platform, macOS included. ~/Library/Caches is the Go convention there,
// but a cross-platform CLI whose docs and scripts say ~/.cache/winkit
// should mean the same path everywhere. It does not create the directory.
// Library callers wanting an explicit location should use Config instead.
func Dir() string {
	if d := os.Getenv(DirEnv); d != "" {
		return d
	}
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return filepath.Join(xdg, "winkit")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "winkit")
	}
	return filepath.Join(home, ".cache", "winkit")
}

// WindowsISO returns the path the assembled Windows installer is cached at.
func WindowsISO() string { return filepath.Join(Dir(), WindowsISOName) }

// VirtIOISO returns the path the virtio-win driver ISO is cached at.
func VirtIOISO() string { return filepath.Join(Dir(), VirtIOISOName) }
