package virtio

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/devcell-sh/go-winkit/cache"
	"github.com/devcell-sh/go-winkit/media/isokit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidate_MissingFileReportsNotExist(t *testing.T) {
	err := Validate(filepath.Join(t.TempDir(), "absent.iso"))

	assert.True(t, os.IsNotExist(err),
		"a missing cache entry must be distinguishable from a corrupt one, "+
			"so FetchISO knows whether to warn about discarding it")
}

// An HTTP error page or a truncated transfer is the failure mode that
// matters here: it lands at the cache path and every later run treats it
// as a hit, so the size floor has to reject it before the rename.
func TestValidate_RejectsTruncatedDownload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "virtio-win.iso")
	require.NoError(t, os.WriteFile(path, []byte("<html>404</html>"), 0o644))

	err := Validate(path)

	require.Error(t, err)
	assert.False(t, os.IsNotExist(err), "the file exists; it is merely unusable")
	assert.Contains(t, err.Error(), "expected at least")
}

// Size alone does not prove it is the right ISO. A big file that is not a
// virtio-win image must fail on the driver probe, not pass silently and
// then break a WinPE build much later.
func TestValidate_RejectsRightSizedNonVirtioISO(t *testing.T) {
	path := filepath.Join(t.TempDir(), "virtio-win.iso")
	smallMinSize(t)
	require.NoError(t, os.WriteFile(path, make([]byte, minSize+1), 0o644))

	err := Validate(path)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "vioscsi")
}

func TestValidate_AcceptsISOCarryingTheARM64Driver(t *testing.T) {
	assert.NoError(t, Validate(virtioISOFixture(t)))
}

func TestFetchISO_ReturnsCachedISOWithoutDownloading(t *testing.T) {
	dir := cacheWithISO(t)

	var served bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served = true
	}))
	defer srv.Close()

	path, err := FetchISO(context.Background(), FetchConfig{CacheDir: dir, URL: srv.URL})

	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, cache.VirtIOISOName), path)
	assert.False(t, served, "a usable cached ISO must not trigger a re-download")
}

func TestFetchISO_DownloadsAndCachesTheISO(t *testing.T) {
	dir := t.TempDir()
	body, err := os.ReadFile(virtioISOFixture(t))
	require.NoError(t, err)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer srv.Close()

	path, err := FetchISO(context.Background(), FetchConfig{CacheDir: dir, URL: srv.URL})

	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, cache.VirtIOISOName), path)
	assert.NoError(t, Validate(path), "the cached ISO must be usable by a later run")
}

// The download lands on a .part file and is only renamed after it
// validates. An interrupted or bogus transfer must therefore leave
// nothing at the cache path for the next run to trust.
func TestFetchISO_BadDownloadNeverLandsAtTheCachePath(t *testing.T) {
	dir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not an ISO"))
	}))
	defer srv.Close()

	_, err := FetchISO(context.Background(), FetchConfig{CacheDir: dir, URL: srv.URL})

	require.Error(t, err)
	_, statErr := os.Stat(filepath.Join(dir, cache.VirtIOISOName))
	assert.True(t, os.IsNotExist(statErr),
		"a failed download must not leave an ISO the next run reads as a cache hit")
}

// A cached copy that no longer validates has to be replaced, not returned
// and not left to fail deep inside a WinPE build.
func TestFetchISO_ReplacesCorruptCachedISO(t *testing.T) {
	dir := t.TempDir()
	smallMinSize(t)
	isoPath := filepath.Join(dir, cache.VirtIOISOName)
	require.NoError(t, os.WriteFile(isoPath, []byte("truncated"), 0o644))

	body, err := os.ReadFile(virtioISOFixture(t))
	require.NoError(t, err)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer srv.Close()

	path, err := FetchISO(context.Background(), FetchConfig{CacheDir: dir, URL: srv.URL})

	require.NoError(t, err)
	assert.NoError(t, Validate(path))
}

func TestFetchISO_ReportsHTTPFailure(t *testing.T) {
	dir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := FetchISO(context.Background(), FetchConfig{CacheDir: dir, URL: srv.URL})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "404")
}

func TestFetchISO_DefaultsToTheSharedCacheDir(t *testing.T) {
	dir := cacheWithISO(t)
	t.Setenv(cache.DirEnv, dir)

	path, err := FetchISO(context.Background(), FetchConfig{})

	require.NoError(t, err)
	assert.Equal(t, cache.VirtIOISO(), path,
		"fetch must seed the same path the integration tests read")
}

// smallMinSize lowers the size floor so tests can use ISOs of a few KB
// instead of the several hundred MB a real virtio-win image weighs.
func smallMinSize(t *testing.T) {
	t.Helper()
	orig := minSize
	minSize = 1 << 10
	t.Cleanup(func() { minSize = orig })
}

// virtioISOFixture builds a genuine ISO carrying the ARM64 vioscsi driver
// that Validate probes for, so the caching tests exercise real validation
// rather than a stub.
func virtioISOFixture(t *testing.T) string {
	t.Helper()
	smallMinSize(t)

	isoPath := filepath.Join(t.TempDir(), "fixture-virtio-win.iso")
	err := isokit.CreateSimpleISO(isoPath, map[string][]byte{
		validationPath: []byte("; vioscsi ARM64 driver\n[Version]\nClass=SCSIAdapter\n"),
	})
	require.NoError(t, err, "building the virtio-win ISO fixture")
	return isoPath
}

func cacheWithISO(t *testing.T) string {
	t.Helper()
	fixture := virtioISOFixture(t)
	data, err := os.ReadFile(fixture)
	require.NoError(t, err)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, cache.VirtIOISOName), data, 0o644))
	return dir
}
