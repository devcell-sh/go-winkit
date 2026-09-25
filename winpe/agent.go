package winpe

import (
	"fmt"
	"strings"

	"github.com/devcell-sh/go-winkit/internal/templates"
)

// AgentLauncherLog is the diagnostic log the windowsPE RunSynchronous
// launcher writes to the answer volume. It records which drive letter
// was found, whether pwsh.exe and the agent script exist, and whether
// the agent was started. Readable from the host after the build.
const AgentLauncherLog = `winkit-launcher.log`

// WinPE payload layout. These files are baked into boot.wim so they exist on
// the WinPE RAM drive (X:) before setup.exe starts.
const (
	// WinPEPayloadDir is where the winkit payload lives inside boot.wim.
	WinPEPayloadDir = `X:\winkit`
	// WinPEBootstrapCmdPath is the cmd.exe shim that winpeshl.ini calls.
	// Stock WinPE lacks powershell.exe; this shim probes volumes for pwsh.exe
	// (PowerShell 7, xcopy-deployed on the answer volume) and launches the
	// real bootstrap.ps1 through it.
	WinPEBootstrapCmdPath = `X:\winkit\bootstrap.cmd`
	// WinPEBootstrapPath is the PowerShell bootstrap, launched by the cmd shim.
	WinPEBootstrapPath = `X:\winkit\bootstrap.ps1`
	// WinPEAgentPath is the control agent, started detached by the bootstrap.
	WinPEAgentPath = `X:\winkit\agent.ps1`

	// PwshVolDir is the directory on the answer volume containing PowerShell 7.
	// Stock WinPE has no PowerShell; pwsh.exe is self-contained and
	// xcopy-deployed from the official GitHub release zip.
	PwshVolDir = `pwsh`

	// AgentVolumeMarker identifies the removable volume carrying the command
	// and result files. WinPE drive letters are not stable, so the agent
	// searches for this file instead of assuming a letter.
	AgentVolumeMarker = `winkit-agent.marker`
	// AgentCommandFile holds a single command line for the agent to run.
	AgentCommandFile = `winkit-cmd.txt`
	// AgentResultFile receives that command's combined output.
	AgentResultFile = `winkit-out.txt`
	// AgentDoneFile is written after the command finishes. The host polls for
	// this instead of AgentResultFile to avoid reading a half-written output
	// file (the redirect flushes incrementally, so the file appears non-empty
	// before diskpart/PowerShell finishes writing).
	AgentDoneFile = `winkit-done.marker`

	// AgentScriptName is the agent's filename on the answer volume — the
	// no-rebake deployment path: a windowsPE RunSynchronous launcher starts
	// it straight off the volume (AgentLauncherCommand), so boot.wim
	// never has to be modified.
	AgentScriptName = `winkit-agent.ps1`
	// SetupActSnapshotName receives the agent's periodic copy of WinPE's
	// X:\Windows\Panther\setupact.log, which otherwise dies with the RAM
	// disk (CELL-364). The x- prefix distinguishes it from the C:\ target
	// disk variant written during the second-pass install phase.
	SetupActSnapshotName = `x-winkit-setupact.log`
	// SetupErrSnapshotName receives setuperr.log the same way.
	SetupErrSnapshotName = `x-winkit-setuperr.log`
	// SetupAPISnapshotName receives X:\Windows\INF\setupapi.dev.log, PnP's
	// full driver-binding trace. drvload.exe has no verbose switch, so this
	// is the only way to see why a driver did or did not bind.
	SetupAPISnapshotName = `x-winkit-setupapi.dev.log`
	// Target disk (C:\) snapshots for the second-pass install phase.
	SetupActTargetSnapshotName = `c-winkit-setupact.log`
	SetupErrTargetSnapshotName = `c-winkit-setuperr.log`

)

