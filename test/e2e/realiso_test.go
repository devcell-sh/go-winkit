package e2e

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/devcell-sh/go-winkit/isokit"
)

// windowsISOPath locates a real Windows ARM64 ISO to test against: the
// DEVCELL_TEST_WINISO env var first, then the devcell cache location.
func windowsISOPath(t *testing.T) string {
	t.Helper()
	candidates := []string{
		os.Getenv("DEVCELL_TEST_WINISO"),
		os.ExpandEnv("$HOME/.devcell/cache/qemu/windows-arm64-en-us.iso"),
	}
	for _, p := range candidates {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	t.Skip("Windows ISO not available (set DEVCELL_TEST_WINISO)")
	return ""
}

func TestDiagnoseISO_RealWindowsISO(t *testing.T) {
	isoPath := windowsISOPath(t)
	diag := isokit.DiagnoseISO(isoPath)
	t.Logf("ISO diagnosis:\n%s", diag)

	assert.Contains(t, diag, "CD001", "should find ISO 9660 descriptors")
	assert.Contains(t, diag, "BOOTAA64.EFI string found", "should find BOOTAA64.EFI in raw scan")
}

func TestReadFileFromISO_RealWindowsISO(t *testing.T) {
	isoPath := windowsISOPath(t)

	data, err := isokit.ReadFileFromISO(isoPath, "/EFI/BOOT/BOOTAA64.EFI")
	require.NoError(t, err, "ReadFileFromISO should extract BOOTAA64.EFI")
	assert.True(t, len(data) > 1000, "BOOTAA64.EFI should be substantial (got %d bytes)", len(data))
	assert.Equal(t, byte('M'), data[0])
	assert.Equal(t, byte('Z'), data[1])

	require.NoError(t, isokit.RequireEFIBootable(isoPath), "real Windows ISO must be EFI bootable")
}
