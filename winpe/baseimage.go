package winpe

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/devcell-sh/go-winkit/wim"
)

// GosshdShellCmdName is the cmd.exe shell script that boots the base image
// into gosshd. GosshdLogName is gosshd's log on the WinPE ramdisk.
const (
	GosshdShellCmdName = "gosshd.cmd"
	GosshdLogName      = "gosshd.log"

	// HypervisorEnableCmdName is an optional one-time first-boot hook. The
	// base image never ships it (the `if exist ... call` below is a no-op);
	// the hpv build injects it to enable the hypervisor via bcdedit on first
	// boot, because the offline BCD write is not honored by winload.
	HypervisorEnableCmdName = "hvenable.cmd"

	// GosshdStructuredPort is the guest path of the virtio-serial structured
	// port gosshd emits per-session records to. It mirrors the device name
	// wired by the qemu package (winkit.structured.0, backed by build.jsonl);
	// gosshd tolerates its absence, so passing it unconditionally is safe even
	// on a boot where the port was not wired.
	GosshdStructuredPort = `\\.\Global\winkit.structured.0`
)

// BaseImageConfig describes a standalone bootable WinPE "base" image:
// virtio drivers (storage, serial, network) plus gosshd as the shell.
type BaseImageConfig struct {
	// WindowsISO is the installer providing the WinPE stage.
	WindowsISO string
	// VirtIOISO is the virtio-win driver ISO.
	VirtIOISO string
	// GosshdExe is a windows cross-compiled gosshd (CrossCompileGosshd).
	GosshdExe string
	// PwshFiles are the extracted PowerShell 7 files (from ExtractPwshFiles).
	// When set, they are injected into boot.wim so pwsh.exe is available at
	// X:\winkit\pwsh\pwsh.exe inside WinPE.
	PwshFiles map[string][]byte
	// WorkDir holds the stage and inject trees; a temp dir when empty.
	WorkDir string
}

