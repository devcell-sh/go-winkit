package winpe

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

const PwshZipURL = "https://github.com/PowerShell/PowerShell/releases/download/v7.6.5/PowerShell-7.6.5-win-arm64.zip"

// FetchPwshFiles downloads PowerShell 7 (if not already cached) and returns
// the extracted file map ready for BaseImageConfig.PwshFiles.
func FetchPwshFiles(cacheDir string, logf func(string, ...any)) (map[string][]byte, error) {
	zipPath := filepath.Join(cacheDir, filepath.Base(PwshZipURL))

	if _, err := os.Stat(zipPath); err != nil {
		if logf != nil {
			logf("downloading PowerShell 7 ARM64")
		}
		if err := downloadFile(zipPath, PwshZipURL); err != nil {
			return nil, fmt.Errorf("downloading pwsh: %w", err)
		}
	} else if logf != nil {
		logf("pwsh cached")
	}

	files, err := ExtractPwshFiles(zipPath)
	if err != nil {
		return nil, fmt.Errorf("extracting pwsh: %w", err)
	}
	return files, nil
}

func downloadFile(dest, url string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	tmp := dest + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dest)
}