// AgentLauncherCommand returns the one non-registry command allowed in
// windowsPE RunSynchronous. Anything that can fail there aborts Setup
// (0x80070001 - 0x40030, run 20260729T172019), so the whole block is
// wrapped in exit /b 0 to guarantee a zero exit code. The agent is started
// detached via "start /min" so Setup is never blocked.
//
// Uses cmd.exe because stock WinPE lacks powershell.exe. The answer volume
// carries pwsh.exe (PowerShell 7) which the agent needs at runtime.
// The actual logic lives in AgentLauncherScript (winkit-launch.cmd on the
// answer volume); this one-liner just finds and calls it.
func AgentLauncherCommand() string {
	return `cmd.exe /c "for %l in (C D E F G H I J K L) do @if exist %l:\` + AgentLauncherScript + ` %l:\` + AgentLauncherScript + ` %l: & exit /b 0"`
}

// AgentLauncherScript is the cmd batch file shipped on the answer volume.
// The windowsPE RunSynchronous command finds and calls it; the script does
// the heavy lifting (diagnostics, guards, starting the agent).
const AgentLauncherScript = `winkit-launch.cmd`

// GenerateAgentLauncherScript produces the batch file that the windowsPE
// RunSynchronous one-liner calls. It logs diagnostics to AgentLauncherLog,
// verifies pwsh.exe and the agent script exist, and starts the agent
// detached. Every path is guarded and logged so a silent failure is
// diagnosable from the answer volume after the build.
func GenerateAgentLauncherScript() []byte {
	// %1 is the drive letter passed by AgentLauncherCommand (e.g. "E:").
	return []byte("@echo off\r\n" +
		"set _WK=%1\r\n" +
		"set _LOG=%_WK%\\" + AgentLauncherLog + "\r\n" +
		"echo %date% %time% launcher: volume=%_WK% >>%_LOG%\r\n" +
		"if not exist %_WK%\\" + PwshVolDir + "\\pwsh.exe (\r\n" +
		"  echo %date% %time% launcher: FAIL pwsh not found at %_WK%\\" + PwshVolDir + "\\pwsh.exe >>%_LOG%\r\n" +
		"  dir %_WK%\\ >>%_LOG% 2>&1\r\n" +
		"  exit /b 0\r\n" +
		")\r\n" +
		"echo %date% %time% launcher: pwsh OK >>%_LOG%\r\n" +
		"if not exist %_WK%\\" + AgentScriptName + " (\r\n" +
		"  echo %date% %time% launcher: FAIL agent script not found at %_WK%\\" + AgentScriptName + " >>%_LOG%\r\n" +
		"  dir %_WK%\\ >>%_LOG% 2>&1\r\n" +
		"  exit /b 0\r\n" +
		")\r\n" +
		"echo %date% %time% launcher: agent script OK >>%_LOG%\r\n" +
		"echo %date% %time% launcher: starting agent >>%_LOG%\r\n" +
		"start /min %_WK%\\" + PwshVolDir + "\\pwsh.exe -ExecutionPolicy Bypass -File %_WK%\\" + AgentScriptName + " %_WK%\r\n" +
		"echo %date% %time% launcher: agent started >>%_LOG%\r\n" +
		"exit /b 0\r\n")
}

// PEAgentLauncherCommand returns the windowsPE RunSynchronous one-liner
// that finds winkit-service.exe on a removable volume and starts the
// native pe-agent detached. Replaces the PS1 agent: no pwsh dependency.
// The exit /b 0 wrapper guarantees a zero exit code so Setup is never aborted.
func PEAgentLauncherCommand(serviceBinaryName string) string {
	return `cmd.exe /c "for %l in (C D E F G H I J K L) do @if exist %l:\` + serviceBinaryName +
		` start /min %l:\` + serviceBinaryName +
		` run --name pe-agent` +
		`" & exit /b 0`
}

// GeneratePEAgentLauncherScript produces the batch file equivalent for
// the PE standalone path (boot.wim injection). The bootstrap.cmd calls
// this to start the pe-agent from the answer volume.
func GeneratePEAgentLauncherScript(serviceBinaryName string) []byte {
	return []byte("@echo off\r\n" +
		"set _WK=%1\r\n" +
		"set _LOG=%_WK%\\" + AgentLauncherLog + "\r\n" +
		"echo %date% %time% launcher: volume=%_WK% >>%_LOG%\r\n" +
		"if not exist %_WK%\\" + serviceBinaryName + " (\r\n" +
		"  echo %date% %time% launcher: FAIL " + serviceBinaryName + " not found >>%_LOG%\r\n" +
		"  dir %_WK%\\ >>%_LOG% 2>&1\r\n" +
		"  exit /b 0\r\n" +
		")\r\n" +
		"echo %date% %time% launcher: starting pe-agent >>%_LOG%\r\n" +
		"start /min %_WK%\\" + serviceBinaryName + " run --name pe-agent\r\n" +
		"echo %date% %time% launcher: pe-agent started >>%_LOG%\r\n" +
		"exit /b 0\r\n")
}

