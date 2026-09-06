//go:build darwin_vz

package cli

// vz support is opt-in behind the darwin_vz build tag (hyphens are not legal
// in Go build tags, hence the underscore). The vz backend cannot boot
// Windows guests (no ACPI/vTPM in Apple's firmware; CELL-523), needs the
// com.apple.security.virtualization entitlement (a codesigned binary), and
// drags in the tmc/apple bindings — none of which a normal winkit build
// wants. Build with `-tags darwin_vz` to compile it in.

import (
	"log/slog"

	"github.com/devcell-sh/go-winkit/winpe"
	"github.com/devcell-sh/go-winkit/winpe/vz"
)

func vzRegistryBackend() (winpe.VMBackend, bool) { return &vz.Backend{}, true }

func vzDefaultVNCPort() uint16 { return vz.DefaultVNCPort }

func vzGosshdVsockPort() uint32 { return vz.DefaultVsockPort }

func vzInstallExtra(vncPort uint16, logger *slog.Logger, bootVolume string) any {
	return &vz.InstallOptions{
		VsockSSHPort: vz.DefaultVsockPort,
		VNCPort:      vncPort,
		Logger:       logger,
		BootVolume:   bootVolume,
	}
}

func vzRunExtra(logger *slog.Logger) any {
	return &vz.RunOptions{
		VsockSSHPort: vz.DefaultVsockPort,
		Logger:       logger,
	}
}

func vzVNCPortFromVM(vm winpe.VM) uint16 { return vz.VNCPortFromVM(vm) }
