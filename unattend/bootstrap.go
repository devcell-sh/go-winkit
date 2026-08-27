package unattend

import (
	_ "embed"

	"github.com/devcell-sh/go-winkit/templates"
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
	BootstrapScriptName = "devcell-bootstrap.ps1"
	// BootstrapLogName is the transcript the script writes next to itself.
	BootstrapLogName = "devcell-bootstrap.log"
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
)

// GenerateBootstrapScript renders the first-logon bootstrap for a config.
func GenerateBootstrapScript(cfg Config) []byte {
	return []byte(templates.Render("bootstrap.ps1.tmpl", cfg))
}