// DriverLoadCommand returns a windowsPE RunSynchronous command that
// drvloads one INF from whatever drive letter the answer volume received.
//
// This is the last hook before Modern Setup searches for install media:
// run 20260812T150644 logged "WinPEInitialization: Leaving Execute Method"
// and "EarlyF6DriverInstall: Entering Execute Method" one second apart, in
// that order. The agent's poll loop is too late: its drvload landed after
// the media search had already failed (0x80070103, run 20260812T143146).
//
// Uses cmd.exe because stock WinPE lacks powershell.exe. drvload.exe is a
// WinPE native tool. Wrapped in exit /b 0 so a broken driver degrades
// gracefully instead of aborting Setup.
func DriverLoadCommand(inf string) string {
	return `cmd.exe /c "for %l in (C D E F G H I J K L) do @if exist %l:\` + inf + ` drvload.exe %l:\` + inf + `" & exit /b 0`
}

// DiagCommand is the one-shot diagnostic the agent executes when a
// build ships it as AgentCommand; its combined output lands in
// winkit-out.txt on the answer volume. Strictly read-only: the first
// version drvloaded vioscsi and collided with wpeinit's own $WinPEDriver$
// load — Setup aborted 0x80070103 ERROR_NO_MORE_ITEMS, run 20260812T143146.
//
// Deprecated: prefer DiagScriptCommand, which invokes the proper
// diagnostics script and waits for completion before the output is read.
const DiagCommand = `Set-Content X:\winkit-lv.txt "list volume` + "`r`n" + `exit"; & diskpart.exe /s X:\winkit-lv.txt; & reg.exe query HKLM\SYSTEM\CurrentControlSet\Services\vioscsi; Get-ChildItem X:\Windows\Panther, X:\$windows.~bt\Sources\Panther -ErrorAction SilentlyContinue`

const (
	// DiagScriptName is the diagnostics script shipped on the answer
	// volume. It follows the same structured-output pattern as
	// GenerateGuestDiagnosticsScript (guest_diagnostics.go) but runs in
	// WinPE under PowerShell.
	DiagScriptName = `winkit-winpe-diag.ps1`
)

// DiagScriptCommand returns the agent command that invokes the
// diagnostics script. The agent runs this via Invoke-Expression in
// PowerShell, so $WinkitVol is expanded from the agent's scope.
func DiagScriptCommand() string {
	return `& "$WinkitVol\` + DiagScriptName + `" $WinkitVol`
}

