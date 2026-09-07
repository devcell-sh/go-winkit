//go:build !darwin

package vz

import (
	"context"
	"fmt"
	"github.com/devcell-sh/go-winkit/vm"
	"runtime"
)

// Backend implements vm.VMBackend using Apple's Virtualization.framework.
// This stub exists so the package compiles on non-darwin platforms; all methods
// return an error.
type Backend struct{}

func (*Backend) DefaultDiskFormat() vm.DiskFormat { return vm.DiskFormatRaw }

func (*Backend) CreateDisk(path string, sizeGB int, format vm.DiskFormat) error {
	return fmt.Errorf("vz backend requires macOS (current: %s)", runtime.GOOS)
}

func (*Backend) CreateOverlay(path, base string) error {
	return fmt.Errorf("vz backend requires macOS (current: %s)", runtime.GOOS)
}

func (*Backend) Flatten(src, dst string) error {
	return fmt.Errorf("vz backend requires macOS (current: %s)", runtime.GOOS)
}

func (*Backend) StartInstall(_ context.Context, _ vm.VMInstallConfig) (vm.VM, error) {
	return nil, fmt.Errorf("vz backend requires macOS (current: %s)", runtime.GOOS)
}

func (*Backend) StartRun(_ context.Context, _ vm.VMRunConfig) (vm.VM, error) {
	return nil, fmt.Errorf("vz backend requires macOS (current: %s)", runtime.GOOS)
}

// VNCPortFromVM returns 0 on non-darwin (stub).
func VNCPortFromVM(_ vm.VM) uint16 { return 0 }

// ScreenshotFromVM returns an error on non-darwin (stub).
func ScreenshotFromVM(_ vm.VM) ([]byte, error) {
	return nil, fmt.Errorf("vz screenshots require macOS")
}
