//go:build !darwin

package vz

import (
	"context"
	"fmt"
	"runtime"

	"github.com/devcell-sh/go-winkit/winpe"
)

// Backend implements winpe.VMBackend using Apple's Virtualization.framework.
// This stub exists so the package compiles on non-darwin platforms; all methods
// return an error.
type Backend struct{}

func (*Backend) DefaultDiskFormat() winpe.DiskFormat { return winpe.DiskFormatRaw }

func (*Backend) CreateDisk(path string, sizeGB int, format winpe.DiskFormat) error {
	return fmt.Errorf("vz backend requires macOS (current: %s)", runtime.GOOS)
}

func (*Backend) CreateOverlay(path, base string) error {
	return fmt.Errorf("vz backend requires macOS (current: %s)", runtime.GOOS)
}

func (*Backend) Flatten(src, dst string) error {
	return fmt.Errorf("vz backend requires macOS (current: %s)", runtime.GOOS)
}

func (*Backend) StartInstall(_ context.Context, _ winpe.VMInstallConfig) (winpe.VM, error) {
	return nil, fmt.Errorf("vz backend requires macOS (current: %s)", runtime.GOOS)
}

func (*Backend) StartRun(_ context.Context, _ winpe.VMRunConfig) (winpe.VM, error) {
	return nil, fmt.Errorf("vz backend requires macOS (current: %s)", runtime.GOOS)
}

// VNCPortFromVM returns 0 on non-darwin (stub).
func VNCPortFromVM(_ winpe.VM) uint16 { return 0 }

// ScreenshotFromVM returns an error on non-darwin (stub).
func ScreenshotFromVM(_ winpe.VM) ([]byte, error) {
	return nil, fmt.Errorf("vz screenshots require macOS")
}
