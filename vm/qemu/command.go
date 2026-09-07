package qemu

import (
	"fmt"
	"runtime"
	"strings"
)

const (
	USBBusID         = "usb-bus"
	CDBusID          = "cd-scsi-bus"
	InstallerCDDevID = "installer-cd"
	ProgressPortName = "winkit.progress.0"
)

// BuildWinPECommand constructs the QEMU argv for booting WinPE.
func BuildWinPECommand(spec Spec, winpeISO, answerImage string) []string {
	argv := baseCommand(spec)

	argv = append(argv,
		"-drive", fmt.Sprintf("file=%s,media=cdrom,if=none,id=cdrom0", winpeISO),
		"-device", fmt.Sprintf("usb-storage,drive=cdrom0,removable=true,bus=%s.0,id=%s,bootindex=1",
			USBBusID, InstallerCDDevID))

	if answerImage != "" {
		driveFormat := "raw"
		if strings.HasSuffix(answerImage, ".qcow2") {
			driveFormat = "qcow2"
		}
		argv = append(argv,
			"-drive", fmt.Sprintf("file=%s,format=%s,if=none,id=usbfat0", answerImage, driveFormat),
			"-device", fmt.Sprintf("usb-storage,drive=usbfat0,removable=true,bus=%s.0", USBBusID))
	}

	return argv
}

// WimBuilderSpec configures a WIM builder WinPE boot.
type WimBuilderSpec struct {
	Spec       Spec
	WinPEISO   string
	SharedImg  string
	WindowsISO string
	VirtIOISO  string
}

// BuildWimBuilderArgv constructs the QEMU argv for the WIM builder VM.
func BuildWimBuilderArgv(wbs WimBuilderSpec) []string {
	if wbs.Spec.CDBus == "scsi" {
		return buildWimBuilderSCSI(wbs)
	}
	return buildWimBuilderUSB(wbs)
}

func buildWimBuilderUSB(wbs WimBuilderSpec) []string {
	argv := BuildWinPECommand(wbs.Spec, wbs.WinPEISO, wbs.SharedImg)

	if wbs.WindowsISO != "" {
		argv = append(argv,
			"-drive", "file="+wbs.WindowsISO+",media=cdrom,if=none,id=cdrom1",
			"-device", "usb-storage,drive=cdrom1,removable=true,bus="+USBBusID+".0")
	}
	if wbs.VirtIOISO != "" {
		argv = append(argv,
			"-drive", "file="+wbs.VirtIOISO+",media=cdrom,if=none,id=cdrom2",
			"-device", "usb-storage,drive=cdrom2,removable=true,bus="+USBBusID+".0")
	}
	return argv
}

func buildWimBuilderSCSI(wbs WimBuilderSpec) []string {
	argv := baseCommand(wbs.Spec)

	argv = append(argv, "-device", fmt.Sprintf("virtio-scsi-pci,id=%s", CDBusID))

	// The FAT volume boots FIRST, not the CD. The volume carries
	// \EFI\BOOT\BOOTAA64.EFI (Windows bootmgr), which has its own ISO9660/UDF
	// drivers and loads BCD + boot.wim from the WinPE CD. Booting the CD's
	// El Torito image instead is fatal on Linux-hosted EDK2: cdboot crashes
	// with a synchronous exception on genisoimage-mastered media and the
	// firmware ASSERTs — it never falls through to the next boot entry.
	argv = append(argv,
		"-drive", fmt.Sprintf("file=%s,media=cdrom,if=none,id=cdrom0", wbs.WinPEISO),
		"-device", fmt.Sprintf("scsi-cd,drive=cdrom0,bus=%s.0,id=%s,bootindex=2",
			CDBusID, InstallerCDDevID))

	if wbs.SharedImg != "" {
		driveFormat := "raw"
		if strings.HasSuffix(wbs.SharedImg, ".qcow2") {
			driveFormat = "qcow2"
		}
		argv = append(argv,
			"-drive", fmt.Sprintf("file=%s,format=%s,if=none,id=usbfat0", wbs.SharedImg, driveFormat),
			"-device", fmt.Sprintf("usb-storage,drive=usbfat0,removable=true,bus=%s.0,bootindex=1", USBBusID))
	}

	if wbs.WindowsISO != "" {
		argv = append(argv,
			"-drive", fmt.Sprintf("file=%s,media=cdrom,if=none,id=cdrom1", wbs.WindowsISO),
			"-device", fmt.Sprintf("scsi-cd,drive=cdrom1,bus=%s.0", CDBusID))
	}

	if wbs.VirtIOISO != "" {
		argv = append(argv,
			"-drive", fmt.Sprintf("file=%s,media=cdrom,if=none,id=cdrom2", wbs.VirtIOISO),
			"-device", fmt.Sprintf("scsi-cd,drive=cdrom2,bus=%s.0", CDBusID))
	}

	return argv
}

