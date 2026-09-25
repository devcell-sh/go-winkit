package winpe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWSL1EngineFiles_ContainsOnlyWSL1Runtime(t *testing.T) {
	files := WSL1EngineFiles()
	require.NotEmpty(t, files)

	set := make(map[string]bool, len(files))
	for _, file := range files {
		assert.False(t, filepath.IsAbs(file), "%s must be relative to the extracted MSI", file)
		set[filepath.ToSlash(file)] = true
	}

	for _, required := range []string{
		"wslservice.exe", "wsl.exe", "libwsl.dll", "wsldeps.dll",
		"wslserviceproxystub.dll", "wslhost.exe", "wslrelay.exe",
		"tools/init", "tools/bsdtar",
	} {
		assert.True(t, set[required], "%s is required by the WSL1 path", required)
	}

	for _, wsl2Only := range []string{
		"tools/kernel", "tools/modules.vhd", "tools/initrd.img",
		"system.vhd", "wsldevicehost.dll", "wslg.exe", "msrdc.exe",
	} {
		assert.False(t, set[wsl2Only], "%s is WSL2/WSLg-only", wsl2Only)
	}
}

func TestWSL1ServiceDependencyPaths(t *testing.T) {
	assert.ElementsMatch(t, []string{
		`\Windows\System32\computecore.dll`,
		`\Windows\System32\computenetwork.dll`,
		`\Windows\System32\msi.dll`,
		`\Windows\System32\vid.dll`,
	}, WSL1ServiceDependencyPaths)
}

func TestWSL1InboxSupportPaths(t *testing.T) {
	assert.ElementsMatch(t, []string{
		`\Windows\System32\drivers\bfs.sys`,
		`\Windows\System32\drivers\afunix.sys`,
		`\Windows\System32\wshunix.dll`,
		`\Windows\System32\wci.dll`,
		`\Windows\System32\drivers\wcifs.sys`,
		`\Windows\System32\drivers\bindflt.sys`,
		`\Windows\System32\drivers\p9rdr.sys`,
		`\Windows\System32\p9np.dll`,
	}, WSL1InboxSupportPaths)
}

func TestWSL1EnginePatchSet(t *testing.T) {
	dir := t.TempDir()
	for _, name := range WSL1EngineFiles() {
		path := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(name), 0o644))
	}

	ps, err := WSL1EnginePatchSet(dir)
	require.NoError(t, err)
	assert.Equal(t, 2, ps.ImageNum)
	require.Len(t, ps.Files, len(WSL1EngineFiles()))
	for _, name := range WSL1EngineFiles() {
		target := `\Program Files\WSL\` + strings.ReplaceAll(name, `/`, `\`)
		assert.Equal(t, filepath.Join(dir, filepath.FromSlash(name)), ps.Files[target])
	}
}

func TestWSL1EnginePatchSet_RejectsIncompletePayload(t *testing.T) {
	_, err := WSL1EnginePatchSet(t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "wslservice.exe")
}
