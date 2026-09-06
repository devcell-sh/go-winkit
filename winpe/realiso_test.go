package winpe

import (
	"os"
	"testing"

	"github.com/devcell-sh/go-winkit/cache"
	"github.com/devcell-sh/go-winkit/isokit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// windowsISOPath returns the cached Windows installer, skipping when the
// cache has not been seeded. Seed it with `task test:seed`.
func windowsISOPath(t *testing.T) string {
	t.Helper()
	p := cache.WindowsISO()
	if _, err := os.Stat(p); err != nil {
		t.Skipf("Windows ISO not available at %s (run: task test:seed)", p)
	}
	return p
}

func TestISOPreflight_RealWindowsISO(t *testing.T) {
	isoPath := windowsISOPath(t)
	info, err := ISOPreflight(isoPath)
	require.NoError(t, err)
	assert.True(t, info.HasBootEFI, "should find BOOTAA64.EFI in ISO metadata")
	assert.True(t, info.Size > 1<<30, "Windows ISO should be >1GB")
	t.Logf("format=%s size=%d hasBootEFI=%v", info.Format, info.Size, info.HasBootEFI)
}

func TestLoadWinPEStorageDrivers_RealVirtioISO(t *testing.T) {
	isoPath := cache.VirtIOISO()
	if _, err := os.Stat(isoPath); err != nil {
		t.Skipf("virtio-win ISO not available at %s (run: task test:seed)", isoPath)
	}

	drivers, err := LoadWinPEStorageDrivers(isoPath)
	require.NoError(t, err, "real virtio-win ISO must yield the ARM64 vioscsi driver")
	assert.True(t, len(drivers["/drivers/vioscsi/vioscsi.sys"]) > 10000, "vioscsi.sys should be a real driver binary")
	assert.Contains(t, string(drivers["/drivers/vioscsi/vioscsi.inf"]), "vioscsi", "INF should reference the driver")
}

func TestWindowsISOBootable_RealWindowsISO(t *testing.T) {
	isoPath := windowsISOPath(t)
	assert.NoError(t, WindowsISOBootable(isoPath),
		"a valid cached installer must pass the cache-reuse check")
}

func TestISOExtractAndValidate_RealWindowsISO(t *testing.T) {
	isoPath := windowsISOPath(t)

	data, err := isokit.ReadFileFromISO(isoPath, "/EFI/BOOT/BOOTAA64.EFI")
	require.NoError(t, err, "should extract BOOTAA64.EFI from Windows ISO")

	info, err := ValidateBootloaderPE(data)
	require.NoError(t, err, "extracted bootloader should be a valid aarch64 PE")
	assert.Equal(t, "aarch64", info.Arch)
	t.Logf("bootloader: arch=%s size=%d", info.Arch, info.Size)
}