// BuildBaseImageFiles produces the FAT-volume file map for a self-contained
// bootable base image. The caller packs it into whatever their hypervisor
// boots (e.g. qemu.CreateFATQcow2). The boot chain: firmware boots
// \EFI\BOOT\BOOTAA64.EFI from the volume, bootmgr loads the injected
// boot.wim, winpeshl.ini runs gosshd.cmd — wpeinit, drvload the virtio
// drivers, drop the firewall, serve SSH in the foreground so the guest
// stays up.
func BuildBaseImageFiles(cfg BaseImageConfig) (map[string][]byte, error) {
	if cfg.GosshdExe == "" {
		return nil, fmt.Errorf("base image requires a gosshd binary (CrossCompileGosshd)")
	}
	workDir := cfg.WorkDir
	if workDir == "" {
		dir, err := os.MkdirTemp("", "winkit-baseimage-*")
		if err != nil {
			return nil, fmt.Errorf("creating work dir: %w", err)
		}
		defer os.RemoveAll(dir)
		workDir = dir
	}

	stageDir := filepath.Join(workDir, "stage")
	if err := ExtractStage(cfg.WindowsISO, stageDir); err != nil {
		return nil, fmt.Errorf("extracting WinPE stage: %w", err)
	}

	drivers := make(map[string][]byte)
	for _, load := range []func(string) (map[string][]byte, error){
		LoadWinPEStorageDrivers,
		LoadWinPEVioserialDrivers,
		LoadWinPENetKVMDrivers,
	} {
		m, err := load(cfg.VirtIOISO)
		if err != nil {
			return nil, err
		}
		for k, v := range m {
			drivers[k] = v
		}
	}

	injectDir := filepath.Join(workDir, "inject")
	if err := os.MkdirAll(injectDir, 0o755); err != nil {
		return nil, err
	}
	for volPath, data := range drivers {
		hostPath := filepath.Join(injectDir, filepath.FromSlash(strings.TrimPrefix(volPath, "/")))
		if err := os.MkdirAll(filepath.Dir(hostPath), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(hostPath, data, 0o644); err != nil {
			return nil, err
		}
	}

	gosshd, err := os.ReadFile(cfg.GosshdExe)
	if err != nil {
		return nil, fmt.Errorf("reading gosshd payload: %w", err)
	}

	var infs []string
	for volPath := range drivers {
		if strings.HasSuffix(volPath, ".inf") {
			infs = append(infs, `X:\winkit`+strings.ReplaceAll(volPath, "/", `\`))
		}
	}

	payload := map[string][]byte{
		"winpeshl.ini":     []byte("[LaunchApps]\r\n" + `X:\winkit\` + GosshdShellCmdName + "\r\n"),
		GosshdShellCmdName: GenerateGosshdShellCmd(infs),
		GosshdVolumeName:   gosshd,
	}
	for name, data := range payload {
		if err := os.WriteFile(filepath.Join(injectDir, name), data, 0o644); err != nil {
			return nil, fmt.Errorf("writing %s: %w", name, err)
		}
	}

	for volPath, data := range cfg.PwshFiles {
		hostPath := filepath.Join(injectDir, filepath.FromSlash(strings.TrimPrefix(volPath, "/")))
		if err := os.MkdirAll(filepath.Dir(hostPath), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(hostPath, data, 0o644); err != nil {
			return nil, fmt.Errorf("writing pwsh %s: %w", volPath, err)
		}
	}

	bootWimPath := filepath.Join(stageDir, "sources", "boot.wim")
	if err := wim.InjectWinPEPayload(bootWimPath, injectDir); err != nil {
		return nil, fmt.Errorf("injecting WinPE payload: %w", err)
	}

	injected, err := os.ReadFile(bootWimPath)
	if err != nil {
		return nil, fmt.Errorf("reading injected boot.wim: %w", err)
	}
	return BootVolumeFiles(stageDir, injected)
}

// GenerateGosshdShellCmd produces the cmd.exe shell for the base image:
// initialise WinPE, load the injected virtio drivers, open the firewall,
// and run gosshd in the foreground — standalone WinPE reboots the moment
// winpeshl's apps return, so the server blocking is what keeps the guest up.
func GenerateGosshdShellCmd(driverINFs []string) []byte {
	var b strings.Builder
	b.WriteString("@echo off\r\n")
	b.WriteString("wpeinit\r\n")
	for _, inf := range driverINFs {
		b.WriteString("drvload " + inf + "\r\n")
	}
	// Optional first-boot hook (hpv build only): wpeinit above has assigned
	// drive letters, so the hook can bcdedit the boot volume's BCD store and
	// reboot. No-op on the base image, which never ships the script.
	b.WriteString("if exist X:\\winkit\\" + HypervisorEnableCmdName +
		" call X:\\winkit\\" + HypervisorEnableCmdName + "\r\n")
	b.WriteString("if exist X:\\winkit\\pwsh\\pwsh.exe set PATH=X:\\winkit\\pwsh;%PATH%\r\n")
	// Start the Event Log service so in-guest diagnostics — notably the
	// Microsoft-Windows-Hyper-V-Hypervisor "hypervisor launched" event in the
	// System log — are queryable via wevtutil. WinPE ships the service
	// (Start=auto) but never actually starts it. pwsh is the one shell we can
	// rely on here (net.exe/sc.exe are not always present).
	b.WriteString("if exist X:\\winkit\\pwsh\\pwsh.exe X:\\winkit\\pwsh\\pwsh.exe " +
		"-NoProfile -Command \"Start-Service EventLog -ErrorAction SilentlyContinue\"\r\n")
	b.WriteString("wpeutil DisableFirewall\r\n")
	b.WriteString(`X:\winkit\` + GosshdVolumeName + ` X:\winkit\` + GosshdLogName +
		" " + GosshdStructuredPort + "\r\n")
	return []byte(b.String())
}
