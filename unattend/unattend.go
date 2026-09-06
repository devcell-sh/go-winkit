package unattend

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"
	"text/template"

	"github.com/devcell-sh/go-winkit/isokit"
	"github.com/devcell-sh/go-winkit/winpe"
)

// Config holds parameters for generating an autounattend.xml file.
type Config struct {
	Username      string
	Password      string
	Locale        string
	Hostname      string
	// DistroName is the WSL1 distro name used for import and all wsl.exe -d
	// invocations. Defaults to "winkit". The bootstrap template, verify
	// functions, and PS1 prompt all derive from this.
	DistroName string
	VirtIODrivers []VirtIODriver
	SSHPubKey     string
	TimeZone      string
	// EnableRDP turns on Remote Desktop and disables the Windows firewall so
	// the forwarded RDP port is reachable. Gate this on Spec.RDPPort > 0 so
	// cells that never expose RDP do not get it.
	EnableRDP bool
	// WinPEAgent ships the WinPE control agent on the answer volume and adds
	// the windowsPE launcher that starts it. The agent snapshots Setup's
	// Panther logs to the volume every few seconds and executes commands the
	// host drops there — the only look inside a failing windowsPE phase.
	WinPEAgent bool
	// OpenSSHPayload is the filename of the Win32-OpenSSH release shipped on
	// the answer volume, empty when none was fetched.
	//
	// OpenSSH Server cannot be installed from our media: the capability is
	// present in the manifest but its payload is not, so Add-WindowsCapability
	// fails 0x80070002 with the capability stuck `Staged` — even with Windows
	// Update reachable and permitted (DISM logged LimitAccess:0). The UUP
	// package carries only OpenSSH-Client; the Server FoD ships on a separate
	// build-matched ISO we do not have. Shipping the standalone release side-
	// steps Windows servicing entirely.
	OpenSSHPayload string
	// OpenSSHPayloadData is the payload's bytes, written to the answer volume
	// by BuildAnswerVolume.
	OpenSSHPayloadData []byte
	// OpenSSHPayloadSize is the payload's true length. PadForFAT cluster-aligns
	// every file with trailing newlines, which is harmless for text but can
	// break a zip: readers locate the End-of-Central-Directory record by
	// scanning back from the end, and unexpected trailing bytes make that
	// inconsistent. The guest truncates to this length before extracting.
	OpenSSHPayloadSize int
	// GosshdBinaryName is the filename of the gosshd server binary shipped on
	// the answer volume, empty when none is used. gosshd is the wsl2 build's
	// dedicated provisioning SSH: specialize copies it to C: and registers an
	// onstart SYSTEM task, so a reachable channel exists before (and
	// independent of) the fragile first-logon bootstrap that installs the
	// Windows OpenSSH the delivered image ships on :22. It travels as an
	// exact (unpadded) file — a PE image tolerates trailing zeros, but the
	// scheduled task launches it by name, so keeping it byte-exact avoids any
	// surprise.
	GosshdBinaryName string
	// GosshdBinaryData is the gosshd binary's bytes, written to the answer
	// volume by BuildAnswerVolume.
	GosshdBinaryData []byte
	// GosshdListenAddr is the address the specialize-registered gosshd task
	// listens on, e.g. ":2222". It is a non-standard port on purpose so the
	// provisioning gosshd coexists with the Windows OpenSSH the image ships on
	// :22. Empty falls back to DefaultGosshdListenAddr.
	GosshdListenAddr string
	// GosshdVsockPort, when non-zero, adds -vsock-port <N> to the gosshd
	// launch command so it listens on a vsock port alongside TCP. Used by the
	// vz backend where NAT port forwarding is unavailable.
	GosshdVsockPort uint32
	// GuestHostIP is the IP address the guest uses to reach the host.
	// Defaults to "10.0.2.2" (QEMU user-mode networking). Callers using a
	// different networking topology (e.g. bridged, WSL2 NAT) set this to the
	// appropriate host address.
	GuestHostIP string
	// SFTPPort, when non-zero, adds a first-logon bootstrap step that mounts
	// the host's sftpshare server as a fixed-disk guest drive: WinFsp is
	// installed from the shipped MSI, rclone from the shipped zip, and the
	// mount runs as an on-start SYSTEM scheduled task (RcloneMountTaskName).
	// Fixed-disk (DRIVE_FIXED) is the point: WSL1 drvfs cannot stat/read
	// through the WebClient's DRIVE_REMOTE mounts (CELL-532), and some
	// Windows tools refuse network drives outright.
	SFTPPort int
	// SFTPUser and SFTPPassword authenticate against the host share.
	// Empty fall back to "winkit"/"winkit" (loopback-only listener; the
	// credentials satisfy SFTP, they are not a security boundary).
	SFTPUser     string
	SFTPPassword string
	// SFTPVolumeName is the WinFsp volume label Explorer shows next to the
	// drive letter (rclone mount --volname). Defaults to "winkit".
	SFTPVolumeName string
	// SFTPDrive is the drive letter (no colon) the share is mounted at.
	// Empty falls back to "W".
	SFTPDrive string
	// RclonePayload is the filename of the rclone windows/arm64 release zip
	// shipped on the answer volume, with RclonePayloadData its bytes. Ships
	// byte-exact: zip readers scan back from the end of the file, so FAT
	// cluster padding would corrupt it.
	RclonePayload     string
	RclonePayloadData []byte
	// WinFspPayload is the filename of the WinFsp MSI shipped on the answer
	// volume, with WinFspPayloadData its bytes. Ships byte-exact: trailing
	// padding breaks the MSI's digital signature.
	WinFspPayload     string
	WinFspPayloadData []byte
	// NixWSLPayloadData, when non-empty, ships as nix.wsl on the answer
	// volume (byte-exact: it is a gzip tarball). The bootstrap's 'import
	// WSL1 Nix distro' step scans attached drives for the name and imports
	// it under DistroName. See the wslnix package for how it is built.
	NixWSLPayloadData []byte
	// EnableWSL1Feature adds a specialize dism command that enables the
	// Microsoft-Windows-Subsystem-Linux optional feature (lxcore.sys).
	// WSL1 distros cannot register without it — wsl --import --version 1
	// exits -1 (WSL_E_WSL1_NOT_SUPPORTED). Enabled with /norestart during
	// specialize so the reboot into OOBE completes it before the
	// first-logon bootstrap imports nix.wsl. The hypervisor-side features
	// (VirtualMachinePlatform, Hyper-V) stay host-driven over SSH and
	// WINKIT_WSL2-gated: WSL1 does not need them.
	EnableWSL1Feature bool
	// SMBIOSHostname registers a boot-time scheduled task that reads the
	// SMBIOS serial number (QEMU -smbios type=1,serial=<name>) and renames
	// the computer if it differs from the current hostname. The rename
	// takes effect on the next boot. This lets the QEMU launcher set a
	// per-run hostname without rebuilding the answer volume.
	SMBIOSHostname bool
	// KeepDisplayAwake adds a specialize command that zeroes the AC display,
	// standby and disk idle timeouts, so the console stays visible for the
	// whole automated install. The equivalent bootstrap step runs only at
	// first logon (after OOBE) and is skipped entirely if the bootstrap fails
	// early — which left screendumps black for most of a run. Set for headless
	// installs whose only window is the QMP screendump.
	KeepDisplayAwake bool
	// PwshFiles is the extracted PowerShell 7 directory, keyed by answer-
	// volume path (e.g. "/pwsh/pwsh.exe"). Stock WinPE has no powershell.exe;
	// these files provide pwsh.exe which the bootstrap.cmd shim and the
	// windowsPE agent launcher probe for at runtime.
	PwshFiles map[string][]byte
	// ImageName selects which image in install.wim to install. The Windows 11
	// ARM64 media carries three (Home, Home Single Language, Pro); without a
	// choice Setup stops to ask. Defaults to "Windows 11 Pro".
	ImageName string
	// InstallWimPath, when set, tells Windows Setup to install from a custom
	// WIM at this path (e.g. "X:\winkit-install.wim") instead of the stock
	// install.wim inside the ISO. The path must be reachable from WinPE — a
	// drive letter assigned to a USB volume the VM mounts.
	InstallWimPath string
	// EFIBootLoader is the raw bytes of the Windows EFI bootloader
	// (BOOTAA64.EFI), extracted from the installer ISO at build time. When
	// set, BuildAnswerVolume writes it to /EFI/BOOT/BOOTAA64.EFI on the
	// answer FAT volume. This lets startup.nsh chainload the installer from
	// the USB volume (FS0) instead of the CD (FS1), working around QEMU
	// 11/HVF where the firmware can enumerate CD files but cannot execute
	// PE binaries from them (CELL-427).
	EFIBootLoader []byte
	// AnswerDrivers are driver files BuildAnswerVolume ships on the answer
	// volume, keyed by volume path (see LoadWinPEStorageDrivers). Every
	// .inf among them gets a windowsPE RunSynchronous drvload command —
	// see WinPEDriverLoads — which runs just before Modern Setup searches
	// for install media. ARM64 WinPE has no inbox vioscsi, so without this
	// the virtio-scsi installer CD is invisible and Setup stops at "a media
	// driver your computer needs is missing" (CELL-429).
	//
	// The files ship byte-exact, not PadForFAT-padded: Setup's driver
	// import validates them against the catalog's hashes.
	AnswerDrivers map[string][]byte
	// WallpaperName is the filename of a wallpaper image shipped on the
	// answer volume (e.g. "wallpaper.jpg"). When set with WallpaperData,
	// BuildAnswerVolume writes the file and the bootstrap copies it to
	// C:\Windows\Web\Wallpaper\winkit\ and applies it via registry.
	WallpaperName string
	// WallpaperData is the wallpaper image bytes, written to the answer
	// volume by BuildAnswerVolume.
	WallpaperData []byte
	// WallpaperPath is the guest-local path to an existing wallpaper file.
	// When set (and WallpaperName is empty), the bootstrap sets registry
	// keys pointing to this path without copying. Use when the file is
	// already on the guest (e.g. from a WebDAV share).
	WallpaperPath string
	// CustomSteps are caller-supplied PowerShell steps appended to the
	// bootstrap after all built-in steps and before guest diagnostics.
	// Each runs inside Invoke-Step: exceptions are caught and logged.
	CustomSteps []CustomStep
	// DisableServices overrides the default list of services to disable in
	// specialize. When nil, DefaultQuietServices() is used. When non-nil
	// (even if empty), exactly those services are disabled. Filter
	// DefaultQuietServices() to keep specific services enabled.
	DisableServices []QuietService
	// AgentCommand, when WinPEAgent is set, is pre-baked into the agent's
	// command file (winpe.AgentCommandFile) so the agent executes it on its first
	// poll and writes the combined output to winkit-out.txt — a one-shot
	// diagnostic channel into WinPE, which has no network and no QGA.
	AgentCommand string
}