// BuildInstallCommand constructs the QEMU argv for initial Windows installation.
// windowsISO is the Windows ARM64 installer. autounattendImage is a FAT image
// or .iso with autounattend.xml. devcellWimImg is an optional FAT/qcow2 volume
// carrying a custom winkit.wim.
func BuildInstallCommand(spec Spec, windowsISO, autounattendImage, devcellWimImg string) []string {
	if spec.CDBus == "scsi" {
		return buildInstallSCSI(spec, windowsISO, autounattendImage, devcellWimImg)
	}
	return buildInstallUSB(spec, windowsISO, autounattendImage, devcellWimImg)
}

func buildInstallUSB(spec Spec, windowsISO, autounattendImage, devcellWimImg string) []string {
	argv := baseCommand(spec)
	bootIdx := 1
	nextIdx := 0

	argv = append(argv,
		"-drive", fmt.Sprintf("file=%s,media=cdrom,if=none,id=cdrom0", windowsISO),
		"-device", fmt.Sprintf("usb-storage,drive=cdrom0,removable=true,bus=%s.0,id=%s,bootindex=%d",
			USBBusID, InstallerCDDevID, bootIdx))
	bootIdx++
	nextIdx = 1

	if spec.VirtIOISO != "" {
		argv = append(argv,
			"-drive", fmt.Sprintf("file=%s,media=cdrom,if=none,id=cdrom%d", spec.VirtIOISO, nextIdx),
			"-device", fmt.Sprintf("usb-storage,drive=cdrom%d,removable=true,bus=%s.0,bootindex=%d",
				nextIdx, USBBusID, bootIdx))
		bootIdx++
		nextIdx++
	}

	switch {
	case autounattendImage == "":
	case strings.HasSuffix(autounattendImage, ".iso"):
		argv = append(argv,
			"-drive", fmt.Sprintf("file=%s,media=cdrom,if=none,id=cdrom%d", autounattendImage, nextIdx),
			"-device", fmt.Sprintf("usb-storage,drive=cdrom%d,removable=true,bus=%s.0", nextIdx, USBBusID))
	default:
		argv = append(argv,
			"-drive", fmt.Sprintf("file=%s,format=raw,if=none,id=usbfat0", autounattendImage),
			"-device", fmt.Sprintf("usb-storage,drive=usbfat0,removable=true,bus=%s.0,bootindex=%d", USBBusID, bootIdx))
	}

	if devcellWimImg != "" {
		driveFormat := "raw"
		if strings.HasSuffix(devcellWimImg, ".qcow2") {
			driveFormat = "qcow2"
		}
		argv = append(argv,
			"-drive", fmt.Sprintf("file=%s,format=%s,if=none,id=devcellwim0", devcellWimImg, driveFormat),
			"-device", fmt.Sprintf("usb-storage,drive=devcellwim0,removable=true,bus=%s.0", USBBusID))
	}

	return applySSHForward(spec, argv)
}

func buildInstallSCSI(spec Spec, windowsISO, autounattendImage, devcellWimImg string) []string {
	argv := baseCommand(spec)
	bootIdx := 1
	nextIdx := 0

	argv = append(argv, "-device", fmt.Sprintf("virtio-scsi-pci,id=%s", CDBusID))

	argv = append(argv,
		"-drive", fmt.Sprintf("file=%s,media=cdrom,if=none,id=cdrom0", windowsISO),
		"-device", fmt.Sprintf("scsi-cd,drive=cdrom0,bus=%s.0,id=%s,bootindex=%d",
			CDBusID, InstallerCDDevID, bootIdx))
	bootIdx++
	nextIdx = 1

	if spec.VirtIOISO != "" {
		argv = append(argv,
			"-drive", fmt.Sprintf("file=%s,media=cdrom,if=none,id=cdrom%d", spec.VirtIOISO, nextIdx),
			"-device", fmt.Sprintf("scsi-cd,drive=cdrom%d,bus=%s.0,bootindex=%d",
				nextIdx, CDBusID, bootIdx))
		bootIdx++
		nextIdx++
	}

	switch {
	case autounattendImage == "":
	case strings.HasSuffix(autounattendImage, ".iso"):
		argv = append(argv,
			"-drive", fmt.Sprintf("file=%s,media=cdrom,if=none,id=cdrom%d", autounattendImage, nextIdx),
			"-device", fmt.Sprintf("scsi-cd,drive=cdrom%d,bus=%s.0", nextIdx, CDBusID))
	default:
		argv = append(argv,
			"-drive", fmt.Sprintf("file=%s,format=raw,if=none,id=usbfat0", autounattendImage),
			"-device", fmt.Sprintf("usb-storage,drive=usbfat0,removable=true,bus=%s.0,bootindex=%d", USBBusID, bootIdx))
	}

	if devcellWimImg != "" {
		driveFormat := "raw"
		if strings.HasSuffix(devcellWimImg, ".qcow2") {
			driveFormat = "qcow2"
		}
		argv = append(argv,
			"-drive", fmt.Sprintf("file=%s,format=%s,if=none,id=devcellwim0", devcellWimImg, driveFormat),
			"-device", fmt.Sprintf("usb-storage,drive=devcellwim0,removable=true,bus=%s.0", USBBusID))
	}

	return applySSHForward(spec, argv)
}

