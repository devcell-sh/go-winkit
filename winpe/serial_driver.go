//go:build cgo

package winpe

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/devcell-sh/go-wimlib"
)

// serialDriverPaths lists the inbox serial port driver files to extract from
// install.wim and inject into boot.wim.
var serialDriverPaths = []string{
	`\Windows\System32\drivers\serial.sys`,
	`\Windows\System32\drivers\serenum.sys`,
}

// TransferSerialDriver extracts the inbox serial port driver from
// install.wim (image 1) and injects it into boot.wim (image 2). WinPE
// does not carry serial.sys, so QEMU's pci-serial device has no driver
// and COM ports are never enumerated.
func TransferSerialDriver(installWimPath, bootWimPath string) error {
	if !wimlib.Available() {
		return fmt.Errorf("wimlib not available: build with CGO_ENABLED=1 and install libwim")
	}

	tmpDir, err := os.MkdirTemp("", "serial-extract-*")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	src, err := wimlib.OpenWIM(installWimPath)
	if err != nil {
		return fmt.Errorf("opening install.wim: %w", err)
	}
	defer src.Close()

	for _, wp := range serialDriverPaths {
		if err := src.ExtractPaths(1, tmpDir, []string{wp}); err != nil {
			continue
		}
	}
	src.Close()

	if err := materializeDCSFiles(tmpDir); err != nil {
		return fmt.Errorf("materializing serial driver payloads: %w", err)
	}

	serialPath := filepath.Join(tmpDir, "Windows", "System32", "drivers", "serial.sys")
	if _, err := os.Stat(serialPath); err != nil {
		return fmt.Errorf("serial.sys not found in install.wim: %w", err)
	}

	dst, err := wimlib.OpenWIM(bootWimPath)
	if err != nil {
		return fmt.Errorf("opening boot.wim: %w", err)
	}
	defer dst.Close()

	windowsTree := filepath.Join(tmpDir, "Windows")
	if err := dst.UpdateImageAddTree(2, windowsTree, `\Windows`); err != nil {
		return fmt.Errorf("injecting serial driver into boot.wim: %w", err)
	}

	if err := dst.Overwrite(); err != nil {
		return fmt.Errorf("overwriting boot.wim: %w", err)
	}
	return nil
}
