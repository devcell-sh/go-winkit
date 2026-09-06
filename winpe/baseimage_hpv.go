package winpe

// HPVImageConfig describes the hpv (hypervisor) stack: a base WinPE image with
// the VirtualMachinePlatform stack transplanted into boot.wim and the BCD set
// to launch the hypervisor at boot, so the guest can run HCS/WSL2 compute
// systems without setup.exe or DISM.
type HPVImageConfig struct {
	BaseImageConfig
	// InstallWim is the donor install.wim the VMP binaries are sourced from
	// (System32-first, then its WinSxS component store). Any stock media
	// works — VMP need not be pre-enabled.
	InstallWim string
	// TransplantOnly builds the VMP stack into boot.wim but leaves the
	// hypervisor DISABLED: no hypervisorlaunchtype, no first-boot enable hook.
	// This is the control image for engagement testing — VMP services are
	// registered but the hypervisor never launches, so a bring-up probe can
	// tell whether Vid/vmcompute require the hypervisor to start.
	TransplantOnly bool
}

// GenerateHypervisorEnableCmd produces the one-time first-boot hook that
// actually engages the hypervisor. The offline go-regedit BCD write sets
// hypervisorlaunchtype=Auto but winload does not honor it; bcdedit's in-guest
// write does (verified: a subsequent boot enters EL2, ~998s under TCG). So on
// first boot we rewrite the flag with bcdedit on the persistent boot volume,
// drop a marker so we run exactly once, and reboot into the enabled state.
//
// The boot volume (the FAT the firmware booted) is the only volume carrying
// \boot\bcd, so we locate it by probing drive letters (wpeinit has assigned
// them by the time gosshd.cmd calls this). The marker lives on that volume so
// it survives the reboot; the ramdisk (X:) would not.
func GenerateHypervisorEnableCmd() []byte {
	return []byte("@echo off\r\n" +
		"setlocal enableextensions\r\n" +
		"set HVBCD=\r\n" +
		"for %%d in (C D E F G H I J K L M N O) do @if not defined HVBCD if exist %%d:\\boot\\bcd set HVBCD=%%d\r\n" +
		"if not defined HVBCD goto :eof\r\n" +
		"if exist %HVBCD%:\\winkit\\hvenabled.flag goto :eof\r\n" +
		// Mark first so a mid-hook failure cannot cause a reboot loop.
		"if not exist %HVBCD%:\\winkit mkdir %HVBCD%:\\winkit\r\n" +
		"echo enabled> %HVBCD%:\\winkit\\hvenabled.flag\r\n" +
		"bcdedit /store %HVBCD%:\\boot\\bcd /set {default} hypervisorlaunchtype Auto\r\n" +
		"if exist %HVBCD%:\\EFI\\Microsoft\\Boot\\BCD bcdedit /store %HVBCD%:\\EFI\\Microsoft\\Boot\\BCD /set {default} hypervisorlaunchtype Auto\r\n" +
		"wpeutil reboot\r\n" +
		":eof\r\n")
}