// GenerateDiagScript produces the WinPE diagnostics script. It is
// shipped on the answer volume and invoked by the agent. Output goes to
// stdout (the agent redirects it to AgentResultFile).
//
// Three sections:
//  1. Disk/volume enumeration
//  2. CIM/PowerShell probes
//  3. Script access: can we see other winkit scripts on the answer volume
func GenerateDiagScript() []byte {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference = 'Continue'\r\n")
	b.WriteString("$Vol = $args[0]\r\n")
	b.WriteString("\r\n")
	b.WriteString("Write-Output '=== WINKIT WINPE DIAGNOSTICS ==='\r\n")
	b.WriteString("Write-Output \"$(Get-Date)\"\r\n")
	b.WriteString("Write-Output \"Volume: $Vol\"\r\n")
	b.WriteString("Write-Output ''\r\n")

	// ── 0. CPU / PROCESSOR CAPABILITIES
	b.WriteString("\r\n")
	b.WriteString("Write-Output '=== PROCESSOR INFO ==='\r\n")
	b.WriteString("Write-Output \"PROCESSOR_ARCHITECTURE=$env:PROCESSOR_ARCHITECTURE\"\r\n")
	b.WriteString("Write-Output \"PROCESSOR_IDENTIFIER=$env:PROCESSOR_IDENTIFIER\"\r\n")
	b.WriteString("Write-Output \"PROCESSOR_LEVEL=$env:PROCESSOR_LEVEL\"\r\n")
	b.WriteString("Write-Output \"PROCESSOR_REVISION=$env:PROCESSOR_REVISION\"\r\n")
	b.WriteString("Write-Output \"NUMBER_OF_PROCESSORS=$env:NUMBER_OF_PROCESSORS\"\r\n")
	b.WriteString("Write-Output ''\r\n")

	b.WriteString("\r\n")
	b.WriteString("Write-Output '=== CPU REGISTRY ==='\r\n")
	b.WriteString("& reg.exe query 'HKLM\\HARDWARE\\DESCRIPTION\\System\\CentralProcessor\\0' 2>$null\r\n")
	b.WriteString("Write-Output ''\r\n")

	for _, item := range []struct{ section, wmic string }{
		{"WMIC CPU (full)", "cpu"},
		{"WMIC COMPUTERSYSTEM", "computersystem"},
		{"WMIC BASEBOARD", "baseboard"},
		{"WMIC BIOS", "bios"},
		{"WMIC MEMORYCHIP", "memorychip"},
		{"WMIC OS", "os"},
	} {
		b.WriteString("\r\n")
		fmt.Fprintf(&b, "Write-Output '=== %s ==='\r\n", item.section)
		fmt.Fprintf(&b, "try { & wmic.exe %s get /format:list 2>$null } catch { Write-Output 'wmic %s: not available' }\r\n", item.wmic, item.wmic)
		b.WriteString("Write-Output ''\r\n")
	}

	b.WriteString("\r\n")
	b.WriteString("Write-Output '=== SYSTEMINFO ==='\r\n")
	b.WriteString("try { & systeminfo.exe 2>$null } catch { Write-Output 'systeminfo: not available' }\r\n")
	b.WriteString("Write-Output ''\r\n")

	// ── 1. DISK CHECKS
	b.WriteString("\r\n")
	b.WriteString("Write-Output '=== DISKPART VOLUMES ==='\r\n")
	b.WriteString("Set-Content X:\\winkit-lv.txt \"list volume`r`nexit\"\r\n")
	b.WriteString("& diskpart.exe /s X:\\winkit-lv.txt\r\n")
	b.WriteString("Write-Output ''\r\n")

	b.WriteString("\r\n")
	b.WriteString("Write-Output '=== DISKPART DISKS ==='\r\n")
	b.WriteString("Set-Content X:\\winkit-ld.txt \"list disk`r`nexit\"\r\n")
	b.WriteString("& diskpart.exe /s X:\\winkit-ld.txt\r\n")
	b.WriteString("Write-Output ''\r\n")

	b.WriteString("\r\n")
	b.WriteString("Write-Output '=== WMIC LOGICALDISK ==='\r\n")
	b.WriteString("try { & wmic.exe logicaldisk get caption,description,filesystem,volumename,size 2>$null } catch { Write-Output 'wmic: not available' }\r\n")
	b.WriteString("Write-Output ''\r\n")

	b.WriteString("\r\n")
	b.WriteString("Write-Output '=== STORAGE DRIVERS ==='\r\n")
	for _, drv := range []string{"USBSTOR", "vioscsi", "viostor", "storahci"} {
		fmt.Fprintf(&b, "Write-Output '-- %s:'\r\n", drv)
		fmt.Fprintf(&b, "& reg.exe query 'HKLM\\SYSTEM\\CurrentControlSet\\Services\\%s' /v Start 2>$null\r\n", drv)
		fmt.Fprintf(&b, "if ($LASTEXITCODE -ne 0) { Write-Output '  not loaded' }\r\n")
	}
	b.WriteString("Write-Output ''\r\n")

	// ── 2. POWERSHELL PROBES (already running in PS, so just call directly)
	b.WriteString("\r\n")
	b.WriteString("Write-Output '=== POWERSHELL AVAILABILITY ==='\r\n")
	b.WriteString("Write-Output 'powershell.exe: found'\r\n")
	b.WriteString("Write-Output ''\r\n")

	b.WriteString("Write-Output '=== POWERSHELL VERSION ==='\r\n")
	b.WriteString("$PSVersionTable | Format-List\r\n")
	b.WriteString("Write-Output ''\r\n")

	b.WriteString("Write-Output '=== POWERSHELL ADMIN CHECK ==='\r\n")
	b.WriteString("Write-Output ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)\r\n")
	b.WriteString("Write-Output ''\r\n")

	b.WriteString("Write-Output '=== POWERSHELL GET-VOLUME ==='\r\n")
	b.WriteString("try { Get-Volume | Format-Table DriveLetter, FileSystemLabel, DriveType, FileSystem, @{N='SizeGB';E={[math]::Round($_.Size/1GB,1)}} -AutoSize | Out-String -Width 200 } catch { Write-Output 'Get-Volume: not available' }\r\n")
	b.WriteString("Write-Output ''\r\n")

	b.WriteString("Write-Output '=== POWERSHELL GET-DISK ==='\r\n")
	b.WriteString("try { Get-Disk | Format-Table Number, FriendlyName, BusType, Size, PartitionStyle -AutoSize | Out-String -Width 200 } catch { Write-Output 'Get-Disk: not available' }\r\n")
	b.WriteString("Write-Output ''\r\n")

	b.WriteString("Write-Output '=== POWERSHELL CPU (full) ==='\r\n")
	b.WriteString("try { Get-CimInstance Win32_Processor | Format-List * } catch { Write-Output 'Get-CimInstance: not available' }\r\n")
	b.WriteString("Write-Output ''\r\n")

	b.WriteString("Write-Output '=== POWERSHELL COMPUTERSYSTEM ==='\r\n")
	b.WriteString("try { Get-CimInstance Win32_ComputerSystem | Format-List * } catch { Write-Output 'Get-CimInstance: not available' }\r\n")
	b.WriteString("Write-Output ''\r\n")

	// ── 3. SCRIPT ACCESS
	b.WriteString("\r\n")
	b.WriteString("Write-Output '=== ANSWER VOLUME CONTENTS ==='\r\n")
	b.WriteString("if (-not $Vol) {\r\n")
	b.WriteString("    Write-Output 'VOL not set, skipping'\r\n")
	b.WriteString("} else {\r\n")
	b.WriteString("    Get-ChildItem \"$Vol\\\" -ErrorAction SilentlyContinue\r\n")
	b.WriteString("    Write-Output ''\r\n")
	b.WriteString("    Write-Output '=== WINKIT SCRIPTS ==='\r\n")
	// The bootstrap name is a literal rather than unattend.BootstrapScriptName:
	// unattend imports winpe, so importing it back here would be a cycle.
	fmt.Fprintf(&b, "    foreach ($f in @('%s','%s','winkit-bootstrap.ps1','autounattend.xml')) {\r\n",
		AgentScriptName, AgentVolumeMarker)
	b.WriteString("        if (Test-Path \"$Vol\\$f\") {\r\n")
	b.WriteString("            Write-Output \"[OK]    $Vol\\$f\"\r\n")
	b.WriteString("        } else {\r\n")
	b.WriteString("            Write-Output \"[MISS]  $Vol\\$f\"\r\n")
	b.WriteString("        }\r\n")
	b.WriteString("    }\r\n")
	b.WriteString("    Write-Output ''\r\n")
	b.WriteString("}\r\n")

	b.WriteString("\r\n")
	b.WriteString("Write-Output '=== PANTHER LOGS ==='\r\n")
	b.WriteString("Get-ChildItem X:\\Windows\\Panther -ErrorAction SilentlyContinue\r\n")
	b.WriteString("Write-Output '---'\r\n")
	b.WriteString("Get-ChildItem 'X:\\$windows.~bt\\Sources\\Panther' -ErrorAction SilentlyContinue\r\n")
	b.WriteString("Write-Output ''\r\n")

	b.WriteString("\r\n")
	b.WriteString("Write-Output '=== WINKIT DIAGNOSTICS COMPLETE ==='\r\n")

	return []byte(b.String())
}

