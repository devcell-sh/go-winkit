package qemu

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestBuildSetupBootArgv asserts the wsl2 full-install boot shape: the FAT
// Setup boot volume is booted (bootindex=1), the Windows ISO is attached only
// as a readable USB CD (NOT booted via El Torito, which ASSERTs on
// Linux-hosted EDK2), and the answer + virtio volumes ride along.
func TestBuildSetupBootArgv(t *testing.T) {
	spec := testSpec()
	spec.DiskPath = "/tmp/wsl2.qcow2" // the empty NVMe target (bootindex=0)
	spec.VirtIOISO = "/tmp/virtio-win.iso"
	spec.SSHPort = 20022
	spec.RDPPort = 23389

	argv := BuildSetupBootArgv(spec, "/tmp/wsl2-boot.qcow2", "/tmp/windows.iso", "/tmp/autounattend.img")
	joined := strings.Join(argv, " ")

	// Boot volume: qcow2 FAT ESP, usb-storage, bootindex=1.
	assert.Contains(t, joined, "file=/tmp/wsl2-boot.qcow2,format=qcow2,if=none,id=usbfat0")
	assert.Contains(t, joined, "usb-storage,drive=usbfat0,removable=true,bus="+USBBusID+".0,bootindex=1")

	// NVMe target is booted first (bootindex=0) and falls through when empty.
	assert.Contains(t, joined, "nvme,drive=disk0,serial=winkit0,bootindex=0")

	// Windows ISO: readable CD, NO bootindex (never the boot source), and NOT
	// on a scsi-cd El Torito path.
	assert.Contains(t, joined, "file=/tmp/windows.iso,media=cdrom,if=none,id=wincd")
	assert.Contains(t, joined, "usb-storage,drive=wincd,removable=true,bus="+USBBusID+".0")
	assert.NotContains(t, joined, "drive=wincd,removable=true,bus="+USBBusID+".0,bootindex")
	assert.NotContains(t, joined, "scsi-cd")

	// Answer volume + virtio ISO attached.
	assert.Contains(t, joined, "file=/tmp/autounattend.img,format=raw,if=none,id=answer0")
	assert.Contains(t, joined, "file=/tmp/virtio-win.iso,media=cdrom,if=none,id=viocd")

	// Port forwards for post-install SSH + RDP.
	assert.Contains(t, joined, "hostfwd=tcp:127.0.0.1:20022-:22")
	assert.Contains(t, joined, "hostfwd=tcp:127.0.0.1:23389-:3389")
}

// TestBuildSetupBootArgv_QcowAnswer covers a .qcow2 answer volume taking the
// qcow2 driver format instead of raw.
func TestBuildSetupBootArgv_QcowAnswer(t *testing.T) {
	spec := testSpec()
	argv := BuildSetupBootArgv(spec, "/tmp/boot.qcow2", "/tmp/win.iso", "/tmp/answer.qcow2")
	joined := strings.Join(argv, " ")
	assert.Contains(t, joined, "file=/tmp/answer.qcow2,format=qcow2,if=none,id=answer0")
}
