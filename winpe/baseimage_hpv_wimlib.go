//go:build wimlib

package winpe

import (
	"fmt"
	"os"
	"path/filepath"

	regedit "github.com/devcell-sh/go-regedit"
	"github.com/devcell-sh/go-wimlib"
)

// hpvBCDPaths are the boot volume's BCD stores that must launch the
// hypervisor. Both are patched so the firmware picks up the flag whichever it
// reads (the FAT volume boots via \boot\bcd in practice).
var hpvBCDPaths = []string{
	"/EFI/Microsoft/Boot/BCD",
	"/boot/bcd",
}

// BuildHPVImageFiles builds the base image, transplants VMP into its boot.wim,
// and sets hypervisorlaunchtype=Auto in the BCD — all offline. The returned
// map is a complete bootable FAT volume ready for CreateFATQcow2.
func BuildHPVImageFiles(cfg HPVImageConfig) (map[string][]byte, error) {
	if cfg.InstallWim == "" {
		return nil, fmt.Errorf("hpv image requires a donor install.wim")
	}

	files, err := BuildBaseImageFiles(cfg.BaseImageConfig)
	if err != nil {
		return nil, fmt.Errorf("building base image: %w", err)
	}

	work, err := os.MkdirTemp("", "winkit-hpv-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)

	// 1. Transplant VMP into boot.wim.
	bootWim := files["/sources/boot.wim"]
	if bootWim == nil {
		return nil, fmt.Errorf("base image missing /sources/boot.wim")
	}
	bootWimPath := filepath.Join(work, "boot.wim")
	if err := os.WriteFile(bootWimPath, bootWim, 0o644); err != nil {
		return nil, err
	}
	regPath := filepath.Join(work, "vmp-services.reg")
	if err := os.WriteFile(regPath, VMPServicesRegExport(), 0o644); err != nil {
		return nil, err
	}
	if err := TransplantVMPIntoBootWim(bootWimPath, cfg.InstallWim, regPath); err != nil {
		return nil, fmt.Errorf("transplanting VMP: %w", err)
	}

	// 1b. Inject the one-time first-boot hypervisor-enable hook at
	// X:\winkit\hvenable.cmd. The offline BCD write below is not honored by
	// winload; this hook rewrites the flag with bcdedit in-guest and reboots
	// once (see GenerateHypervisorEnableCmd). It sits next to gosshd in the
	// existing \winkit tree of boot.wim's WinPE image (image 2). Skipped for
	// the TransplantOnly control image (hypervisor stays disabled).
	if !cfg.TransplantOnly {
		hookFile := filepath.Join(work, HypervisorEnableCmdName)
		if err := os.WriteFile(hookFile, GenerateHypervisorEnableCmd(), 0o644); err != nil {
			return nil, err
		}
		if err := injectFileIntoBootWim(bootWimPath, hookFile,
			`\winkit\`+HypervisorEnableCmdName); err != nil {
			return nil, fmt.Errorf("injecting hypervisor-enable hook: %w", err)
		}
	}

	transplanted, err := os.ReadFile(bootWimPath)
	if err != nil {
		return nil, err
	}
	files["/sources/boot.wim"] = transplanted

	// 2. Set hypervisorlaunchtype=Auto in the BCD stores, offline. Skipped for
	// the TransplantOnly control (hypervisor must stay disabled).
	for _, key := range hpvBCDPaths {
		if cfg.TransplantOnly {
			break
		}
		bcd := files[key]
		if bcd == nil {
			return nil, fmt.Errorf("base image missing BCD %s", key)
		}
		bcdPath := filepath.Join(work, filepath.Base(key)+"-store")
		if err := os.WriteFile(bcdPath, bcd, 0o644); err != nil {
			return nil, err
		}
		if err := enableHypervisorInBCD(bcdPath); err != nil {
			return nil, fmt.Errorf("enabling hypervisor in %s: %w", key, err)
		}
		patched, err := os.ReadFile(bcdPath)
		if err != nil {
			return nil, err
		}
		files[key] = patched
	}

	return files, nil
}

// injectFileIntoBootWim adds a single host file to boot.wim's WinPE image
// (image 2) at wimDest, overwriting the WIM in place. Used for the first-boot
// hypervisor-enable hook; the heavier wim.InjectWinPEPayload assumes a full
// agent payload (winpeshl.ini + a \winkit tree) we do not want to disturb.
func injectFileIntoBootWim(bootWimPath, hostFile, wimDest string) error {
	w, err := wimlib.OpenWIM(bootWimPath)
	if err != nil {
		return fmt.Errorf("opening boot.wim: %w", err)
	}
	defer w.Close()
	if err := w.UpdateImageAdd(2, hostFile, wimDest); err != nil {
		return fmt.Errorf("adding %s: %w", wimDest, err)
	}
	if err := w.Overwrite(); err != nil {
		return fmt.Errorf("overwriting boot.wim: %w", err)
	}
	return nil
}

// enableHypervisorInBCD sets hypervisorlaunchtype=Auto (250000f0) on the
// boot store.
//
// NOTE: we deliberately do NOT clear BcdOSLoaderBoolean_WinPE (26000022).
// On this aarch64 ramdisk WinPE, WinPE=1 is required to boot at all —
// clearing it faults winload during bootmgfw→winload and QEMU exits at
// ~38s (verified). An in-guest `bcdedit /set {default} hypervisorlaunchtype
// Auto` + reboot engages the hypervisor with WinPE=1 left intact, so the
// flag does not gate the hypervisor at runtime the way go-regedit's
// ClearWinPEFlag comment claims.
func enableHypervisorInBCD(bcdPath string) error {
	if err := regedit.SetHypervisorLaunchType(bcdPath, regedit.HypervisorLaunchAuto); err != nil {
		return fmt.Errorf("setting hypervisorlaunchtype: %w", err)
	}
	return nil
}