const (
	// EchoProbeScriptName is the filename for the COM-port echo
	// probe + virtiofs write test script.
	EchoProbeScriptName = `winkit-winpe-echo-probe.ps1`
)

// DiagToolPaths returns the WIM-internal paths of System32 binaries
// to extract from install.wim and inject into boot.wim. Stock WinPE
// lacks these; injecting them gives the diagnostics script real service
// management and process visibility.
func DiagToolPaths() []string {
	return []string{
		`\Windows\System32\sc.exe`,
		`\Windows\System32\tasklist.exe`,
		`\Windows\System32\wevtutil.exe`,
	}
}

// PayloadConfig parameterises the generated WinPE payload scripts.
type PayloadConfig struct {
	// DriverINFs are loaded with drvload before setup.exe starts. Usually
	// empty: NVMe and USB storage have inbox Windows ARM64 drivers, so
	// injection is only needed for extras like virtio-net.
	DriverINFs []string
	// SerialPort is the guest device path for the unified COM2 serial port.
	// Carries both progress markers and structured JSON (build.jsonl feed).
	// Uses the inbox serial.sys driver (pci-serial 16550), available in all
	// three phases (WinPE, second-pass Setup, desktop) without drvload.
	SerialPort string
	// WPEInit causes the bootstrap to call wpeinit before anything else.
	// Required when booting WinPE standalone (no setup.exe) — without it,
	// serial ports and other hardware are not initialized.
	WPEInit bool
	// PollSeconds is how often the agent checks for a new command (default 5).
	PollSeconds int
	// SyncAgent causes the bootstrap to run the agent synchronously (blocking)
	// instead of detached. Required when booting WinPE standalone (no
	// setup.exe): without it, winpeshl.ini returns after bootstrap.cmd and
	// WinPE reboots immediately.
	SyncAgent bool
}

