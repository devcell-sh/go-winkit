package qemu

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Spec holds the QEMU VM configuration for a WinPE boot.
type Spec struct {
	VMName       string
	CPUs         uint
	MemoryGB     uint64
	DiskPath     string
	FirmwarePath string
	VarsPath     string
	DisplayType  string
	QMPSocketDir string
	Accel        string
	NoReboot     bool
	MachineType  string
	// CPU overrides the -cpu model. Empty lets cpuType() pick the
	// per-accelerator default. The proven EL3/secure machine needs
	// "neoverse-n1" (with -cpu max Windows never writes a byte once EL3 is
	// present).
	CPU string
	// FirmwareKernel loads FirmwarePath via -kernel instead of pflash. Under
	// secure=on this is what lets a stock normal-world EDK2 work: QEMU's ARM
	// boot stub takes the EL3 entry and drops the payload to non-secure.
	// Firmware-in-pflash has no such stub and hangs at EL3. Costs the vars store.
	FirmwareKernel bool
	CDBus          string

	SerialLogPath          string
	GuestProgressLogPath   string
	GuestStructuredLogPath string

	VirtIOISO string
	SSHPort   uint16
	// SSHGuestPort is the guest port SSHPort forwards to. Zero means 22 (the
	// standard sshd), which is what WinPE's gosshd and a stock OpenSSH listen
	// on. The wsl2 stack sets 2222: there gosshd runs as a dedicated
	// provisioning server on a non-standard port so it coexists with the
	// Windows OpenSSH the delivered image ships on :22.
	SSHGuestPort uint16
	// RDPPort, when non-zero, forwards host:RDPPort → guest:3389 alongside the
	// SSH forward. Used by the wsl2 full-install stack to reach the installed
	// OS's Remote Desktop (the answer file enables RDP in specialize).
	RDPPort uint16
	// OpenSSHHostPort, when non-zero, forwards host:OpenSSHHostPort → guest:22
	// in addition to the gosshd forward. The wsl2 stack uses it to verify the
	// Windows OpenSSH the image ships on :22, separately from the gosshd
	// provisioning channel on SSHGuestPort.
	OpenSSHHostPort uint16
	SSHHost       string
	LogVolumePath string
	DiskCacheMode string
	// SMBIOSSerial, when non-empty, passes -smbios type=1,serial=<value>
	// to QEMU. The guest reads it via WMI (Win32_BIOS.SerialNumber) and
	// can use it as a dynamic hostname source.
	SMBIOSSerial string
}

const maxUnixSocketPath = 104

// QMPSocketPath returns the QMP socket path, falling back to a short
// temp-dir path when the natural path exceeds the unix socket limit.
func QMPSocketPath(spec Spec) string {
	dir := spec.QMPSocketDir
	if dir == "" {
		dir = "/tmp"
	}
	natural := filepath.Join(dir, "qemu-"+spec.VMName+"-qmp.sock")
	if len(natural) < maxUnixSocketPath {
		return natural
	}
	sum := sha256.Sum256([]byte(natural))
	return filepath.Join(os.TempDir(), fmt.Sprintf("winkit-qmp-%x.sock", sum[:8]))
}

// ApplyDefaults fills zero-value fields with sensible defaults.
func (s *Spec) ApplyDefaults() {
	if s.CPUs == 0 {
		s.CPUs = 4
	}
	if s.MemoryGB == 0 {
		s.MemoryGB = 4
	}
	if s.DisplayType == "" {
		s.DisplayType = "none"
	}
	if s.Accel == "" {
		s.Accel = defaultAccel()
	}
	if s.CDBus == "" {
		s.CDBus = "scsi"
	}
}

func defaultAccel() string {
	if runtime.GOOS == "darwin" {
		return "hvf"
	}
	if probeKVM() == nil {
		return "kvm"
	}
	return "tcg,thread=multi"
}

// DefaultAccel returns the best available accelerator for the current host:
// HVF on macOS, KVM on Linux (if /dev/kvm is accessible), TCG otherwise.
func DefaultAccel() string { return defaultAccel() }

func probeKVM() error {
	f, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
	if err != nil {
		return err
	}
	return f.Close()
}

func (s *Spec) effectiveAccel() string {
	if s.Accel != "" {
		return s.Accel
	}
	return defaultAccel()
}

// Validate returns an error if required fields are missing.
func (s *Spec) Validate() error {
	if s.DiskPath == "" {
		return fmt.Errorf("DiskPath is required")
	}
	if s.FirmwarePath == "" {
		return fmt.Errorf("FirmwarePath is required")
	}
	return nil
}
