package qemu

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildInstallCommand_AttachesWindowsISO(t *testing.T) {
	spec := testSpec()
	argv := BuildInstallCommand(spec, "/tmp/windows.iso", "/tmp/autounattend.img", "")
	joined := strings.Join(argv, " ")

	assert.Contains(t, joined, "file=/tmp/windows.iso,media=cdrom,if=none,id=cdrom0")
	assert.Contains(t, joined, "usb-storage,drive=cdrom0,removable=true,bus="+USBBusID+".0,id="+InstallerCDDevID)
}

func TestBuildInstallCommand_AttachesVirtIOISO(t *testing.T) {
	spec := testSpec()
	spec.VirtIOISO = "/tmp/virtio-win.iso"
	argv := BuildInstallCommand(spec, "/tmp/windows.iso", "/tmp/autounattend.img", "")
	joined := strings.Join(argv, " ")

	assert.Contains(t, joined, "file=/tmp/virtio-win.iso,media=cdrom,if=none,id=cdrom1")
	assert.Contains(t, joined, "usb-storage,drive=cdrom1,removable=true,bus="+USBBusID+".0")
}

func TestBuildInstallCommand_AttachesAutounattendFAT(t *testing.T) {
	spec := testSpec()
	argv := BuildInstallCommand(spec, "/tmp/windows.iso", "/tmp/autounattend.img", "")
	joined := strings.Join(argv, " ")

	assert.Contains(t, joined, "file=/tmp/autounattend.img,format=raw,if=none,id=usbfat0")
	assert.Contains(t, joined, "usb-storage,drive=usbfat0,removable=true,bus="+USBBusID+".0")
}

func TestBuildInstallCommand_AttachesAutounattendISO(t *testing.T) {
	spec := testSpec()
	argv := BuildInstallCommand(spec, "/tmp/windows.iso", "/tmp/autounattend.iso", "")
	joined := strings.Join(argv, " ")

	assert.Contains(t, joined, "file=/tmp/autounattend.iso,media=cdrom,if=none,id=cdrom")
}

func TestBuildInstallCommand_AttachesDevcellWim(t *testing.T) {
	spec := testSpec()
	argv := BuildInstallCommand(spec, "/tmp/windows.iso", "/tmp/autounattend.img", "/tmp/devcell.qcow2")
	joined := strings.Join(argv, " ")

	assert.Contains(t, joined, "file=/tmp/devcell.qcow2,format=qcow2,if=none,id=devcellwim0")
	assert.Contains(t, joined, "usb-storage,drive=devcellwim0,removable=true,bus="+USBBusID+".0")
}

func TestBuildInstallCommand_DevcellWimRaw(t *testing.T) {
	spec := testSpec()
	argv := BuildInstallCommand(spec, "/tmp/windows.iso", "/tmp/autounattend.img", "/tmp/devcell.img")
	joined := strings.Join(argv, " ")

	assert.Contains(t, joined, "file=/tmp/devcell.img,format=raw,if=none,id=devcellwim0")
}

func TestBuildInstallCommand_SCSI(t *testing.T) {
	spec := testSpec()
	spec.CDBus = "scsi"
	spec.VirtIOISO = "/tmp/virtio-win.iso"
	argv := BuildInstallCommand(spec, "/tmp/windows.iso", "/tmp/autounattend.img", "")
	joined := strings.Join(argv, " ")

	assert.Contains(t, joined, "virtio-scsi-pci,id="+CDBusID)
	assert.Contains(t, joined, "scsi-cd,drive=cdrom0,bus="+CDBusID+".0")
	assert.Contains(t, joined, "scsi-cd,drive=cdrom1,bus="+CDBusID+".0")
	assert.Contains(t, joined, "usb-storage,drive=usbfat0,removable=true,bus="+USBBusID+".0")
}

func TestBuildInstallCommand_NoAutounattend(t *testing.T) {
	spec := testSpec()
	argv := BuildInstallCommand(spec, "/tmp/windows.iso", "", "")
	joined := strings.Join(argv, " ")

	assert.Contains(t, joined, "file=/tmp/windows.iso")
	assert.NotContains(t, joined, "usbfat0")
}

func TestBuildRunCommand_BootsFromDisk(t *testing.T) {
	spec := testSpec()
	argv := BuildRunCommand(spec)
	joined := strings.Join(argv, " ")

	assert.Contains(t, joined, "-boot c")
}

func TestBuildRunCommand_AttachesVirtIOISO(t *testing.T) {
	spec := testSpec()
	spec.VirtIOISO = "/tmp/virtio-win.iso"
	argv := BuildRunCommand(spec)
	joined := strings.Join(argv, " ")

	assert.Contains(t, joined, "file=/tmp/virtio-win.iso,media=cdrom,if=none,id=cdrom1")
	assert.Contains(t, joined, "usb-storage,drive=cdrom1,removable=true,bus="+USBBusID+".0")
}

func TestBuildRunCommand_NoVirtIO(t *testing.T) {
	spec := testSpec()
	argv := BuildRunCommand(spec)
	joined := strings.Join(argv, " ")

	assert.NotContains(t, joined, "cdrom1")
}

func TestBuildRunCommand_SSHForward(t *testing.T) {
	spec := testSpec()
	spec.SSHPort = 2222
	argv := BuildRunCommand(spec)
	joined := strings.Join(argv, " ")

	assert.Contains(t, joined, "hostfwd=tcp:127.0.0.1:2222-:22")
}

func TestBuildRunCommand_LogVolume(t *testing.T) {
	spec := testSpec()
	spec.LogVolumePath = "/tmp/logs.img"
	argv := BuildRunCommand(spec)
	joined := strings.Join(argv, " ")

	assert.Contains(t, joined, "file=/tmp/logs.img,format=raw,if=none,id=usbfat0")
	assert.Contains(t, joined, "usb-storage,drive=usbfat0,removable=true")
}
