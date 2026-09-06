package unattend

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDownloadOpenSSH_FetchesAndCaches(t *testing.T) {
	payload := []byte("fake-openssh-zip-content")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(payload)
	}))
	defer srv.Close()

	dir := t.TempDir()
	path, err := downloadCached(context.Background(), srv.URL, filepath.Join(dir, "openssh.zip"), false)
	require.NoError(t, err)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, payload, got)

	_, err = os.Stat(path + ".done")
	assert.NoError(t, err, ".done marker must exist")
}

func TestDownloadOpenSSH_CacheHit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("server should not be hit on cache hit")
	}))
	defer srv.Close()

	dir := t.TempDir()
	dest := filepath.Join(dir, "openssh.zip")
	require.NoError(t, os.WriteFile(dest, []byte("cached"), 0644))
	require.NoError(t, os.WriteFile(dest+".done", nil, 0644))

	path, err := downloadCached(context.Background(), srv.URL, dest, false)
	require.NoError(t, err)
	assert.Equal(t, dest, path)
}

func TestDownloadOpenSSH_NoCacheForcesRedownload(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte("fresh"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	dest := filepath.Join(dir, "openssh.zip")
	require.NoError(t, os.WriteFile(dest, []byte("stale"), 0644))
	require.NoError(t, os.WriteFile(dest+".done", nil, 0644))

	path, err := downloadCached(context.Background(), srv.URL, dest, true)
	require.NoError(t, err)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, []byte("fresh"), got)
	assert.Equal(t, 1, calls)
}

func TestDownloadOpenSSH_DoneMarkerButFileMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("redownloaded"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	dest := filepath.Join(dir, "openssh.zip")
	require.NoError(t, os.WriteFile(dest+".done", nil, 0644))

	path, err := downloadCached(context.Background(), srv.URL, dest, false)
	require.NoError(t, err)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, []byte("redownloaded"), got)
}

func TestDownloadOpenSSH_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	dir := t.TempDir()
	_, err := downloadCached(context.Background(), srv.URL, filepath.Join(dir, "openssh.zip"), false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "404")
}

func TestOpenSSHPayloadPath(t *testing.T) {
	path := OpenSSHPayloadPath("/tmp/cache")
	assert.Equal(t, filepath.Join("/tmp/cache", OpenSSHPayloadName), path)
}