// BuildQcowBootArgv constructs the QEMU argv for booting a standalone WinPE
// FAT qcow2 volume (winpe.BuildBaseImageFiles output). The volume carries
// the whole boot chain, so it gets bootindex=1 on usb-storage; no optical
// media is attached.
func BuildQcowBootArgv(spec Spec, volumeImg string) []string {
	argv := baseCommand(spec)
	argv = append(argv,
		"-drive", fmt.Sprintf("file=%s,format=qcow2,if=none,id=usbfat0", volumeImg),
		"-device", fmt.Sprintf("usb-storage,drive=usbfat0,removable=true,bus=%s.0,bootindex=1", USBBusID))
	return applySSHForward(spec, argv)
}

// BuildSetupBootArgv boots a full unattended Windows install. bootVolume is a
// FAT qcow2 carrying the retail Setup boot chain (winpe.BuildSetupBootVolumeFiles)
// and gets bootindex=1; the empty NVMe target (spec.DiskPath, bootindex=0 in
// baseCommand) is tried first, fails, and firmware falls through to it. The
// Windows ISO is attached as a *readable* USB CD — not booted — so Setup's
// WinPE reads \sources\install.wim with inbox USB drivers (no virtio needed at
// install time; the target is NVMe, also inbox). The answer volume carries
// autounattend.xml + first-logon bootstrap; the virtio ISO ships the drivers
// the answer file registers in specialize.
func BuildSetupBootArgv(spec Spec, bootVolume, windowsISO, answerVolume string) []string {
	argv := baseCommand(spec)

	argv = append(argv,
		"-drive", fmt.Sprintf("file=%s,format=qcow2,if=none,id=usbfat0", bootVolume),
		"-device", fmt.Sprintf("usb-storage,drive=usbfat0,removable=true,bus=%s.0,bootindex=1", USBBusID))

	if windowsISO != "" {
		argv = append(argv,
			"-drive", fmt.Sprintf("file=%s,media=cdrom,if=none,id=wincd", windowsISO),
			"-device", fmt.Sprintf("usb-storage,drive=wincd,removable=true,bus=%s.0", USBBusID))
	}

	if answerVolume != "" {
		driveFormat := "raw"
		if strings.HasSuffix(answerVolume, ".qcow2") {
			driveFormat = "qcow2"
		}
		argv = append(argv,
			"-drive", fmt.Sprintf("file=%s,format=%s,if=none,id=answer0", answerVolume, driveFormat),
			"-device", fmt.Sprintf("usb-storage,drive=answer0,removable=true,bus=%s.0", USBBusID))
	}

	if spec.VirtIOISO != "" {
		argv = append(argv,
			"-drive", fmt.Sprintf("file=%s,media=cdrom,if=none,id=viocd", spec.VirtIOISO),
			"-device", fmt.Sprintf("usb-storage,drive=viocd,removable=true,bus=%s.0", USBBusID))
	}

	return applySSHForward(spec, argv)
}

// applySSHForward patches the user netdev with a host port forward to the
// guest's SSH port when the spec asks for one.
func applySSHForward(spec Spec, argv []string) []string {
	if spec.SSHPort == 0 {
		return argv
	}
	host := spec.SSHHost
	if host == "" {
		host = "127.0.0.1"
	}
	guestPort := spec.SSHGuestPort
	if guestPort == 0 {
		guestPort = 22
	}
	for i, a := range argv {
		if a == "-netdev" && i+1 < len(argv) {
			argv[i+1] = argv[i+1] + fmt.Sprintf(",hostfwd=tcp:%s:%d-:%d", host, spec.SSHPort, guestPort)
			if spec.RDPPort != 0 {
				argv[i+1] = argv[i+1] + fmt.Sprintf(",hostfwd=tcp:%s:%d-:3389", host, spec.RDPPort)
			}
			// A second forward for the Windows OpenSSH the wsl2 image ships on
			// :22, separate from the gosshd provisioning channel above.
			if spec.OpenSSHHostPort != 0 {
				argv[i+1] = argv[i+1] + fmt.Sprintf(",hostfwd=tcp:%s:%d-:22", host, spec.OpenSSHHostPort)
			}
			break
		}
	}
	return argv
}

