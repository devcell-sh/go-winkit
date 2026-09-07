package qemu

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func testSpec() Spec {
	return Spec{
		VMName:       "test-vm",
		CPUs:         2,
		MemoryGB:     4,
		DiskPath:     "/tmp/scratch.qcow2",
		FirmwarePath: "/tmp/firmware.fd",
		VarsPath:     "/tmp/vars.fd",
		DisplayType:  "none",
		QMPSocketDir: "/tmp",
		NoReboot:     true,
	}
}

func TestBuildWimBuilderArgv_VirtIOISO(t *testing.T) {
	wbs := WimBuilderSpec{
		Spec:       testSpec(),
		WinPEISO:   "/tmp/winpe.iso",
		SharedImg:  "/tmp/shared.qcow2",
		WindowsISO: "/tmp/windows.iso",
		VirtIOISO:  "/tmp/virtio-win.iso",
	}
	argv := BuildWimBuilderArgv(wbs)
	joined := strings.Join(argv, " ")

	assert.Contains(t, joined, "file=/tmp/virtio-win.iso,media=cdrom,if=none,id=cdrom2")
	assert.Contains(t, joined, "usb-storage,drive=cdrom2")
}

func TestBuildWimBuilderArgv_NoVirtIOISO(t *testing.T) {
	wbs := WimBuilderSpec{
		Spec:       testSpec(),
		WinPEISO:   "/tmp/winpe.iso",
		SharedImg:  "/tmp/shared.qcow2",
		WindowsISO: "/tmp/windows.iso",
	}
	argv := BuildWimBuilderArgv(wbs)
	joined := strings.Join(argv, " ")

	assert.NotContains(t, joined, "cdrom2")
}

func TestBuildWimBuilderArgv_SCSI(t *testing.T) {
	s := testSpec()
	s.CDBus = "scsi"
	wbs := WimBuilderSpec{
		Spec:       s,
		WinPEISO:   "/tmp/winpe.iso",
		SharedImg:  "/tmp/shared.qcow2",
		WindowsISO: "/tmp/windows.iso",
		VirtIOISO:  "/tmp/virtio-win.iso",
	}
	argv := BuildWimBuilderArgv(wbs)
	joined := strings.Join(argv, " ")

	assert.Contains(t, joined, "virtio-scsi-pci,id="+CDBusID)
	// FAT volume boots first: its BOOTAA64.EFI (Windows bootmgr) reads the
	// CD; executing the CD's own El Torito image crashes Linux-hosted EDK2.
	assert.Contains(t, joined, "scsi-cd,drive=cdrom0,bus="+CDBusID+".0,id="+InstallerCDDevID+",bootindex=2")
	assert.Contains(t, joined, "scsi-cd,drive=cdrom1")
	assert.Contains(t, joined, "scsi-cd,drive=cdrom2")
	assert.Contains(t, joined, "usb-storage,drive=usbfat0,removable=true,bus="+USBBusID+".0,bootindex=1")
	assert.NotContains(t, joined, "usb-storage,drive=cdrom")
}

func TestBuildWimBuilderArgv_BaseCommand(t *testing.T) {
	wbs := WimBuilderSpec{
		Spec:     testSpec(),
		WinPEISO: "/tmp/winpe.iso",
	}
	argv := BuildWimBuilderArgv(wbs)
	joined := strings.Join(argv, " ")

	assert.Contains(t, joined, "qemu-system-aarch64")
	assert.Contains(t, joined, "-smp 2")
	assert.Contains(t, joined, "-m 4G")
	assert.Contains(t, joined, "-display none")
	assert.Contains(t, joined, "-no-reboot")
}

func TestBaseCommand_WiresStructuredPort(t *testing.T) {
	s := testSpec()
	s.GuestProgressLogPath = "/tmp/build.log"
	s.GuestStructuredLogPath = "/tmp/build.jsonl"
	wbs := WimBuilderSpec{
		Spec:     s,
		WinPEISO: "/tmp/winpe.iso",
	}
	argv := BuildWimBuilderArgv(wbs)
	joined := strings.Join(argv, " ")

	assert.Contains(t, joined, "winkit.structured.0",
		"structured port must be wired when GuestStructuredLogPath is set")
	assert.Contains(t, joined, "path=/tmp/build.jsonl",
		"structured chardev must point to the configured path")
}

func TestBaseCommand_NoStructuredPortWithoutPath(t *testing.T) {
	s := testSpec()
	s.GuestProgressLogPath = "/tmp/build.log"
	wbs := WimBuilderSpec{
		Spec:     s,
		WinPEISO: "/tmp/winpe.iso",
	}
	argv := BuildWimBuilderArgv(wbs)
	joined := strings.Join(argv, " ")

	assert.NotContains(t, joined, "winkit.structured.0",
		"structured port must NOT be wired when GuestStructuredLogPath is empty")
}

func TestApplyDefaults_CDBusSCSI(t *testing.T) {
	s := Spec{}
	s.ApplyDefaults()
	assert.Equal(t, "scsi", s.CDBus,
		"CDBus must default to scsi — USB-attached CD boot crashes cdboot on aarch64 (CELL-429)")
}

func TestBuildWimBuilderArgv_DefaultUsesSCSI(t *testing.T) {
	s := testSpec()
	s.CDBus = ""
	s.ApplyDefaults()
	wbs := WimBuilderSpec{
		Spec:       s,
		WinPEISO:   "/tmp/winpe.iso",
		SharedImg:  "/tmp/shared.qcow2",
		WindowsISO: "/tmp/windows.iso",
	}
	argv := BuildWimBuilderArgv(wbs)
	joined := strings.Join(argv, " ")

	assert.Contains(t, joined, "virtio-scsi-pci",
		"default CDBus must produce SCSI wiring")
	assert.Contains(t, joined, "scsi-cd,drive=cdrom0",
		"WinPE ISO must be on scsi-cd by default")
	assert.NotContains(t, joined, "usb-storage,drive=cdrom0",
		"WinPE ISO must NOT be on usb-storage by default")
}

func TestQMPSocketPath_Short(t *testing.T) {
	spec := Spec{VMName: "test", QMPSocketDir: "/tmp"}
	path := QMPSocketPath(spec)
	assert.Equal(t, "/tmp/qemu-test-qmp.sock", path)
}

func TestBaseCommand_SMBIOSSerial(t *testing.T) {
	spec := testSpec()
	spec.SMBIOSSerial = "my-hostname"
	argv := strings.Join(baseCommand(spec), " ")
	assert.Contains(t, argv, "-smbios type=1,serial=my-hostname")

	spec2 := testSpec()
	argv2 := strings.Join(baseCommand(spec2), " ")
	assert.NotContains(t, argv2, "-smbios")
}

func TestQMPSocketPath_FallsBackWhenTooLong(t *testing.T) {
	spec := Spec{
		VMName:       "test",
		QMPSocketDir: "/very/long/path/that/is/way/too/long/for/unix/socket/and/will/overflow/the/sun/path/limit",
	}
	path := QMPSocketPath(spec)
	assert.Less(t, len(path), maxUnixSocketPath)
}