// GenerateShellINI produces winpeshl.ini, which replaces WinPE's default
// startup. Entries run in order and synchronously, so the bootstrap is listed
// first and setup.exe second — dropping setup.exe here would leave WinPE with
// nothing to do after the bootstrap returns.
//
// The bootstrap is a cmd.exe script because stock WinPE lacks powershell.exe.
// The shim probes volumes for pwsh.exe and launches the real PS1 bootstrap.
func GenerateShellINI() []byte {
	return []byte("[LaunchApps]\r\n" +
		WinPEBootstrapCmdPath + "\r\n" +
		`%SYSTEMDRIVE%\setup.exe` + "\r\n")
}

// GenerateShellINI_NoSetup produces winpeshl.ini that runs ONLY the
// bootstrap — no setup.exe. Used when booting WinPE standalone (CELL-430).
func GenerateShellINI_NoSetup() []byte {
	return []byte("[LaunchApps]\r\n" +
		WinPEBootstrapCmdPath + "\r\n")
}

// GenerateBootstrapCmd produces the cmd.exe shim that winpeshl.ini calls.
// Stock WinPE has cmd.exe but not powershell.exe. This shim probes removable
// volumes for pwsh.exe (PowerShell 7, xcopy-deployed on the answer volume)
// and launches the real PowerShell bootstrap through it.
func GenerateBootstrapCmd() []byte {
	return []byte("@for %%d in (C D E F G H I J K L M N O P Q R S T U V W Y Z) do " +
		"@if exist %%d:\\" + PwshVolDir + "\\pwsh.exe " +
		"%%d:\\" + PwshVolDir + "\\pwsh.exe -ExecutionPolicy Bypass -File " + WinPEBootstrapPath + " & goto :eof\r\n")
}

