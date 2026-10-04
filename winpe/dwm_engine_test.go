package winpe

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDWMSystem32Paths_ContainsDWMCore(t *testing.T) {
	paths := DWMSystem32Paths
	require.NotEmpty(t, paths)

	set := make(map[string]bool, len(paths))
	for _, p := range paths {
		assert.True(t, strings.HasPrefix(p, `\Windows\System32\`),
			"%s must be a WIM path under \\Windows\\System32\\", p)
		base := p[strings.LastIndex(p, `\`)+1:]
		set[base] = true
	}

	for _, required := range []string{
		"dwm.exe", "dwmcore.dll", "dcomp.dll",
		"d3d11.dll", "dxgi.dll", "d3d10warp.dll",
		"d2d1.dll", "DWrite.dll",
		"uDWM.dll", "Microsoft.Internal.WarpPal.dll",
		"mf.dll", "mfplat.dll",
	} {
		assert.True(t, set[required], "%s is required for DWM compositor", required)
	}
}

func TestDWMSystem32Paths_NoBootWimDuplicates(t *testing.T) {
	// dwmapi.dll is already in boot.wim; we must NOT overwrite the
	// existing stub because our transplant adds the full compositor
	// alongside it.
	for _, p := range DWMSystem32Paths {
		assert.NotEqual(t, `\Windows\System32\dwmapi.dll`, p,
			"dwmapi.dll is already in boot.wim, do not transplant it")
	}
}

func TestDWMSystem32Paths_NoDuplicates(t *testing.T) {
	seen := make(map[string]bool, len(DWMSystem32Paths))
	for _, p := range DWMSystem32Paths {
		assert.False(t, seen[p], "duplicate path: %s", p)
		seen[p] = true
	}
}

func TestDWMWinSxSKeywords_NonEmpty(t *testing.T) {
	require.NotEmpty(t, DWMWinSxSKeywords)
	for _, kw := range DWMWinSxSKeywords {
		assert.Equal(t, strings.ToLower(kw), kw,
			"WinSxS keywords must be lowercase for case-insensitive matching")
	}
}

func TestDWMServicingPackageKeywords_NonEmpty(t *testing.T) {
	require.NotEmpty(t, DWMServicingPackageKeywords)
	for _, kw := range DWMServicingPackageKeywords {
		assert.Equal(t, strings.ToLower(kw), kw,
			"servicing keywords must be lowercase")
	}
}
