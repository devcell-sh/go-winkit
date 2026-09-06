//go:build wimlib

package winpe

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/devcell-sh/go-winkit/cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stockInstallWim is a cached MCT install.wim with VirtualMachinePlatform
// DISABLED (the normal shipping state). The whole point of the WinSxS
// fallback is to source VMP binaries from such an image without prior DISM.
func stockInstallWim(t *testing.T) string {
	t.Helper()
	p := filepath.Join(cache.Dir(), "mct-work", "iso-stage", "sources", "install.wim")
	if _, err := os.Stat(p); err != nil {
		t.Skipf("stock install.wim not cached at %s (run: winkit build qemu-image)", p)
	}
	return p
}

// Every VMP service binary must resolve from a stock (VMP-disabled)
// install.wim: inbox drivers from System32, VMP-only binaries from their
// WinSxS component dir. Each extracted file must be a real PE. This is the
// offline proof of the fix — no VM boot.
func TestExtractTransplantFiles_ResolvesFromStockWinSxS(t *testing.T) {
	wim := stockInstallWim(t)
	dest := t.TempDir()

	services := VMPTransplantServices()
	require.NoError(t, ExtractTransplantFiles(wim, services, dest),
		"all VMP service binaries must resolve from stock install.wim (System32 or WinSxS)")

	for _, svc := range services {
		p := filepath.Join(dest, filepath.FromSlash(svc.File))
		info, err := os.Stat(p)
		require.NoError(t, err, "%s (%s) must be staged", svc.Name, svc.File)
		assert.Greater(t, info.Size(), int64(0), "%s must be non-empty", svc.Name)

		data, err := os.ReadFile(p)
		require.NoError(t, err)
		require.GreaterOrEqual(t, len(data), 2)
		assert.Equal(t, "MZ", string(data[:2]),
			"%s must be a PE image after any DCS decompression", svc.Name)
	}
}

// The full parity payload (vmwp.exe, the HCS client DLLs, the hypervisor
// binaries) must also resolve from a stock install.wim: inbox files from
// System32 (hvaa64.exe/hvloader.dll), VMP-only files from their WinSxS
// component. Binaries must be PEs; INF/MOF text files pass through. Offline,
// no boot.
func TestExtractParityFiles_ResolvesFromStockWinSxS(t *testing.T) {
	wim := stockInstallWim(t)
	dest := t.TempDir()

	parity := append(VMPParityFiles(), VMMSExtraFiles()...)
	require.NoError(t, ExtractParityFiles(wim, parity, dest),
		"all VMP parity files must resolve from stock install.wim")

	for _, f := range parity {
		p := filepath.Join(dest, filepath.FromSlash(f.Dest))
		info, err := os.Stat(p)
		require.NoError(t, err, "%s must be staged", f.Dest)
		assert.Greater(t, info.Size(), int64(0), "%s must be non-empty", f.Dest)

		if ext := filepath.Ext(f.Dest); ext == ".exe" || ext == ".dll" || ext == ".sys" {
			data, err := os.ReadFile(p)
			require.NoError(t, err)
			require.GreaterOrEqual(t, len(data), 2)
			assert.Equal(t, "MZ", string(data[:2]),
				"%s must be a PE after any DCS decompression", f.Dest)
		}
	}
}
