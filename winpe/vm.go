package winpe

import "context"

// DiskFormat selects the virtual disk image format.
type DiskFormat int

const (
	// DiskFormatDefault lets the backend choose (qcow2 for QEMU, raw for vz).
	DiskFormatDefault DiskFormat = iota
	DiskFormatQcow2
	DiskFormatRaw
)

func (f DiskFormat) String() string {
	switch f {
	case DiskFormatQcow2:
		return "qcow2"
	case DiskFormatRaw:
		return "raw"
	default:
		return "default"
	}
}

// VMBackend abstracts the VM lifecycle so callers are backend-agnostic.
// Implementations live in winpe/qemu (QEMU) and winpe/vz (macOS
// Virtualization.framework). QMP, screendump, stall detection, and send-key
// are QEMU-specific and intentionally excluded.
type VMBackend interface {
	StartInstall(ctx context.Context, cfg VMInstallConfig) (VM, error)
	StartRun(ctx context.Context, cfg VMRunConfig) (VM, error)
	CreateDisk(path string, sizeGB int, format DiskFormat) error

	// CreateOverlay creates a copy-on-write layer over base at path.
	// QEMU: qcow2 backing file. vz/raw: full copy of base (APFS sparse).
	CreateOverlay(path, base string) error

	// Flatten merges an overlay into a standalone image at dst.
	// QEMU: qemu-img convert. vz/raw: no-op (already standalone).
	Flatten(src, dst string) error

	// DefaultDiskFormat returns the backend's preferred disk format.
	DefaultDiskFormat() DiskFormat
}

// VM is a handle to a running virtual machine.
type VM interface {
	Stop() error
	Wait() error
	SSHAddr() string
	OutputDir() string
	// Done returns a channel that is closed when the VM stops or enters an
	// error state. Callers can select on this to detect unexpected exits.
	Done() <-chan struct{}
}

// VMInstallConfig is the backend-agnostic configuration for a fresh Windows
// install VM. Backend-specific options (QEMU's BootVolume, Secure, CDBus, etc.)
// are passed via BackendExtra: each backend type-asserts to its own options type.
type VMInstallConfig struct {
	WindowsISO   string
	VirtIOISO    string
	AnswerVolume string

	DiskPath   string
	DiskSizeGB int
	DiskFormat DiskFormat

	CPUs     uint
	MemoryGB uint64
	OutputDir string

	SSHPort         uint16
	SSHGuestPort    uint16
	OpenSSHHostPort uint16
	RDPPort         uint16

	Accel string

	// BackendExtra holds backend-specific configuration. Each backend
	// type-asserts to its own options type and ignores unrecognized values.
	BackendExtra any
}

// VMRunConfig is the backend-agnostic configuration for booting an already-
// installed disk (continue/finalize mode).
type VMRunConfig struct {
	DiskPath   string
	DiskFormat DiskFormat

	OutputDir string
	VMName    string
	VirtIOISO string

	CPUs     uint
	MemoryGB uint64

	SSHPort         uint16
	SSHGuestPort    uint16
	OpenSSHHostPort uint16
	RDPPort         uint16
	SSHHost         string

	SharedDir string
	Accel     string
	// SMBIOSSerial, when non-empty, passes the value as the SMBIOS serial
	// number so the guest's boot-time hostname task can read and apply it.
	SMBIOSSerial string

	// BackendExtra holds backend-specific configuration.
	BackendExtra any
}
