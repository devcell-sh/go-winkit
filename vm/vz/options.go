package vz

import "log/slog"

// DefaultVsockPort is the guest vsock port gosshd listens on for vz backend
// SSH. The host-side proxy connects to this port via VirtioSocketDevice.
const DefaultVsockPort uint32 = 2222

// DefaultVNCPort is the default TCP port for the headless VNC server.
const DefaultVNCPort uint16 = 25900

// SharedDirTag is the virtio-fs tag the guest uses to mount the shared
// directory: net use X: \\virtiofs\winkit
const SharedDirTag = "winkit"

// InstallOptions holds vz-specific options for StartInstall.
type InstallOptions struct {
	EFIVarsPath  string
	VsockSSHPort uint32
	VNCPort      uint16
	Logger       *slog.Logger
	// BootVolume is a raw FAT image containing the Windows Setup EFI boot
	// chain (BOOTAA64.EFI, BCD, boot.wim). Attached as USB mass storage so
	// the EFI firmware can boot from it. Required for fresh installs.
	BootVolume string
}

// RunOptions holds vz-specific options for StartRun.
type RunOptions struct {
	EFIVarsPath  string
	VsockSSHPort uint32
	VNCPort      uint16
	Logger       *slog.Logger
}