// GenerateBootstrap produces a PowerShell script that runs before
// setup.exe: initializes WinPE, loads requested drivers, opens the
// serial port for progress, and launches the agent.
//
// Launched by the cmd.exe shim via pwsh.exe (PowerShell 7). Uses $PSHOME
// to locate the same pwsh.exe binary for spawning the agent.
func GenerateBootstrap(cfg PayloadConfig) []byte {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference = 'Continue'\r\n")

	if cfg.WPEInit {
		b.WriteString("& wpeinit\r\n")
	}

	// Load drivers before anything else. Exit codes are collected here and
	// reported below rather than printed as we go.
	if len(cfg.DriverINFs) > 0 {
		b.WriteString("$drvload = @()\r\n")
	}
	for _, inf := range cfg.DriverINFs {
		fmt.Fprintf(&b, "& drvload.exe '%s'\r\n", inf)
		fmt.Fprintf(&b, "$drvload += \"%s exit=$LASTEXITCODE\"\r\n", inf)
	}

	b.WriteString(psProgressLine(cfg, "bootstrap-start"))

	// 0x80070103 is ERROR_NO_MORE_ITEMS: the driver is already bound, which
	// is success wearing the costume of a failure. Reporting the code makes
	// that distinguishable from a driver that genuinely did not load.
	if len(cfg.DriverINFs) > 0 {
		if line := psProgressLine(cfg, "drvload $d"); line != "" {
			b.WriteString("foreach ($d in $drvload) {\r\n")
			b.WriteString("    " + line)
			b.WriteString("}\r\n")
		}
	}

	if cfg.SyncAgent {
		fmt.Fprintf(&b, "& \"$PSHOME\\pwsh.exe\" -ExecutionPolicy Bypass -File '%s'\r\n", WinPEAgentPath)
	} else {
		fmt.Fprintf(&b, "Start-Process -WindowStyle Minimized \"$PSHOME\\pwsh.exe\" "+
			"'-ExecutionPolicy Bypass -File %s'\r\n", WinPEAgentPath)
	}
	b.WriteString(psProgressLine(cfg, "agent-started"))
	return []byte(b.String())
}

// GenerateAgent produces a PowerShell control agent: a poll loop that
// snapshots Setup's logs onto the winkit volume and runs one command at a
// time, streaming output through Tee-Object to both the result file and the
// virtio-serial progress port.
//
// This exists because there is no qemu-guest-agent build for Windows ARM64
// (virtio-win ships only i386/x86_64 MSIs), so QMP guest-exec is unavailable.
// The command file lives on the removable FAT image the host also writes, and
// needs no drivers beyond inbox usbstor.
func GenerateAgent(cfg PayloadConfig) []byte {
	poll := cfg.PollSeconds
	if poll <= 0 {
		poll = 5
	}

	data := struct {
		SerialPort       string
		VolumeMarker     string
		CommandFile      string
		ResultFile       string
		DoneFile         string
		SetupActSnapshot string
		SetupErrSnapshot string
		SetupAPISnapshot string
		PollSeconds      int
	}{
		SerialPort:       cfg.SerialPort,
		VolumeMarker:     AgentVolumeMarker,
		CommandFile:      AgentCommandFile,
		ResultFile:       AgentResultFile,
		DoneFile:         AgentDoneFile,
		SetupActSnapshot: SetupActSnapshotName,
		SetupErrSnapshot: SetupErrSnapshotName,
		SetupAPISnapshot: SetupAPISnapshotName,
		PollSeconds:      poll,
	}

	out := templates.Render("winpe-agent.ps1.tmpl", data)
	out = strings.ReplaceAll(out, "\n", "\r\n")
	return []byte(out)
}

// EchoProbeScriptCommand returns the agent command that invokes the
// COM-port echo probe script.
func EchoProbeScriptCommand() string {
	return `& "$WinkitVol\` + EchoProbeScriptName + `" $WinkitVol`
}

