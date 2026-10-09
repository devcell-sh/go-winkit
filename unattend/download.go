package unattend

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// OpenSSHPayloadPath returns the expected cache path for the OpenSSH release.
func OpenSSHPayloadPath(cacheDir string) string {
	return filepath.Join(cacheDir, OpenSSHPayloadName)
}

// DownloadOpenSSH fetches Microsoft's signed Win32-OpenSSH ARM64 release to
// cacheDir, returning the local path. Uses a .done marker so repeated calls
// are free. When noCache is true the marker is removed to force re-download.
func DownloadOpenSSH(ctx context.Context, cacheDir string, noCache bool) (string, error) {
	dest := OpenSSHPayloadPath(cacheDir)
	return downloadCached(ctx, OpenSSHReleaseURL, dest, noCache)
}

// DownloadRclone fetches the pinned rclone windows/arm64 release zip to
// cacheDir, returning the local path. Same caching contract as
// DownloadOpenSSH.
func DownloadRclone(ctx context.Context, cacheDir string, noCache bool) (string, error) {
	dest := filepath.Join(cacheDir, RclonePayloadName)
	return downloadCached(ctx, RcloneReleaseURL, dest, noCache)
}

// DownloadWinFsp fetches the pinned WinFsp MSI to cacheDir, returning the
// local path. Same caching contract as DownloadOpenSSH.
func DownloadWinFsp(ctx context.Context, cacheDir string, noCache bool) (string, error) {
	dest := filepath.Join(cacheDir, WinFspPayloadName)
	return downloadCached(ctx, WinFspReleaseURL, dest, noCache)
}

// downloadCached fetches url to dest with a .done marker for caching.
func downloadCached(ctx context.Context, url, dest string, noCache bool) (string, error) {
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return "", fmt.Errorf("creating cache dir: %w", err)
	}

	if noCache {
		os.Remove(dest + ".done")
	}

	if hasDownloadMarker(dest) {
		if _, err := os.Stat(dest); err == nil {
			return dest, nil
		}
		os.Remove(dest + ".done")
	}

	if err := downloadFile(ctx, url, dest); err != nil {
		return "", err
	}
	if err := os.WriteFile(dest+".done", nil, 0644); err != nil {
		return "", fmt.Errorf("writing download marker: %w", err)
	}
	return dest, nil
}

func hasDownloadMarker(path string) bool {
	_, err := os.Stat(path + ".done")
	return err == nil
}

func downloadFile(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}

	tmp, err := os.CreateTemp(filepath.Dir(dest), filepath.Base(dest)+".part-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		tmp.Close()
		os.Remove(tmpName)
	}()

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, dest)
}