// BuildRunCommand constructs the QEMU argv for normal VM operation (post-install).
func BuildRunCommand(spec Spec) []string {
	argv := baseCommand(spec)
	argv = append(argv, "-boot", "c")
	argv = applySSHForward(spec, argv)

	if spec.VirtIOISO != "" {
		argv = append(argv,
			"-drive", fmt.Sprintf("file=%s,media=cdrom,if=none,id=cdrom1", spec.VirtIOISO),
			"-device", fmt.Sprintf("usb-storage,drive=cdrom1,removable=true,bus=%s.0", USBBusID))
	}

	if spec.LogVolumePath != "" {
		argv = append(argv,
			"-drive", fmt.Sprintf("file=%s,format=raw,if=none,id=usbfat0", spec.LogVolumePath),
			"-device", "usb-storage,drive=usbfat0,removable=true")
	}

	return argv
}

func baseCommand(spec Spec) []string {
	qemuBin := "qemu-system-aarch64"

	machine := machineType(spec)
	if spec.MachineType != "" {
		machine = spec.MachineType
	}

	argv := []string{
		qemuBin,
		"-machine", machine,
		"-cpu", cpuType(spec),
		"-accel", spec.effectiveAccel(),
		"-smp", fmt.Sprintf("%d", spec.CPUs),
		"-m", fmt.Sprintf("%dG", spec.MemoryGB),
	}

	// UEFI firmware. -kernel is what the proven secure-world (EL3) config
	// uses: QEMU's boot stub handles the EL3 entry a normal-world EDK2 cannot.
	// pflash is the normal mode and keeps an NVRAM vars store.
	if spec.FirmwareKernel {
		argv = append(argv, "-kernel", spec.FirmwarePath)
	} else {
		argv = append(argv,
			"-drive", fmt.Sprintf("if=pflash,format=raw,readonly=on,file=%s", spec.FirmwarePath))
		if spec.VarsPath != "" {
			argv = append(argv,
				"-drive", fmt.Sprintf("if=pflash,format=raw,file=%s", spec.VarsPath))
		}
	}

	diskDrive := fmt.Sprintf("if=none,format=qcow2,file=%s,id=disk0", spec.DiskPath)
	if spec.DiskCacheMode != "" {
		diskDrive += ",cache=" + spec.DiskCacheMode
	}
	argv = append(argv,
		"-drive", diskDrive,
		"-device", "nvme,drive=disk0,serial=winkit0,bootindex=0")

	argv = append(argv,
		"-netdev", "user,id=net0",
		"-device", "virtio-net-pci,netdev=net0")

	argv = append(argv, "-display", spec.DisplayType)
	argv = append(argv, "-device", "ramfb")

	argv = append(argv,
		"-device", fmt.Sprintf("qemu-xhci,id=%s,p2=8", USBBusID),
		"-device", "usb-kbd",
		"-device", "usb-tablet")

	if spec.SerialLogPath != "" {
		argv = append(argv, "-serial", "file:"+spec.SerialLogPath)
	}

	needsSerialBus := spec.GuestProgressLogPath != "" || spec.GuestStructuredLogPath != ""
	if needsSerialBus {
		argv = append(argv, "-device", "virtio-serial-pci,id=virtio-serial0")
	}

	if spec.GuestProgressLogPath != "" {
		argv = append(argv,
			"-chardev", "file,id=guestprog,path="+spec.GuestProgressLogPath,
			"-device", "virtserialport,bus=virtio-serial0.0,chardev=guestprog,name="+ProgressPortName)
	}

	if spec.GuestStructuredLogPath != "" {
		argv = append(argv,
			"-chardev", "file,id=gueststruct,path="+spec.GuestStructuredLogPath,
			"-device", "virtserialport,bus=virtio-serial0.0,chardev=gueststruct,name=winkit.structured.0")
	}

	argv = append(argv,
		"-qmp", "unix:"+QMPSocketPath(spec)+",server,nowait")

	if spec.NoReboot {
		argv = append(argv, "-no-reboot")
	}

	if spec.VMName != "" {
		argv = append(argv, "-name", spec.VMName)
	}

	if spec.SMBIOSSerial != "" {
		argv = append(argv, "-smbios", "type=1,serial="+spec.SMBIOSSerial)
	}

	return argv
}

func machineType(spec Spec) string {
	accel := spec.effectiveAccel()
	if strings.HasPrefix(accel, "tcg") {
		return "virt,virtualization=true"
	}
	if runtime.GOOS == "darwin" {
		return "virt,highmem=on"
	}
	return "virt"
}

func cpuType(spec Spec) string {
	if spec.CPU != "" {
		return spec.CPU
	}
	accel := spec.effectiveAccel()
	if strings.HasPrefix(accel, "tcg") {
		return "max,pauth-impdef=on"
	}
	if runtime.GOOS == "darwin" {
		return "host"
	}
	return "max"
}