// winPEFixedSyncCommands is how many RunSynchronousCommand entries the
// windowsPE template always emits (the LabConfig bypasses). Optional
// commands are numbered from here. <Order> values must be contiguous from
// 1: run 20260812T132820 shipped 1,2,3,4,5,7 — the agent launcher at 6 was
// gated off while a driver loader at 7 was gated on — and Setup rejected
// the whole answer file with 0x8007000D (ERROR_INVALID_DATA) before
// executing anything. TestGenerateXML_WindowsPEOrdersAreContiguous
// guards this constant against drift.
const winPEFixedSyncCommands = 5

// AgentLauncherOrder is the <Order> of the agent launcher command.
func (c Config) AgentLauncherOrder() int {
	return winPEFixedSyncCommands + 1
}

// WinPEDriverLoad is one drvload RunSynchronousCommand.
type WinPEDriverLoad struct {
	Order       int
	Path        string
	Description string
}

// WinPEDriverLoads returns one drvload command per .inf in AnswerDrivers,
// numbered contiguously after the fixed commands and the agent launcher.
// One command per INF keeps each identical to the shape Setup is known to
// accept — see winpe.DriverLoadCommand.
func (c Config) WinPEDriverLoads() []WinPEDriverLoad {
	infs := c.winPEDriverINFs()
	if len(infs) == 0 {
		return nil
	}
	next := winPEFixedSyncCommands + 1
	if c.WinPEAgent {
		next++
	}
	loads := make([]WinPEDriverLoad, 0, len(infs))
	for i, inf := range infs {
		loads = append(loads, WinPEDriverLoad{
			Order:       next + i,
			Path:        escapeXMLAmp(winpe.DriverLoadCommand(inf)),
			Description: "Load " + inf + " so Setup can see the installer media",
		})
	}
	return loads
}

