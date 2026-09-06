//go:build !darwin_vz

package cli

// Stubs for builds without the darwin_vz tag: the vz backend is not
// compiled in, "vz" is absent from the backend registry, and every
// vz-specific code path is inert. See vzsupport_enabled.go for why.

import (
	"log/slog"

	"github.com/devcell-sh/go-winkit/winpe"
)

func vzRegistryBackend() (winpe.VMBackend, bool) { return nil, false }

func vzDefaultVNCPort() uint16 { return 0 }

func vzGosshdVsockPort() uint32 { return 0 }

func vzInstallExtra(uint16, *slog.Logger, string) any { return nil }

func vzRunExtra(*slog.Logger) any { return nil }

func vzVNCPortFromVM(winpe.VM) uint16 { return 0 }