// GenerateEchoProbeScript produces a WinPE PowerShell script that:
//  1. Probes COM1 through COM4, echoing a unique marker to each port so the
//     host can determine which serial device maps to PCI-serial on ARM64.
//  2. Loads the viofs driver via drvload and mounts a virtiofs share using
//     virtiofs.exe, then writes a test file to the mount point.
//
// The answer volume path is passed as $args[0].
// viofs driver files and virtiofs.exe are expected under $Vol\drivers\viofs\.
// The virtiofs tag must match Spec.VirtioFSTag (default "winkit-logs").
func GenerateEchoProbeScript(viofsTag string) []byte {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference = 'Continue'\r\n")
	b.WriteString("$Vol = $args[0]\r\n")
	b.WriteString("\r\n")

	// Section 1: COM port probe
	b.WriteString("Write-Output '===== COM PORT PROBE ====='\r\n")
	for i := 1; i <= 4; i++ {
		marker := fmt.Sprintf("WINKIT_COM_ECHO_COM%d", i)
		fmt.Fprintf(&b, "try {\r\n")
		fmt.Fprintf(&b, "    [System.IO.File]::WriteAllText('COM%d', '%s')\r\n", i, marker)
		fmt.Fprintf(&b, "    Write-Output 'COM%d: OK'\r\n", i)
		fmt.Fprintf(&b, "} catch {\r\n")
		fmt.Fprintf(&b, "    Write-Output 'COM%d: FAILED'\r\n", i)
		fmt.Fprintf(&b, "}\r\n")
	}
	b.WriteString("Write-Output '===== COM PROBE DONE ====='\r\n")
	b.WriteString("\r\n")

	// Section 2: viofs driver load + virtiofs mount
	b.WriteString("Write-Output '===== VIOFS MOUNT ====='\r\n")

	b.WriteString("if (Test-Path \"$Vol\\drivers\\viofs\\viofs.inf\") {\r\n")
	b.WriteString("    & drvload.exe \"$Vol\\drivers\\viofs\\viofs.inf\"\r\n")
	b.WriteString("    Write-Output \"drvload viofs: $LASTEXITCODE\"\r\n")
	b.WriteString("} else {\r\n")
	b.WriteString("    Write-Output 'viofs.inf not found — skipping driver load'\r\n")
	b.WriteString("}\r\n")

	// Wait a moment for PnP to settle
	b.WriteString("Start-Sleep -Seconds 3\r\n")

	mountLetter := "V:"
	b.WriteString("if (Test-Path \"$Vol\\drivers\\viofs\\virtiofs.exe\") {\r\n")
	fmt.Fprintf(&b, "    & \"$Vol\\drivers\\viofs\\virtiofs.exe\" mount -t %s %s\r\n", viofsTag, mountLetter)
	b.WriteString("    if ($LASTEXITCODE -ne 0) {\r\n")
	b.WriteString("        Write-Output 'virtiofs mount: FAILED'\r\n")
	b.WriteString("    } else {\r\n")
	b.WriteString("        Write-Output 'virtiofs mount: OK'\r\n")
	fmt.Fprintf(&b, "        Set-Content '%s\\viofs-probe.txt' 'WINKIT_VIOFS_HELLO'\r\n", mountLetter)
	fmt.Fprintf(&b, "        if (Test-Path '%s\\viofs-probe.txt') {\r\n", mountLetter)
	b.WriteString("            Write-Output 'viofs write: OK'\r\n")
	b.WriteString("        } else {\r\n")
	b.WriteString("            Write-Output 'viofs write: FAILED'\r\n")
	b.WriteString("        }\r\n")
	b.WriteString("    }\r\n")
	b.WriteString("} else {\r\n")
	b.WriteString("    Write-Output 'virtiofs.exe not found — skipping mount'\r\n")
	b.WriteString("}\r\n")
	b.WriteString("Write-Output '===== VIOFS DONE ====='\r\n")
	b.WriteString("\r\n")

	b.WriteString("Write-Output 'WINKIT ECHO PROBE COMPLETE'\r\n")

	// go-diskfs v1.9.4 records the cluster-rounded size in the directory
	// entry instead of the actual file size, so reads return trailing
	// garbage for any file not cluster-aligned. Pad to a 512-byte boundary.
	if rem := b.Len() % 512; rem != 0 {
		b.WriteString("# ")
		for b.Len()%512 != 0 {
			b.WriteByte('.')
		}
	}

	return []byte(b.String())
}

// psProgressLine emits a PowerShell line that writes to the serial port.
func psProgressLine(cfg PayloadConfig, msg string) string {
	if cfg.SerialPort == "" {
		return ""
	}
	return fmt.Sprintf("\"winkit: %s\" | Out-File -Append '%s' -Encoding utf8\r\n", msg, cfg.SerialPort)
}