// winPEDriverINFs returns the volume-relative backslash paths of the .inf
// files in AnswerDrivers, sorted for deterministic rendering.
func (c Config) winPEDriverINFs() []string {
	var infs []string
	for p := range c.AnswerDrivers {
		if !strings.EqualFold(path.Ext(p), ".inf") {
			continue
		}
		infs = append(infs, strings.ReplaceAll(strings.TrimPrefix(p, "/"), "/", `\`))
	}
	sort.Strings(infs)
	return infs
}

// escapeXMLAmp makes a command line safe to drop into an XML element. Only
// & can appear in the commands we generate; text/template does not escape.
func escapeXMLAmp(s string) string {
	return strings.ReplaceAll(s, "&", "&amp;")
}

// VirtIODriver describes a driver to stage during Windows setup.
//
// The driver is staged with pnputil /add-driver (without /install) in
// specialize RunSynchronous. Staging puts the driver into the driver store
// without triggering PnP enumeration; Windows' own PnP discovers and installs
// it automatically on the next boot (the OOBE boot), so the driver is active
// before first logon. This is critical for netkvm (gosshd needs the NIC) and
// vioserial (progress logging through OOBE).
//
// History: pnputil /add-driver /install in specialize caused a reboot loop
// ("computer restarted unexpectedly", run 20260831T202847) because /install
// forces PnP enumeration during Setup's own device-setup phase. Without
// /install, pnputil only stages into the store and does not interfere.
// PnpCustomizationsWinPE/DriverPaths was also tried but aborts Setup when the
// path does not resolve (0x80070001 - 0x40030, run 20260729T172019).
//
// The stage command is a short cmd for-loop, NOT a PowerShell one-liner: Setup
// rejects the whole answer file (0x80220005) when any
// RunSynchronousCommand/Path exceeds 259 chars (run 20260831T205732).
type VirtIODriver struct {
	// INFRelPath is the INF's path relative to the root of whatever volume
	// carries it (the virtio driver CD), without a drive letter — the letter
	// is probed at runtime. E.g. `NetKVM\w11\ARM64\netkvm.inf`.
	INFRelPath  string
	Description string
}

// NetKVMDriverPaths returns the virtio-win NetKVM network driver for Windows
// ARM64.
//
// Storage needs no injection — NVMe and USB CD are inbox — but the NIC is
// virtio-net-pci, for which Windows ARM64 has no inbox driver. Without it the
// installed guest has no network: no SSH, no winget, no WSL distro download.
// Staged in specialize so PnP installs it on the OOBE boot, giving gosshd
// a NIC before first logon.
func NetKVMDriverPaths() []VirtIODriver {
	return []VirtIODriver{{
		INFRelPath:  `NetKVM\w11\ARM64\netkvm.inf`,
		Description: "Install the VirtIO network driver (NetKVM)",
	}}
}

// VioserialDriverPaths returns the virtio-win vioserial driver for Windows
// ARM64.
//
// The vioserial driver makes the virtio-serial port visible to Windows as
// \\.\Global\winkit.progress.0. Without it the bootstrap's Send-Progress
// writes to nothing: PL011 UART does not register as a COMx port on ARM64,
// and virtio-serial needs this driver. Staged in specialize so PnP installs
// it on the OOBE boot, giving progress logging through OOBE and first logon.
func VioserialDriverPaths() []VirtIODriver {
	return []VirtIODriver{{
		INFRelPath:  `vioserial\w11\ARM64\vioser.inf`,
		Description: "Install the VirtIO serial driver (vioserial)",
	}}
}

// specializeFixedCommands is how many RunSynchronousCommand entries the
// specialize template always emits before the optional VirtIODrivers block.
// 1=BypassNRO, 2=firewall (when EnableRDP). Because the firewall command is
// conditional, the driver block uses specializeDriverBaseOrder (always 3)
// regardless of whether the firewall command is present: specialize orders
// need not be contiguous (unlike windowsPE).
const specializeDriverBaseOrder = 3

// SpecializeDriverStageOrders returns one order per VirtIODriver for the
// specialize pnputil /add-driver staging commands. Empty when no drivers.
func (c Config) SpecializeDriverStageOrders() []int {
	orders := make([]int, len(c.VirtIODrivers))
	for i := range c.VirtIODrivers {
		orders[i] = specializeDriverBaseOrder + i
	}
	return orders
}

// specializePostDriverBase is the first order after all driver-stage commands.
func (c Config) specializePostDriverBase() int {
	return specializeDriverBaseOrder + len(c.VirtIODrivers)
}

// SpecializeBootstrapCopyOrder returns the <Order> for the specialize command
// that copies the bootstrap script from the answer volume to C:\.
func (c Config) SpecializeBootstrapCopyOrder() int {
	return c.specializePostDriverBase()
}

// FirstLogonBootstrapOrder returns the <Order> of the bootstrap launch in
// FirstLogonCommands. Drivers are now staged in specialize and auto-installed
// by PnP on the OOBE boot, so the bootstrap is the first (and only) command.
func (c Config) FirstLogonBootstrapOrder() int {
	return 1
}

// DefaultGosshdListenAddr is the provisioning gosshd listen address when a
// config ships the binary without an explicit address. A non-standard port so
// it never collides with the Windows OpenSSH the image ships on :22.
const DefaultGosshdListenAddr = ":2222"

// SpecializeGosshdCopyOrder and SpecializeGosshdTaskOrder are the <Order>s for
// the specialize commands that copy gosshd to C:\ and register its onstart
// SYSTEM task. They follow the bootstrap copy; orders need not be contiguous,
// Windows Setup runs them in ascending order.
func (c Config) SpecializeGosshdCopyOrder() int { return c.specializePostDriverBase() + 1 }
func (c Config) SpecializeGosshdTaskOrder() int { return c.specializePostDriverBase() + 2 }

// SpecializeSMBIOSHostnameOrder is the <Order> for the specialize command
// that registers the boot-time hostname-from-SMBIOS scheduled task.
func (c Config) SpecializeSMBIOSHostnameOrder() int { return c.specializePostDriverBase() + 3 }

// SpecializeKeepDisplayAwakeOrder is the <Order> for the specialize powercfg
// that keeps the console visible through the whole install (see KeepDisplayAwake).
func (c Config) SpecializeKeepDisplayAwakeOrder() int { return c.specializePostDriverBase() + 4 }

// specializeQuietServicesBase is the first <Order> used by the quiet-services
// block. Each service disable is a separate RunSynchronousCommand to stay under
// the 259-char Path limit enforced by Windows Setup.
func (c Config) specializeQuietServicesBase() int { return c.specializePostDriverBase() + 5 }

// CustomStep is a caller-supplied PowerShell step appended to the
// first-logon bootstrap.
type CustomStep struct {
	Name   string // step label passed to Invoke-Step
	Script string // PowerShell body
}

// QuietService identifies a Windows service to disable during specialize.
type QuietService struct {
	Key  string // registry key under HKLM\SYSTEM\CurrentControlSet\Services
	Desc string
}

// defaultQuietServices lists the services disabled in specialize and the
// <Order> offset from specializeQuietServicesBase for each command.
var defaultQuietServices = []QuietService{
	{"WinDefend", "Windows Defender Antivirus Service"},
	{"WdNisSvc", "Windows Defender Network Inspection Service"},
	{"WdNisDrv", "Windows Defender Network Inspection Driver"},
	{"WdFilter", "Windows Defender Minifilter Driver"},
	{"WdBoot", "Windows Defender Boot Driver"},
	{"Sense", "Windows Defender Advanced Threat Protection"},
	{"WSearch", "Windows Search"},
	{"SysMain", "Superfetch"},
	{"DiagTrack", "Connected User Experiences and Telemetry"},
	{"dmwappushservice", "WAP Push Message Routing Service"},
	{"wuauserv", "Windows Update"},
	{"UsoSvc", "Update Orchestrator Service"},
	{"WbioSrvc", "Windows Biometric Service"},
	{"MapsBroker", "Downloaded Maps Manager"},
	{"TabletInputService", "Touch Keyboard and Handwriting Panel"},
	{"WerSvc", "Windows Error Reporting Service"},
	{"Spooler", "Print Spooler"},
	{"PhoneSvc", "Phone Service"},
	{"wisvc", "Windows Insider Service"},
	{"XblAuthManager", "Xbox Live Auth Manager"},
	{"XblGameSave", "Xbox Live Game Save"},
}

// quietPolicyCount is the number of extra registry policy commands after the
// per-service disables: 2 Defender policy + 1 TamperProtection + 1 EarlyLaunch
// + 2 Windows Update + 1 telemetry.
const quietPolicyCount = 7

// SpecializeQuietServicesOrders returns the <Order> values for each service
// disable command plus the policy registry commands at the end.
func (c Config) SpecializeQuietServicesOrders() []int {
	base := c.specializeQuietServicesBase()
	n := len(c.QuietServices()) + quietPolicyCount
	orders := make([]int, n)
	for i := range orders {
		orders[i] = base + i
	}
	return orders
}

// DefaultQuietServices returns the built-in list of services disabled during
// specialize. Callers can filter this to keep specific services enabled.
func DefaultQuietServices() []QuietService {
	out := make([]QuietService, len(defaultQuietServices))
	copy(out, defaultQuietServices)
	return out
}

// QuietServices returns the service list for the template.
func (c Config) QuietServices() []QuietService {
	if c.DisableServices != nil {
		return c.DisableServices
	}
	return defaultQuietServices
}

// SpecializeQuietServicesEnd is the first order after all quiet-services commands.
func (c Config) specializeQuietServicesEnd() int {
	return c.specializeQuietServicesBase() + len(c.QuietServices()) + quietPolicyCount
}

// SpecializeWSL1FeatureOrder is the <Order> for the dism command that enables
// the WSL1 optional feature (see EnableWSL1Feature).
func (c Config) SpecializeWSL1FeatureOrder() int { return c.specializeQuietServicesEnd() }

// GosshdListenAddrOrDefault is the address the specialize task launches gosshd
// on, falling back to DefaultGosshdListenAddr.
func (c Config) GosshdListenAddrOrDefault() string {
	if c.GosshdListenAddr != "" {
		return c.GosshdListenAddr
	}
	return DefaultGosshdListenAddr
}

// GosshdVsockFlags returns the -vsock-port flag fragment for the schtasks
// command, or empty when vsock is not configured.
func (c Config) GosshdVsockFlags() string {
	if c.GosshdVsockPort == 0 {
		return ""
	}
	return fmt.Sprintf(" -vsock-port %d", c.GosshdVsockPort)
}

// DefaultSessionUser is used when the host provides no $USER.
const DefaultSessionUser = "winkit"

// SessionUsername returns the account name to create in the guest: the host's
// $USER, mirroring winkit's HOST_USER model (Docker's entrypoint and the tart
// engine derive their session user the same way), so a Windows cell has the
// same account as every other engine.
func SessionUsername() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return DefaultSessionUser
}

// DefaultConfig returns sensible defaults for a winkit Windows VM.
func DefaultConfig() Config {
	return Config{
		Username:  SessionUsername(),
		Password:  "rdp",
		Locale:    "en-US",
		Hostname:   "winkit",
		DistroName: "winkit",
		TimeZone:  "UTC",
		ImageName: "Windows 11 Pro",
		// No driver injection: the VM uses NVMe for disk and a USB CD-ROM for
		// media, both covered by inbox Windows ARM64 drivers (CELL-359).
		// Callers wanting virtio devices must supply VirtIODrivers explicitly.
	}
}

var autounattendFuncs = template.FuncMap{
	"inc":      func(i int) int { return i + 1 },
	"add":      func(a, b int) int { return a + b },
	"addOrder": func(i, base int) int { return i + base },
	// agentLauncher emits winpe.AgentLauncherCommand with XML escaping; the
	// command is Go-generated so the template and the shipped script cannot
	// drift apart.
	"agentLauncher": func() string {
		return strings.ReplaceAll(winpe.AgentLauncherCommand(), "&", "&amp;")
	},
}

var autounattendTmpl = template.Must(
	template.New("autounattend").Funcs(autounattendFuncs).Parse(autounattendTmplStr),
)

// GenerateXML produces a Windows unattended install XML.
func GenerateXML(cfg Config) []byte {
	var buf bytes.Buffer
	if err := autounattendTmpl.Execute(&buf, cfg); err != nil {
		panic(fmt.Sprintf("autounattend template error: %v", err))
	}
	return buf.Bytes()
}

// The oobeSystem OOBE block hides every screen Microsoft documents for a fully
// automated OOBE. The Skip*OOBE settings are deliberately not used — Microsoft
// warns against them for this purpose:
// https://learn.microsoft.com/en-us/windows-hardware/customize/desktop/automate-oobe
const autounattendTmplStr = `<?xml version="1.0" encoding="utf-8"?>
<unattend xmlns="urn:schemas-microsoft-com:unattend">

  <settings pass="windowsPE">
    <!-- No PnpCustomizationsWinPE/DriverPaths component here: Modern Setup
         (MOUPG) parses it and then ignores it — run 20260812T150644 logged
         "SetupManager: Drivers Path: []" with the component present and a
         resolvable %configsetroot% path. Since every element in this pass
         is a potential Setup-abort (an unresolved DriverPaths is exactly
         how run 20260729T172019 died), a proven no-op does not earn its
         place. Drivers load through the RunSynchronous commands below. -->
    <component name="Microsoft-Windows-International-Core-WinPE"
               processorArchitecture="arm64" publicKeyToken="31bf3856ad364e35"
               language="neutral" versionScope="nonSxS"
               xmlns:wcm="http://schemas.microsoft.com/WMIConfig/2002/State">
      <SetupUILanguage>
        <UILanguage>{{.Locale}}</UILanguage>
      </SetupUILanguage>
      <InputLocale>{{.Locale}}</InputLocale>
      <SystemLocale>{{.Locale}}</SystemLocale>
      <UILanguage>{{.Locale}}</UILanguage>
      <UserLocale>{{.Locale}}</UserLocale>
    </component>

    <component name="Microsoft-Windows-Setup"
               processorArchitecture="arm64" publicKeyToken="31bf3856ad364e35"
               language="neutral" versionScope="nonSxS"
               xmlns:wcm="http://schemas.microsoft.com/WMIConfig/2002/State">


      <!-- Windows 11 evaluates its hardware requirements during this pass, so
           the bypass keys must already be in the WinPE registry. Setting them
           later is too late — Setup stops on "This PC doesn't currently meet
           Windows 11 system requirements". -->
      <RunSynchronous>
        <RunSynchronousCommand wcm:action="add">
          <Order>1</Order>
          <Path>reg add HKLM\SYSTEM\Setup\LabConfig /v BypassTPMCheck /t REG_DWORD /d 1 /f</Path>
        </RunSynchronousCommand>
        <RunSynchronousCommand wcm:action="add">
          <Order>2</Order>
          <Path>reg add HKLM\SYSTEM\Setup\LabConfig /v BypassSecureBootCheck /t REG_DWORD /d 1 /f</Path>
        </RunSynchronousCommand>
        <RunSynchronousCommand wcm:action="add">
          <Order>3</Order>
          <Path>reg add HKLM\SYSTEM\Setup\LabConfig /v BypassRAMCheck /t REG_DWORD /d 1 /f</Path>
        </RunSynchronousCommand>
        <RunSynchronousCommand wcm:action="add">
          <Order>4</Order>
          <Path>reg add HKLM\SYSTEM\Setup\LabConfig /v BypassStorageCheck /t REG_DWORD /d 1 /f</Path>
        </RunSynchronousCommand>
        <RunSynchronousCommand wcm:action="add">
          <Order>5</Order>
          <Path>reg add HKLM\SYSTEM\Setup\LabConfig /v BypassCPUCheck /t REG_DWORD /d 1 /f</Path>
        </RunSynchronousCommand>
{{- if .WinPEAgent}}
        <!-- The one vetted non-reg command: probes letters with "if exist"
             (cannot fail), starts the agent detached, force-exits 0 — so it
             can never abort Setup the way an unresolved DriverPaths did. -->
        <RunSynchronousCommand wcm:action="add">
          <Order>{{.AgentLauncherOrder}}</Order>
          <Path>{{agentLauncher}}</Path>
          <Description>Start the winkit WinPE agent from the answer volume</Description>
        </RunSynchronousCommand>
{{- end}}
{{- range .WinPEDriverLoads}}
        <!-- ARM64 WinPE has no inbox vioscsi, so the virtio-scsi installer
             CD is invisible and Modern Setup parks on "media driver
             missing" (CELL-429). This runs in the last window before that
             search: WinPEInitialization executes these commands, then
             EarlyF6DriverInstall looks for media one second later (run
             20260812T150644). Same cannot-fail shape as the launcher
             above, which that log shows exiting 0x00000000. -->
        <RunSynchronousCommand wcm:action="add">
          <Order>{{.Order}}</Order>
          <Path>{{.Path}}</Path>
          <Description>{{.Description}}</Description>
        </RunSynchronousCommand>
{{- end}}
        <!-- Nothing but "reg add" (and the vetted agent launcher above) may
             run here. windowsPE has killed three multi-hour runs: misplaced
             elements fail silently, unresolved DriverPaths abort Setup
             (0x80070001 - 0x40030), and WinPE no longer ships the WMI
             command-line tool. Anything else belongs in specialize or
             FirstLogonCommands, where the full OS runs it and the
             diagnostics script can report on it. -->
      </RunSynchronous>

      <DiskConfiguration>
        <Disk wcm:action="add">
          <DiskID>0</DiskID>
          <WillWipeDisk>true</WillWipeDisk>
          <CreatePartitions>
            <CreatePartition wcm:action="add">
              <Order>1</Order>
              <Type>EFI</Type>
              <Size>256</Size>
            </CreatePartition>
            <CreatePartition wcm:action="add">
              <Order>2</Order>
              <Type>MSR</Type>
              <Size>128</Size>
            </CreatePartition>
            <CreatePartition wcm:action="add">
              <Order>3</Order>
              <Type>Primary</Type>
              <Extend>true</Extend>
            </CreatePartition>
          </CreatePartitions>
          <ModifyPartitions>
            <ModifyPartition wcm:action="add">
              <Order>1</Order>
              <PartitionID>1</PartitionID>
              <Format>FAT32</Format>
              <Label>EFI</Label>
            </ModifyPartition>
            <ModifyPartition wcm:action="add">
              <Order>2</Order>
              <PartitionID>2</PartitionID>
            </ModifyPartition>
            <ModifyPartition wcm:action="add">
              <Order>3</Order>
              <PartitionID>3</PartitionID>
              <Format>NTFS</Format>
              <Label>Windows</Label>
            </ModifyPartition>
          </ModifyPartitions>
        </Disk>
      </DiskConfiguration>

      <ImageInstall>
        <OSImage>
{{- if or .ImageName .InstallWimPath}}
          <InstallFrom>
{{- if .InstallWimPath}}
            <Path>{{.InstallWimPath}}</Path>
{{- end}}
            <MetaData wcm:action="add">
              <Key>/IMAGE/NAME</Key>
              <Value>{{.ImageName}}</Value>
            </MetaData>
          </InstallFrom>
{{- end}}
          <InstallTo>
            <DiskID>0</DiskID>
            <PartitionID>3</PartitionID>
          </InstallTo>
        </OSImage>
      </ImageInstall>

      <UserData>
        <AcceptEula>true</AcceptEula>
        <ProductKey>
          <WillShowUI>Never</WillShowUI>
        </ProductKey>
      </UserData>
    </component>
  </settings>

  <settings pass="specialize">
    <!-- RunSynchronous belongs to Microsoft-Windows-Deployment in this pass;
         Microsoft-Windows-Shell-Setup does not define it. -->
    <component name="Microsoft-Windows-Deployment"
               processorArchitecture="arm64" publicKeyToken="31bf3856ad364e35"
               language="neutral" versionScope="nonSxS"
               xmlns:wcm="http://schemas.microsoft.com/WMIConfig/2002/State">
      <!-- Microsoft removed the oobe\bypassnro script in 2025 builds, but the
           registry value it wrote still disables the "must be online with a
           Microsoft account" gate. Set here so it exists before OOBE starts. -->
      <RunSynchronous>
        <RunSynchronousCommand wcm:action="add">
          <Order>1</Order>
          <Path>reg add HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\OOBE /v BypassNRO /t REG_DWORD /d 1 /f</Path>
          <Description>Allow local account setup without network</Description>
        </RunSynchronousCommand>
{{- if .EnableRDP}}
        <RunSynchronousCommand wcm:action="add">
          <Order>2</Order>
          <Path>netsh advfirewall set allprofiles state off</Path>
          <Description>Disable firewall so forwarded ports are reachable</Description>
        </RunSynchronousCommand>
{{- end}}
{{- range $i, $d := .VirtIODrivers}}
        <!-- Stage the driver into the driver store (no /install): PnP
             auto-installs it on the next boot (OOBE). /install during
             specialize triggers PnP enumeration during Setup's own device-setup
             phase, causing a reboot loop (run 20260831T202847). -->
        <RunSynchronousCommand wcm:action="add">
          <Order>{{index $.SpecializeDriverStageOrders $i}}</Order>
          <Path>cmd /c "for %d in (C D E F G H I J K L) do @if exist %d:\{{$d.INFRelPath}} pnputil /add-driver %d:\{{$d.INFRelPath}}"</Path>
          <Description>Stage {{$d.Description}} into driver store</Description>
        </RunSynchronousCommand>
{{- end}}
        <!-- Copy the bootstrap to C:\ now, while specialize has drive letters,
             so FirstLogonCommands can launch it from a fixed path. This MUST be
             short: Setup rejects the whole answer file (0x80220005 "value is
             invalid") if a RunSynchronousCommand/Path exceeds 259 chars, which
             a PowerShell one-liner does — run 20260831T205732 died exactly here.
             A cmd for-loop stays well under the cap. -->
        <RunSynchronousCommand wcm:action="add">
          <Order>{{.SpecializeBootstrapCopyOrder}}</Order>
          <Path>cmd /c "for %d in (C D E F G H I J K L) do @if exist %d:\winkit-bootstrap.ps1 copy /y %d:\winkit-bootstrap.ps1 C:\winkit-bootstrap.ps1 >nul"</Path>
          <Description>Copy bootstrap script to C:\ for reliable first-logon discovery</Description>
        </RunSynchronousCommand>
{{- if .GosshdBinaryName}}
        <!-- gosshd is the provisioning SSH: copy it to C:\ now (specialize has
             drive letters and runs as SYSTEM) and register an onstart SYSTEM
             task, so a reachable channel exists on the first boot regardless of
             whether the first-logon bootstrap succeeds. Both commands stay well
             under the 259-char cap. It is torn down before the image ships. -->
        <RunSynchronousCommand wcm:action="add">
          <Order>{{.SpecializeGosshdCopyOrder}}</Order>
          <Path>cmd /c "for %d in (C D E F G H I J K L) do @if exist %d:\{{.GosshdBinaryName}} copy /y %d:\{{.GosshdBinaryName}} C:\{{.GosshdBinaryName}} >nul"</Path>
          <Description>Copy gosshd provisioning server to C:\</Description>
        </RunSynchronousCommand>
        <RunSynchronousCommand wcm:action="add">
          <Order>{{.SpecializeGosshdTaskOrder}}</Order>
          <Path>cmd /c schtasks /create /tn gosshd /sc onstart /ru SYSTEM /rl HIGHEST /tr "C:\{{.GosshdBinaryName}} -addr {{.GosshdListenAddrOrDefault}}{{.GosshdVsockFlags}} -shell powershell C:\gosshd.log \\.\Global\winkit.structured.0" /f</Path>
          <Description>Run gosshd provisioning server at every boot as SYSTEM</Description>
        </RunSynchronousCommand>
{{- end}}
{{- if .SMBIOSHostname}}
        <!-- Register a boot-time task that reads the SMBIOS serial number
             (set by QEMU -smbios type=1,serial=<hostname>) and renames the
             computer if it differs. Rename-Computer without -Restart just
             updates the registry; the name takes effect on next boot. -->
        <RunSynchronousCommand wcm:action="add">
          <Order>{{.SpecializeSMBIOSHostnameOrder}}</Order>
          <Path>cmd /c schtasks /create /tn winkit-hostname /sc onstart /ru SYSTEM /rl HIGHEST /tr "powershell -NoP -C $s=(gwmi Win32_BIOS).SerialNumber;if($s-and$env:COMPUTERNAME-cne$s){Rename-Computer $s -Force}" /f</Path>
          <Description>Sync hostname from SMBIOS serial at every boot</Description>
        </RunSynchronousCommand>
{{- end}}
{{- if .KeepDisplayAwake}}
        <!-- Keep the console visible for the whole install. The bootstrap step
             that does this runs only at first logon (post-OOBE) and not at all
             if the bootstrap fails early, which left screendumps black for most
             of a run. AC timeouts only; the VM is always "plugged in". -->
        <RunSynchronousCommand wcm:action="add">
          <Order>{{.SpecializeKeepDisplayAwakeOrder}}</Order>
          <Path>cmd /c "powercfg /change monitor-timeout-ac 0 &amp; powercfg /change standby-timeout-ac 0 &amp; powercfg /change disk-timeout-ac 0"</Path>
          <Description>Keep the display, disks and machine awake during install</Description>
        </RunSynchronousCommand>
{{- end}}
        <!-- Disable non-essential services before OOBE starts so they never
             consume CPU/IO during the install. Each reg add sets the service
             Start type to 4 (Disabled). Split into individual commands to stay
             under the 259-char Path limit. -->
{{- range $i, $svc := .QuietServices}}
        <RunSynchronousCommand wcm:action="add">
          <Order>{{index $.SpecializeQuietServicesOrders $i}}</Order>
          <Path>reg add HKLM\SYSTEM\CurrentControlSet\Services\{{$svc.Key}} /v Start /t REG_DWORD /d 4 /f</Path>
          <Description>Disable {{$svc.Desc}}</Description>
        </RunSynchronousCommand>
{{- end}}
        <RunSynchronousCommand wcm:action="add">
          <Order>{{index .SpecializeQuietServicesOrders (len .QuietServices)}}</Order>
          <Path>reg add HKLM\SOFTWARE\Policies\Microsoft\Windows Defender /v DisableAntiSpyware /t REG_DWORD /d 1 /f</Path>
          <Description>Defender policy: disable antispyware</Description>
        </RunSynchronousCommand>
        <RunSynchronousCommand wcm:action="add">
          <Order>{{index .SpecializeQuietServicesOrders (add (len .QuietServices) 1)}}</Order>
          <Path>reg add "HKLM\SOFTWARE\Policies\Microsoft\Windows Defender\Real-Time Protection" /v DisableRealtimeMonitoring /t REG_DWORD /d 1 /f</Path>
          <Description>Defender policy: disable realtime monitoring</Description>
        </RunSynchronousCommand>
        <RunSynchronousCommand wcm:action="add">
          <Order>{{index .SpecializeQuietServicesOrders (add (len .QuietServices) 2)}}</Order>
          <Path>reg add "HKLM\SOFTWARE\Microsoft\Windows Defender\Features" /v TamperProtection /t REG_DWORD /d 0 /f</Path>
          <Description>Defender: disable Tamper Protection</Description>
        </RunSynchronousCommand>
        <RunSynchronousCommand wcm:action="add">
          <Order>{{index .SpecializeQuietServicesOrders (add (len .QuietServices) 3)}}</Order>
          <Path>reg add HKLM\SYSTEM\CurrentControlSet\Control\EarlyLaunch /v DriverLoadPolicy /t REG_DWORD /d 7 /f</Path>
          <Description>Disable Early Launch Anti-Malware driver loading</Description>
        </RunSynchronousCommand>
        <RunSynchronousCommand wcm:action="add">
          <Order>{{index .SpecializeQuietServicesOrders (add (len .QuietServices) 4)}}</Order>
          <Path>reg add HKLM\SOFTWARE\Policies\Microsoft\Windows\WindowsUpdate\AU /v NoAutoUpdate /t REG_DWORD /d 1 /f</Path>
          <Description>Windows Update policy: disable automatic updates</Description>
        </RunSynchronousCommand>
        <RunSynchronousCommand wcm:action="add">
          <Order>{{index .SpecializeQuietServicesOrders (add (len .QuietServices) 5)}}</Order>
          <Path>reg add HKLM\SOFTWARE\Policies\Microsoft\Windows\WindowsUpdate\AU /v AUOptions /t REG_DWORD /d 1 /f</Path>
          <Description>Windows Update policy: never check for updates</Description>
        </RunSynchronousCommand>
        <RunSynchronousCommand wcm:action="add">
          <Order>{{index .SpecializeQuietServicesOrders (add (len .QuietServices) 6)}}</Order>
          <Path>reg add HKLM\SOFTWARE\Policies\Microsoft\Windows\DataCollection /v AllowTelemetry /t REG_DWORD /d 0 /f</Path>
          <Description>Telemetry policy: disable diagnostic data collection</Description>
        </RunSynchronousCommand>
{{- if .EnableWSL1Feature}}

        <!-- WSL1 needs the Microsoft-Windows-Subsystem-Linux optional feature
             (lxcore.sys); without it the version-1 wsl import exits -1. /norestart
             defers the driver load to the reboot into OOBE, so the feature is
             live before the first-logon bootstrap imports nix.wsl. Wrapped in
             exit /b 0: dism returns 3010 (restart required), which would
             otherwise abort Setup. -->
        <RunSynchronousCommand wcm:action="add">
          <Order>{{.SpecializeWSL1FeatureOrder}}</Order>
          <Path>cmd /c "dism /online /enable-feature /featurename:Microsoft-Windows-Subsystem-Linux /all /norestart &amp; exit /b 0"</Path>
          <Description>Enable the WSL1 optional feature</Description>
        </RunSynchronousCommand>
{{- end}}
      </RunSynchronous>
      <ExtendOSPartition>
        <Extend>true</Extend>
      </ExtendOSPartition>
    </component>
{{- if .EnableRDP}}

    <component name="Microsoft-Windows-TerminalServices-LocalSessionManager"
               processorArchitecture="arm64" publicKeyToken="31bf3856ad364e35"
               language="neutral" versionScope="nonSxS">
      <fDenyTSConnections>false</fDenyTSConnections>
    </component>

    <component name="Microsoft-Windows-TerminalServices-RDP-WinStationExtensions"
               processorArchitecture="arm64" publicKeyToken="31bf3856ad364e35"
               language="neutral" versionScope="nonSxS">
      <!-- NLA on: clients authenticate during connection setup (CredSSP)
           and land on the desktop. With 0 the server pre-fills its
           interactive logon form and waits for a keypress that no
           automated client sends. -->
      <UserAuthentication>1</UserAuthentication>
      <SecurityLayer>2</SecurityLayer>
    </component>
{{- end}}

    <component name="Microsoft-Windows-Shell-Setup"
               processorArchitecture="arm64" publicKeyToken="31bf3856ad364e35"
               language="neutral" versionScope="nonSxS"
               xmlns:wcm="http://schemas.microsoft.com/WMIConfig/2002/State">
      <ComputerName>{{.Hostname}}</ComputerName>
      <TimeZone>{{.TimeZone}}</TimeZone>
    </component>
  </settings>

  <settings pass="oobeSystem">
    <!-- Region defaults for the installed OS. The windowsPE component above
         only covers Setup itself; without this, OOBE can still stop on a
         region/keyboard page. -->
    <component name="Microsoft-Windows-International-Core"
               processorArchitecture="arm64" publicKeyToken="31bf3856ad364e35"
               language="neutral" versionScope="nonSxS">
      <InputLocale>{{.Locale}}</InputLocale>
      <SystemLocale>{{.Locale}}</SystemLocale>
      <UILanguage>{{.Locale}}</UILanguage>
      <UserLocale>{{.Locale}}</UserLocale>
    </component>

    <component name="Microsoft-Windows-Shell-Setup"
               processorArchitecture="arm64" publicKeyToken="31bf3856ad364e35"
               language="neutral" versionScope="nonSxS"
               xmlns:wcm="http://schemas.microsoft.com/WMIConfig/2002/State">

      <OOBE>
        <HideEULAPage>true</HideEULAPage>
        <HideOEMRegistrationScreen>true</HideOEMRegistrationScreen>
        <HideLocalAccountScreen>true</HideLocalAccountScreen>
        <HideOnlineAccountScreens>true</HideOnlineAccountScreens>
        <HideWirelessSetupInOOBE>true</HideWirelessSetupInOOBE>
        <ProtectYourPC>3</ProtectYourPC>
        <!-- Hide* hides individual screens; it does NOT skip OOBE, and the
             Zero Day Patch step is not one of the hideable screens. Without
             Skip*OOBE the install completes and then dies in OOBE with
             "Something went wrong ... OOBEZDP", because ZDP wants a network
             the guest does not have (virtio-net-pci, and Windows 11 ARM64
             ships no inbox virtio-net driver). Microsoft advises against these
             two settings; that advice does not hold here. Keep the Hide*
             screens above as well — the two mechanisms coexist. -->
        <SkipMachineOOBE>true</SkipMachineOOBE>
        <SkipUserOOBE>true</SkipUserOOBE>
      </OOBE>

      <UserAccounts>
        <LocalAccounts>
          <LocalAccount wcm:action="add">
            <Name>{{.Username}}</Name>
            <Group>Administrators</Group>
            <Password>
              <Value>{{.Password}}</Value>
              <PlainText>true</PlainText>
            </Password>
          </LocalAccount>
        </LocalAccounts>
      </UserAccounts>

      <AutoLogon>
        <Enabled>true</Enabled>
        <Username>{{.Username}}</Username>
        <Password>
          <Value>{{.Password}}</Value>
          <PlainText>true</PlainText>
        </Password>
        <LogonCount>3</LogonCount>
      </AutoLogon>

      <FirstLogonCommands>
        <!-- Drivers are staged in specialize (into the driver store, not forced)
             and auto-installed by PnP on the OOBE boot. No driver commands here.
             specialize copied the bootstrap to C:\, so launch it from the fixed
             path — kept short, same 259-char reason as above. -->
        <SynchronousCommand wcm:action="add">
          <Order>{{.FirstLogonBootstrapOrder}}</Order>
          <CommandLine>powershell -NoProfile -ExecutionPolicy Bypass -File C:\winkit-bootstrap.ps1</CommandLine>
          <Description>Run the winkit bootstrap copied to C:\ by specialize</Description>
        </SynchronousCommand>
      </FirstLogonCommands>

    </component>
  </settings>

</unattend>
`

// WriteImage creates a FAT32 disk image containing autounattend.xml
// and startup.nsh from pre-rendered XML. Use BuildAnswerVolume for real
// install volumes — it also ships the first-logon bootstrap the XML launcher
// expects; this raw-XML variant exists for validation tests.
func WriteImage(xmlBytes []byte, destPath string) error {
	return writeAnswerImage(xmlBytes, nil, nil, destPath)
}

// writeAnswerImage validates the answer file and writes it, the shared base
// files, and any extra files to a FAT image. Every payload in extra is padded
// to the volume's cluster boundary by CreateFATImagePadded — a fixed 2048 no
// longer aligns once the payload (pwsh + OpenSSH) pushes the volume past 260MB
// and go-diskfs switches to 4KB clusters. Files in exact are written
// byte-identical: driver payloads must match their catalog hashes, so they
// cannot carry padding (the round-trip verification still catches the go-diskfs
// boundary bug loudly if one of them ever lands on it).
func writeAnswerImage(xmlBytes []byte, extra, exact map[string][]byte, destPath string) error {
	// A misplaced setting is ignored silently by Windows Setup, so a bad
	// answer file only shows up hours later as an unexplained install
	// failure. Refuse to write one.
	if errs := Validate(xmlBytes); len(errs) > 0 {
		msgs := make([]string, len(errs))
		for i, err := range errs {
			msgs[i] = err.Error()
		}
		return fmt.Errorf("invalid answer file:\n  %s", strings.Join(msgs, "\n  "))
	}
	padded := map[string][]byte{
		"/autounattend.xml":              xmlBytes,
		"/startup.nsh":                   []byte(winpe.StartupNSH),
		"/" + GuestDiagnosticsScriptName: GenerateGuestDiagnosticsScript(),
	}
	for name, data := range extra {
		padded[name] = data
	}
	return isokit.CreateFATImagePadded(destPath, padded, exact)
}

// BuildAnswerVolume renders the answer file and the first-logon bootstrap
// from one config and writes the complete answer volume. This is the entry
// point for building a real install volume — the XML's FirstLogonCommands
// launcher expects the bootstrap script to ship next to it, and taking the
// config here makes it impossible to build a volume where they disagree.
func BuildAnswerVolume(cfg Config, destPath string) error {
	extra := map[string][]byte{
		"/" + BootstrapScriptName: GenerateBootstrapScript(cfg),
	}
	if cfg.OpenSSHPayload != "" && len(cfg.OpenSSHPayloadData) > 0 {
		// Windows servicing cannot install OpenSSH Server from our media, so
		// the standalone release travels with the answer file.
		extra["/"+cfg.OpenSSHPayload] = cfg.OpenSSHPayloadData
	}
	if cfg.WallpaperName != "" && len(cfg.WallpaperData) > 0 {
		extra["/"+cfg.WallpaperName] = cfg.WallpaperData
	}
	if cfg.WinPEAgent {
		// Both virtio-serial ports wired: progress for the human-readable
		// stream, structured for the Panther-log JSON tee into build.jsonl.
		// Harmless when the ports are absent (no vioserial driver): the
		// agent opens them lazily and keeps working without them.
		extra["/"+winpe.AgentScriptName] = winpe.GenerateAgent(winpe.PayloadConfig{
			ProgressPort:   `\\.\Global\` + winpe.ProgressPortName,
			StructuredPort: `\\.\Global\` + winpe.StructuredPortName,
		})
		extra["/"+winpe.AgentVolumeMarker] = []byte("winkit agent volume\r\n")
		extra["/"+winpe.DiagScriptName] = winpe.GenerateDiagScript()
		extra["/"+winpe.HyperVDiagScriptName] = winpe.GenerateHyperVDiagScript("")
		if cfg.AgentCommand != "" {
			// set /p reads the first line only, so padding after the
			// newline is harmless.
			extra["/"+winpe.AgentCommandFile] = []byte(cfg.AgentCommand + "\r\n")
		}
	}
	if len(cfg.EFIBootLoader) > 0 {
		extra["/EFI/BOOT/BOOTAA64.EFI"] = cfg.EFIBootLoader
	}
	for path, data := range cfg.PwshFiles {
		extra[path] = data
	}
	// Answer-volume drivers are only consumed by WinPE's drvload, which
	// does not verify Authenticode. Cluster-padding is safe here; the
	// installed-OS copy comes from the virtio-win CD via pnputil.
	for path, data := range cfg.AnswerDrivers {
		extra[path] = data
	}
	// Byte-exact (unpadded) files: gosshd launches byte-for-byte as built,
	// the rclone zip is read from its end, and the WinFsp MSI is signature-
	// checked — FAT cluster padding corrupts all three.
	exact := map[string][]byte{}
	if cfg.GosshdBinaryName != "" && len(cfg.GosshdBinaryData) > 0 {
		exact["/"+cfg.GosshdBinaryName] = cfg.GosshdBinaryData
	}
	if cfg.RclonePayload != "" && len(cfg.RclonePayloadData) > 0 {
		exact["/"+cfg.RclonePayload] = cfg.RclonePayloadData
	}
	if cfg.WinFspPayload != "" && len(cfg.WinFspPayloadData) > 0 {
		exact["/"+cfg.WinFspPayload] = cfg.WinFspPayloadData
	}
	if len(cfg.NixWSLPayloadData) > 0 {
		exact["/nix.wsl"] = cfg.NixWSLPayloadData
	}
	return writeAnswerImage(GenerateXML(cfg), extra, exact, destPath)
}

// WriteISO creates a small ISO image containing autounattend.xml.
//
// Deprecated: unusable for driving Windows Setup. CreateSimpleISO writes ISO
// 9660 Level 1 names, so the file lands as "AUTOUNAT.XML" and Setup — which
// searches for "autounattend.xml" and does not read the Rock Ridge extension
// that preserves the real name — silently ignores it. Use
// WriteImage; the FAT writer stores a long-filename entry.
func WriteISO(xmlBytes []byte, destPath string) error {
	return isokit.CreateSimpleISO(destPath, map[string][]byte{
		"/autounattend.xml": xmlBytes,
	})
}
