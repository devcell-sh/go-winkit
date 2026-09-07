package unattend

import (
	_ "embed"

	"github.com/devcell-sh/go-winkit/internal/templates"
)

// First-logon bootstrap.
//
// All first-logon provisioning lives in one generated PowerShell script
// shipped on the answer volume, not in inline FirstLogonCommands: a script
// file has no XML/cmd quoting hazards (a multi-line SSH key broke the inline
// form), is unit-testable, and can report its own failures. An inline
// CommandLine that fails does so silently — and silent guest failures have
// each cost a multi-hour run to notice.
//
// Failure reporting goes to two host-readable channels:
//   - the virtio-serial progress port (lands in
//     Spec.GuestProgressLogPath on the host, live)
//   - a Start-Transcript log on the answer volume, read back with
//     isokit.ReadFileFromFAT after the run
const (
	// BootstrapScriptName is the script placed on the answer volume and
	// invoked by the single FirstLogonCommands entry.
	BootstrapScriptName = "winkit-bootstrap.ps1"
	// BootstrapLogName is the transcript the script writes next to itself.
	BootstrapLogName = "winkit-bootstrap.log"
	// OpenSSHVersion pins the Win32-OpenSSH release. Win32-OpenSSH is
	// Microsoft's own signed distribution of the same code the OpenSSH.Server
	// capability installs, so this is not a third-party substitute: Windows
	// 11 24H2 ships 9.5.6.1 in System32\OpenSSH, the same generation.
	//
	// Pinned rather than tracked from `latest`, because 9.8 split sshd into
	// sshd.exe + sshd-session.exe + sshd-auth.exe. WinPE's shell runs as
	// NT AUTHORITY\SYSTEM, so sshd takes the privilege-separation path and
	// the auth child exits 0xC0000142 STATUS_DLL_INIT_FAILED, dropping the
	// connection right after KEXINIT. Before 9.8 there is one sshd.exe and
	// no such child. Tracking `latest` is what moved us onto 10.0 silently.
	OpenSSHVersion = "v9.5.0.0p1-Beta"
	// OpenSSHPayloadName is the Win32-OpenSSH release shipped on the answer
	// volume. Windows servicing cannot install OpenSSH Server from our media,
	// so the standalone build is the primary install path.
	//
	// The version is part of the name on purpose: DownloadOpenSSH treats a
	// cached file plus its .done marker as a hit, so a bump that kept the old
	// name would leave the previous archive in place and change nothing.
	OpenSSHPayloadName = "openssh-arm64-9.5.0.0p1-Beta.zip"
	// OpenSSHReleaseURL is Microsoft's signed ARM64 release.
	OpenSSHReleaseURL = "https://github.com/PowerShell/Win32-OpenSSH/releases/download/" +
		OpenSSHVersion + "/OpenSSH-ARM64.zip"

	// RcloneVersion pins the rclone release the guest mounts the SFTP share
	// with. Mount support for windows/arm64 exists since v1.59 (cgo-free
	// cgofuse); v1.75.0 is what the CELL-532 interactive bring-up proved.
	RcloneVersion = "v1.75.0"
	// RclonePayloadName is the rclone release zip shipped on the answer
	// volume. Versioned name on purpose: the download cache treats file +
	// .done marker as a hit, so a version bump must change the name.
	RclonePayloadName = "rclone-" + RcloneVersion + "-windows-arm64.zip"
	// RcloneReleaseURL is the official windows/arm64 build.
	RcloneReleaseURL = "https://downloads.rclone.org/" + RcloneVersion +
		"/rclone-" + RcloneVersion + "-windows-arm64.zip"

	// WinFspVersion pins the WinFsp release (ARM64-capable since 2.0).
	WinFspVersion = "2.0.23075"
	// WinFspPayloadName is the WinFsp MSI shipped on the answer volume.
	WinFspPayloadName = "winfsp-" + WinFspVersion + ".msi"
	// WinFspReleaseURL is the signed WinFsp installer.
	WinFspReleaseURL = "https://github.com/winfsp/winfsp/releases/download/v2.0/winfsp-" +
		WinFspVersion + ".msi"

	// RcloneMountTaskName is the scheduled task that runs the rclone mount.
	// The shape is load-bearing (CELL-532): launched from an SSH session,
	// rclone dies with the session and the WinFsp drive letter is per-logon;
	// as an on-start SYSTEM task the mount survives sessions, remounts on
	// every boot, and the drive is visible globally — including to WSL1
	// drvfs, the whole point of the fixed-disk mount.
	RcloneMountTaskName = "winkit-rclone-mount"

	// DefaultGuestHostIP is the host address under QEMU user-mode
	// networking (slirp).
	DefaultGuestHostIP = "10.0.2.2"
	// DefaultSFTPUser / DefaultSFTPPassword / DefaultSFTPVolumeName /
	// DefaultSFTPDrive fill the share fields when only the port is set.
	// Shared with build callers that need the resolved values before
	// render time (e.g. to bake wsl.RcloneMountService).
	DefaultSFTPUser       = "winkit"
	DefaultSFTPPassword   = "winkit"
	DefaultSFTPVolumeName = "winkit"
	DefaultSFTPDrive      = "W"
)

// GenerateBootstrapScript renders the first-logon bootstrap for a config.
func GenerateBootstrapScript(cfg Config) []byte {
	if cfg.GuestHostIP == "" {
		cfg.GuestHostIP = DefaultGuestHostIP
	}
	if cfg.SFTPPort > 0 {
		if cfg.SFTPUser == "" {
			cfg.SFTPUser = DefaultSFTPUser
		}
		if cfg.SFTPPassword == "" {
			cfg.SFTPPassword = DefaultSFTPPassword
		}
		if cfg.SFTPVolumeName == "" {
			cfg.SFTPVolumeName = DefaultSFTPVolumeName
		}
		if cfg.SFTPDrive == "" {
			cfg.SFTPDrive = DefaultSFTPDrive
		}
	}
	return []byte(templates.Render("bootstrap.ps1.tmpl", cfg))
}
