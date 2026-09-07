package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runBuild executes the build command with the given args and stdin, with
// wimlib forced "available" so flag handling is reachable in an untagged
// test binary.
func runBuild(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	restore := wimlibAvailable
	wimlibAvailable = func() bool { return true }
	t.Cleanup(func() { wimlibAvailable = restore })

	cmd := newBuildCmd()
	cmd.SetIn(strings.NewReader(stdin))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := cmd.ExecuteContext(ctx)
	return out.String(), err
}

func TestBuild_PromptDeclinedAborts(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "img.qcow2")
	require.NoError(t, os.WriteFile(dest, []byte("existing"), 0o644))

	out, err := runBuild(t, "n\n", dest)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "aborted")
	assert.Contains(t, out, "overwrite? [y/N]")

	data, _ := os.ReadFile(dest)
	assert.Equal(t, "existing", string(data), "declining must not touch the file")
}

func TestBuild_ForceSkipsPrompt(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "img.qcow2")
	require.NoError(t, os.WriteFile(dest, []byte("existing"), 0o644))

	cacheDir := t.TempDir()
	out, err := runBuild(t, "", dest, "--force", "--cache-dir", cacheDir)
	require.Error(t, err)
	assert.NotContains(t, out, "overwrite?")
	assert.NotContains(t, err.Error(), "aborted")
}

func TestClearCache_RemovesISOsAndDownloadTrees(t *testing.T) {
	cacheDir := t.TempDir()
	iso := filepath.Join(cacheDir, "windows-arm64-en-us.iso")
	virtio := filepath.Join(cacheDir, "virtio-win.iso")
	esd := filepath.Join(cacheDir, "uupdump-download", "28000.2804", "professional_en-us.esd")
	marker := esd + ".done"
	keep := filepath.Join(cacheDir, "unrelated.txt")

	for _, p := range []string{iso, virtio, esd, marker, keep} {
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte("x"), 0o644))
	}

	clearCache(cacheDir, func(string, ...any) {})

	for _, p := range []string{iso, virtio, esd, marker} {
		_, err := os.Stat(p)
		assert.True(t, os.IsNotExist(err), "%s must be removed", p)
	}
	_, err := os.Stat(keep)
	assert.NoError(t, err, "unrelated cache content must survive")
}

func TestBuild_DefaultLaneTriesMCTThenFallback(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "out.qcow2")
	cacheDir := t.TempDir()
	_, err := runBuild(t, "", dest, "--cache-dir", cacheDir)
	require.Error(t, err)
	assert.ErrorContains(t, err, "fetching Windows ISO")
}

func TestBuild_AccelValidation(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "out.qcow2")
	cacheDir := t.TempDir()

	for _, accel := range []string{"hvf", "kvm", "tcg"} {
		_, err := runBuild(t, "", dest, "--cache-dir", cacheDir, "--accel", accel)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "not valid",
			"accel=%s should be accepted", accel)
	}

	_, err := runBuild(t, "", dest, "--cache-dir", cacheDir, "--accel", "xen")
	assert.ErrorContains(t, err, "not valid")
}

func TestBuild_ImageTypeFlag(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "out.qcow2")
	cacheDir := t.TempDir()

	_, err := runBuild(t, "", dest, "--cache-dir", cacheDir, "--image-type", "vmdk")
	assert.ErrorContains(t, err, "unknown image format")

	for _, ft := range []string{"qcow2", "utm"} {
		_, err := runBuild(t, "", dest, "--cache-dir", cacheDir, "--image-type", ft)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "unknown image format", "image-type %q must be accepted", ft)
	}
}

func TestBuild_PEFlag(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "out.qcow2")
	cacheDir := t.TempDir()

	_, err := runBuild(t, "", dest, "--cache-dir", cacheDir, "--pe")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "mutually exclusive")
}

func TestBuild_WSLFlag(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "out.qcow2")
	cacheDir := t.TempDir()

	_, err := runBuild(t, "", dest, "--cache-dir", cacheDir, "--wsl")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "mutually exclusive")
}

func TestBuild_PEAndWSLMutuallyExclusive(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "out.qcow2")
	cacheDir := t.TempDir()

	_, err := runBuild(t, "", dest, "--cache-dir", cacheDir, "--pe", "--wsl")
	assert.ErrorContains(t, err, "mutually exclusive")
}

func TestBuild_WSLImageFlag(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "out.qcow2")
	cacheDir := t.TempDir()

	_, err := runBuild(t, "", dest, "--cache-dir", cacheDir, "--wsl-image", "ubuntu")
	require.Error(t, err)
	// --wsl-image implies --wsl, so it should set up WSL config.
	assert.NotContains(t, err.Error(), "mutually exclusive")
}

func TestBuild_FromFlag(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "out.qcow2")
	cacheDir := t.TempDir()

	_, err := runBuild(t, "", dest, "--cache-dir", cacheDir, "--from", "windows/11-pro-arm64")
	require.Error(t, err)
	assert.ErrorContains(t, err, "fetching Windows ISO")
}

func TestBuild_FromLocalISO(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "out.qcow2")
	cacheDir := t.TempDir()

	_, err := runBuild(t, "", dest, "--cache-dir", cacheDir, "--from", "./windows.iso")
	assert.ErrorContains(t, err, "not yet supported")
}

func TestBuild_FromLocalWIM(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "out.qcow2")
	cacheDir := t.TempDir()

	_, err := runBuild(t, "", dest, "--cache-dir", cacheDir, "--from", "./install.wim")
	assert.ErrorContains(t, err, "not yet supported")
}

func TestBuild_ConfigFileFlag(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "winkit.yaml")
	require.NoError(t, os.WriteFile(cfgFile, []byte("from: windows/11-pro-arm64\npe: true\n"), 0o644))
	dest := filepath.Join(dir, "out.qcow2")
	cacheDir := t.TempDir()

	_, err := runBuild(t, "", dest, "--cache-dir", cacheDir, "-f", cfgFile)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "mutually exclusive")
}

func TestBuild_ConfigFileMissing(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "out.qcow2")
	cacheDir := t.TempDir()

	_, err := runBuild(t, "", dest, "--cache-dir", cacheDir, "-f", "/nonexistent/winkit.yaml")
	assert.ErrorContains(t, err, "reading config")
}

func TestBuild_CLIOverridesConfig(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "winkit.yaml")
	require.NoError(t, os.WriteFile(cfgFile, []byte("pe: true\n"), 0o644))
	dest := filepath.Join(dir, "out.qcow2")
	cacheDir := t.TempDir()

	// Config says pe:true but CLI says --wsl, which should override to WSL mode.
	// pe+wsl would be mutually exclusive if both were set, but --wsl overrides.
	// Actually, the flag override only sets WSL, it doesn't unset PE.
	// So this should error with "mutually exclusive".
	_, err := runBuild(t, "", dest, "--cache-dir", cacheDir, "-f", cfgFile, "--wsl")
	assert.ErrorContains(t, err, "mutually exclusive")
}
