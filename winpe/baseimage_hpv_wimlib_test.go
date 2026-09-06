//go:build wimlib

package winpe

import (
	"os"
	"path/filepath"
	"testing"

	regedit "github.com/devcell-sh/go-regedit"
)

// enableHypervisorInBCD must set hypervisorlaunchtype=Auto and MUST leave
// the WinPE flag alone. Clearing BcdOSLoaderBoolean_WinPE faults winload on
// this aarch64 ramdisk WinPE (QEMU exits at ~38s, verified), while an
// in-guest launch-type edit engages the hypervisor with WinPE=1 intact.
// This guards against re-introducing the boot-breaking ClearWinPEFlag call.
func TestEnableHypervisorInBCD_SetsLaunchTypeAndPreservesWinPE(t *testing.T) {
	src := filepath.Join("testdata", "base-boot-bcd")
	data, err := os.ReadFile(src)
	if err != nil {
		t.Skipf("no base-boot-bcd fixture: %v", err)
	}
	bcd := filepath.Join(t.TempDir(), "bcd")
	if err := os.WriteFile(bcd, data, 0o644); err != nil {
		t.Fatal(err)
	}

	loader := `Objects\` + regedit.WinPELoaderGUID + `\Elements\`

	// Precondition: the stock loader entry has WinPE=1.
	before, err := regedit.ReadServiceKey(bcd, loader+regedit.ElementWinPE)
	if err != nil {
		t.Fatalf("fixture missing WinPE element: %v", err)
	}
	if got := before.Values["Element"].Data; len(got) != 1 || got[0] != 0x01 {
		t.Fatalf("fixture WinPE flag = %x, want 01 (stock WinPE media)", got)
	}

	if err := enableHypervisorInBCD(bcd); err != nil {
		t.Fatalf("enableHypervisorInBCD: %v", err)
	}

	// hypervisorlaunchtype = Auto, 8-byte little-endian.
	hv, err := regedit.ReadServiceKey(bcd, loader+regedit.ElementHypervisorLaunchType)
	if err != nil {
		t.Fatalf("reading hypervisorlaunchtype: %v", err)
	}
	if got, want := hv.Values["Element"].Data, []byte{1, 0, 0, 0, 0, 0, 0, 0}; !equalBytes(got, want) {
		t.Errorf("hypervisorlaunchtype = %x, want %x (Auto)", got, want)
	}

	// WinPE flag must be UNCHANGED (still 1) — clearing it breaks the boot.
	wp, err := regedit.ReadServiceKey(bcd, loader+regedit.ElementWinPE)
	if err != nil {
		t.Fatalf("reading WinPE flag: %v", err)
	}
	if got := wp.Values["Element"].Data; len(got) != 1 || got[0] != 0x01 {
		t.Errorf("WinPE flag = %x, want 01 (must be left intact)", got)
	}
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
