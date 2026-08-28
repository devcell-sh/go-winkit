package wim

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/devcell-sh/go-regedit"
)

// PatchStagedBCD sets the boot flags the transplanted VMP/Hyper-V stack needs
// in every BCD store found under a boot-media stage directory, and returns how
// many stores it patched.
//
// Without hypervisorlaunchtype=Auto the transplanted drivers are present and
// registered but winload never starts the hypervisor, so the stack is inert.
// The remaining flags (NxPolicy, prerelease signatures, integrity checks,
// VSMLaunchType) let a Dev-signed, VBS-free WinPE load them.
//
// This has to happen on the host: a booted WinPE runs from a ramdisk and
// cannot open the BCD store it came from — bcdedit fails there with "the boot
// configuration data store could not be opened".
func PatchStagedBCD(stageDir string) (int, error) {
	var patched int
	for _, rel := range []string{
		filepath.Join("efi", "microsoft", "boot", "bcd"),
		filepath.Join("boot", "bcd"),
	} {
		bcd := filepath.Join(stageDir, rel)
		if _, err := os.Stat(bcd); err != nil {
			continue
		}
		if err := regedit.SetHypervisorLaunchType(bcd, regedit.HypervisorLaunchAuto); err != nil {
			return patched, fmt.Errorf("hypervisorlaunchtype in %s: %w", rel, err)
		}
		for _, el := range []struct {
			name  string
			id    string
			value uint64
		}{
			{"NxPolicy=AlwaysOn", "25000020", 3},
			{"VSMLaunchType=Off", "250000e3", 0},
		} {
			if err := regedit.SetBCDIntegerElement(bcd, regedit.WinPELoaderGUID, el.id, el.value); err != nil {
				return patched, fmt.Errorf("setting %s in %s: %w", el.name, rel, err)
			}
		}
		for _, el := range []struct {
			name string
			id   string
		}{
			{"AllowPrereleaseSignatures", "16000049"},
			{"DisableIntegrityChecks", "16000009"},
		} {
			if err := regedit.SetBCDBooleanElement(bcd, regedit.WinPELoaderGUID, el.id, true); err != nil {
				return patched, fmt.Errorf("setting %s in %s: %w", el.name, rel, err)
			}
		}
		patched++
	}
	if patched == 0 {
		return 0, fmt.Errorf("no BCD store found under %s", stageDir)
	}
	return patched, nil
}
