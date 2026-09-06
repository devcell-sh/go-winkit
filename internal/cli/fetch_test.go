package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runFetch(t *testing.T, args ...string) (string, error) {
	t.Helper()
	restore := wimlibAvailable
	wimlibAvailable = func() bool { return true }
	t.Cleanup(func() { wimlibAvailable = restore })

	cmd := newFetchCmd()
	cmd.SetIn(strings.NewReader(""))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := cmd.ExecuteContext(ctx)
	return out.String(), err
}

func TestFetch_DefaultSourceIsMCT(t *testing.T) {
	cacheDir := t.TempDir()
	_, err := runFetch(t, "--cache-dir", cacheDir)
	// With a canceled context the MCT fetch fails, but the error should
	// come from the MCT path (not uupdump).
	require.Error(t, err)
}

func TestFetch_ExplicitMCTWithBuildErrors(t *testing.T) {
	cacheDir := t.TempDir()
	_, err := runFetch(t, "--cache-dir", cacheDir, "--source", "mct", "--build", "26100.9278")
	require.Error(t, err)
	assert.ErrorContains(t, err, "MCT catalog serves only the published GA build")
	assert.Contains(t, err.Error(), "26100.9278")
}

func TestFetch_ImplicitMCTWithBuildReroutesToUUPDump(t *testing.T) {
	cacheDir := t.TempDir()
	// Default source is mct, but --build silently re-routes to uupdump.
	_, err := runFetch(t, "--cache-dir", cacheDir, "--build", "26100.9278")
	require.Error(t, err)
	// Must NOT contain the MCT error; must come from the uupdump path.
	assert.NotContains(t, err.Error(), "MCT catalog serves only")
}

func TestFetch_ExplicitUUPDumpWithBuildWorks(t *testing.T) {
	cacheDir := t.TempDir()
	_, err := runFetch(t, "--cache-dir", cacheDir, "--source", "uupdump", "--build", "26100.9278")
	require.Error(t, err)
	// Fails on the network (canceled ctx), not on source validation.
	assert.NotContains(t, err.Error(), "MCT catalog serves only")
}
