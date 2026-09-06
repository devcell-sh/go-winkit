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

	// With --force and an empty cache dir the run proceeds past the
	// overwrite gate straight into the fetch, which fails offline — the
	// point is that no prompt appeared and no abort happened.
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

// The default lane (no --build pin) routes to the MCT catalog.
// With a pre-canceled context both MCT and the UUP dump fallback fail,
// so the error reveals the fallback path was tried.
func TestBuild_DefaultLaneTriesMCTThenFallback(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "out.qcow2")
	cacheDir := t.TempDir()
	_, err := runBuild(t, "", dest, "--cache-dir", cacheDir)
	require.Error(t, err)
	// The error must come from the UUP dump fallback (MCT failed first,
	// then UUP dump failed on the canceled context too).
	assert.ErrorContains(t, err, "fetching Windows ISO")
}

// A --build pin routes directly to the UUP dump lane.
func TestBuild_BuildPinUsesUUPDump(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "out.qcow2")
	cacheDir := t.TempDir()
	_, err := runBuild(t, "", dest, "--cache-dir", cacheDir, "--build", "26100.9278")
	require.Error(t, err)
	assert.ErrorContains(t, err, "fetching Windows ISO")
}

func TestBuild_MediaSpecFlagsReachResolve(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "out.qcow2")
	cacheDir := t.TempDir()

	// Bad axes must fail before any network or download work.
	_, err := runBuild(t, "", dest, "--cache-dir", cacheDir, "--os", "linux")
	assert.ErrorContains(t, err, "unsupported os")

	_, err = runBuild(t, "", dest, "--cache-dir", cacheDir, "--arch", "amd64")
	assert.ErrorContains(t, err, "unsupported arch")

	_, err = runBuild(t, "", dest, "--cache-dir", cacheDir,
		"--version", "23h2", "--build", "26100.9278")
	assert.ErrorContains(t, err, "conflict")

	// 26H2 titles look GA upstream but the release never shipped — only
	// an explicit --build may select it.
	_, err = runBuild(t, "", dest, "--cache-dir", cacheDir, "--version", "26h2")
	assert.ErrorContains(t, err, "--build")
}

func TestBuild_StageFlag(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "out.qcow2")
	cacheDir := t.TempDir()

	_, err := runBuild(t, "", dest, "--cache-dir", cacheDir, "--stage", "bogus")
	assert.ErrorContains(t, err, "unknown stage")

	// Valid stages reach the fetch (fail on canceled ctx, not on flag parsing).
	for _, stage := range []string{"core", "base", "wsl", "wsl2"} {
		_, err := runBuild(t, "", dest, "--cache-dir", cacheDir, "--stage", stage)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "unknown stage", "stage %q must be accepted", stage)
	}
}

func TestBuild_AccelValidation(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "out.qcow2")
	cacheDir := t.TempDir()

	// All stages accept hvf, kvm, tcg.
	for _, stage := range []string{"core", "base", "wsl", "wsl2"} {
		for _, accel := range []string{"hvf", "kvm", "tcg"} {
			_, err := runBuild(t, "", dest, "--cache-dir", cacheDir, "--stage", stage, "--accel", accel)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "not valid for stage",
				"stage=%s accel=%s should be accepted", stage, accel)
		}
	}

	// An unknown accelerator is rejected.
	_, err := runBuild(t, "", dest, "--cache-dir", cacheDir, "--stage", "wsl", "--accel", "xen")
	assert.ErrorContains(t, err, "not valid for stage")
}

func TestBuild_ImageTypeFlag(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "out.qcow2")
	cacheDir := t.TempDir()

	_, err := runBuild(t, "", dest, "--cache-dir", cacheDir, "--image-type", "vmdk")
	assert.ErrorContains(t, err, "unknown image format")

	// qcow2 and utm are accepted (reach the fetch, fail on canceled ctx).
	for _, ft := range []string{"qcow2", "utm"} {
		_, err := runBuild(t, "", dest, "--cache-dir", cacheDir, "--image-type", ft)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "unknown image format", "image-type %q must be accepted", ft)
	}
}
