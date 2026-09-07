package winpe

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/devcell-sh/go-winkit/isokit"
)

// GosshdVolumeName is gosshd's filename on the shared volume.
const GosshdVolumeName = "gosshd.exe"

// BuildConfig describes what to bake into a bootable WinPE artifact.
type BuildConfig struct {
	WindowsISO string
	VirtIOISO  string
	PwshFiles  map[string][]byte
	OutputDir  string

	HyperV  bool
	WSL2    bool
	OpenSSH bool
	VirtIO  bool

	// GosshdExe is a cross-compiled gosshd binary (CrossCompileGosshd) to
	// ship on the shared volume as GosshdVolumeName. Win32-OpenSSH cannot
	// serve sessions in WinPE, so this is the base image's SSH server.
	GosshdExe string

	ProgressPort string
}

// BuildResult holds paths to the artifacts the caller needs to boot.
type BuildResult struct {
	WinPEISO    string
	SharedFiles map[string][]byte
}

// Build produces a bootable WinPE ISO and a shared file map. The caller
// packs SharedFiles into whatever volume format their hypervisor needs
// (e.g. FAT qcow2 for QEMU, VHD for Hyper-V).
func Build(cfg BuildConfig) (*BuildResult, error) {
	if cfg.OutputDir == "" {
		dir, err := os.MkdirTemp("", "winkit-build-*")
		if err != nil {
			return nil, fmt.Errorf("creating temp dir: %w", err)
		}
		cfg.OutputDir = dir
	}

	stageDir := filepath.Join(cfg.OutputDir, "stage")
	if err := ExtractStage(cfg.WindowsISO, stageDir); err != nil {
		return nil, fmt.Errorf("extracting WinPE stage: %w", err)
	}

	vioserialDrivers, err := LoadWinPEVioserialDrivers(cfg.VirtIOISO)
	if err != nil {
		return nil, fmt.Errorf("loading vioserial drivers: %w", err)
	}
	vioscsiDrivers, err := LoadWinPEStorageDrivers(cfg.VirtIOISO)
	if err != nil {
		return nil, fmt.Errorf("loading vioscsi drivers: %w", err)
	}

	bootWimPath := filepath.Join(stageDir, "sources", "boot.wim")
	bootWimData, err := os.ReadFile(bootWimPath)
	if err != nil {
		return nil, fmt.Errorf("reading boot.wim: %w", err)
	}

	var efiBootLoader []byte
	if bl, err := InstallerBootloader(cfg.WindowsISO); err == nil {
		if _, err := ValidateBootloaderPE(bl); err == nil {
			efiBootLoader = bl
		}
	}

	shared := cfg.sharedFiles(efiBootLoader)
	shared["/boot.wim"] = bootWimData

	injectDir := filepath.Join(cfg.OutputDir, "inject")
	if err := os.MkdirAll(injectDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating inject dir: %w", err)
	}

	allDrivers := mergeFileMaps(vioserialDrivers, vioscsiDrivers)
	for answerPath, data := range allDrivers {
		hostPath := filepath.Join(injectDir, filepath.FromSlash(answerPath))
		if err := os.MkdirAll(filepath.Dir(hostPath), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(hostPath, data, 0o644); err != nil {
			return nil, err
		}
	}

	pc := cfg.payloadConfig(vioserialDrivers, vioscsiDrivers)
	for name, data := range payloadFiles(pc) {
		if err := os.WriteFile(filepath.Join(injectDir, name), data, 0o644); err != nil {
			return nil, fmt.Errorf("writing %s: %w", name, err)
		}
	}

	if err := InjectWinPEPayload(bootWimPath, injectDir); err != nil {
		return nil, fmt.Errorf("injecting WinPE payload: %w", err)
	}

	// The FAT volume is the boot device (Linux-hosted EDK2 crashes executing
	// the CD's El Torito image), so it must be a complete boot disk.
	// "/boot.wim" stays the clean pre-injection copy — that's the DISM
	// servicing source, not the boot media.
	injectedWim, err := os.ReadFile(bootWimPath)
	if err != nil {
		return nil, fmt.Errorf("reading injected boot.wim: %w", err)
	}
	bootFiles, err := BootVolumeFiles(stageDir, injectedWim)
	if err != nil {
		return nil, err
	}
	for path, data := range bootFiles {
		shared[path] = data
	}

	if cfg.GosshdExe != "" {
		data, err := os.ReadFile(cfg.GosshdExe)
		if err != nil {
			return nil, fmt.Errorf("reading gosshd payload: %w", err)
		}
		shared["/"+GosshdVolumeName] = data
	}

	winpeISO := filepath.Join(cfg.OutputDir, "winpe-builder.iso")
	if err := isokit.CreateWindowsISO(winpeISO, stageDir, "WINPE"); err != nil {
		return nil, fmt.Errorf("creating WinPE ISO: %w", err)
	}

	return &BuildResult{
		WinPEISO:    winpeISO,
		SharedFiles: shared,
	}, nil
}

// BootVolumeFiles returns the files that make a FAT volume a complete
// standalone WinPE boot disk: the EFI bootloader, BCD stores, boot.sdi, and
// the boot.wim at the ramdisk path the stock BCD references
// ([boot]\sources\boot.wim). Firmware boots \EFI\BOOT\BOOTAA64.EFI from the
// volume directly — no optical media involved, which matters because
// Linux-hosted EDK2 crashes executing genisoimage-mastered El Torito images.
func BootVolumeFiles(stageDir string, bootWim []byte) (map[string][]byte, error) {
	files := map[string][]byte{
		"/sources/boot.wim": bootWim,
	}
	for volPath, stageRel := range map[string]string{
		"/EFI/BOOT/BOOTAA64.EFI":  filepath.Join("efi", "boot", "bootaa64.efi"),
		"/EFI/Microsoft/Boot/BCD": filepath.Join("efi", "microsoft", "boot", "bcd"),
		"/boot/bcd":               filepath.Join("boot", "bcd"),
		"/boot/boot.sdi":          filepath.Join("boot", "boot.sdi"),
	} {
		data, err := os.ReadFile(filepath.Join(stageDir, stageRel))
		if err != nil {
			return nil, fmt.Errorf("reading stage %s for boot volume: %w", stageRel, err)
		}
		files[volPath] = data
	}
	return files, nil
}

// BuildSetupBootVolumeFiles assembles a FAT boot volume that ramdisk-boots the
// retail Windows Setup — the stock \sources\boot.wim from the install ISO,
// UNMODIFIED (no gosshd/WinPE injection). Firmware boots \EFI\BOOT\BOOTAA64.EFI
// from this volume (bootindex=1) and the stock BCD loads boot.wim, launching
// Setup, which then reads \sources\install.wim from the still-attached Windows
// ISO. This is the same FAT-boot method the base/WIM-builder paths use, because
// booting the ISO's El Torito image directly ASSERTs on Linux-hosted EDK2.
func BuildSetupBootVolumeFiles(winISO, workDir string) (map[string][]byte, error) {
	stageDir := filepath.Join(workDir, "setup-stage")
	if err := ExtractStage(winISO, stageDir); err != nil {
		return nil, fmt.Errorf("extracting Setup boot stage: %w", err)
	}
	bootWim, err := os.ReadFile(filepath.Join(stageDir, "sources", "boot.wim"))
	if err != nil {
		return nil, fmt.Errorf("reading Setup boot.wim: %w", err)
	}
	return BootVolumeFiles(stageDir, bootWim)
}

func (c BuildConfig) wimPrepOps() []WimPrepOp {
	var ops []WimPrepOp
	if c.HyperV {
		ops = append(ops, HyperVPrepOps()...)
	}
	if c.WSL2 {
		ops = append(ops, WSL2PrepOps()...)
	}
	if c.OpenSSH {
		ops = append(ops, OpenSSHPrepOps()...)
	}
	if c.VirtIO {
		ops = append(ops, VirtIODriverPrepOps()...)
	}
	return ops
}

func (c BuildConfig) sharedFiles(efiBootLoader []byte) map[string][]byte {
	wmcfg := WimPrepConfig{Ops: c.wimPrepOps()}
	return SharedVolumeFiles(wmcfg, efiBootLoader, c.PwshFiles)
}

// payloadFiles returns the boot chain injected into boot.wim. winpeshl.ini
// launches bootstrap.cmd — a cmd.exe shim, because stock WinPE has no
// PowerShell — which probes volumes for pwsh.exe and hands off to
// bootstrap.ps1.
func payloadFiles(pc PayloadConfig) map[string][]byte {
	return map[string][]byte{
		"winpeshl.ini":  GenerateShellINI_NoSetup(),
		"bootstrap.cmd": GenerateBootstrapCmd(),
		"bootstrap.ps1": GenerateBootstrap(pc),
		"agent.ps1":     GenerateAgent(pc),
	}
}

func (c BuildConfig) payloadConfig(vioserialDrivers, vioscsiDrivers map[string][]byte) PayloadConfig {
	pc := PayloadConfig{
		WPEInit:      true,
		ProgressPort: c.ProgressPort,
		PollSeconds:  5,
		SyncAgent:    true,
	}
	var infs []string
	if len(vioserialDrivers) > 0 {
		infs = append(infs, `X:\winkit\drivers\vioserial\vioser.inf`)
	}
	if len(vioscsiDrivers) > 0 {
		infs = append(infs, `X:\winkit\drivers\vioscsi\vioscsi.inf`)
	}
	pc.DriverINFs = infs
	return pc
}

func mergeFileMaps(maps ...map[string][]byte) map[string][]byte {
	out := make(map[string][]byte)
	for _, m := range maps {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}
