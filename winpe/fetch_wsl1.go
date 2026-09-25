package winpe

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
)

const wsl1PackageArch = "arm64"

// FetchWSL1Engine returns the extracted Microsoft WSL ARM64 runtime used by
// the WinPE WSL1 builder. A complete cached extraction is reused. Downloads
// and extraction are staged so an interruption cannot look like a valid cache.
func FetchWSL1Engine(ctx context.Context, cacheDir string, noCache bool, logf func(string, ...any)) (string, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	toolDir := filepath.Join(cacheDir, "tools", "wsl-"+WSL1PackageVersion)
	msiName := fmt.Sprintf("wsl.%s.0.%s.msi", WSL1PackageVersion, wsl1PackageArch)
	msiPath := filepath.Join(toolDir, msiName)
	extractDir := filepath.Join(toolDir, "extracted")
	engineDir := filepath.Join(extractDir, "PFiles64", "WSL")

	if !noCache {
		if _, err := WSL1EnginePatchSet(engineDir); err == nil {
			logf("using cached WSL %s runtime at %s", WSL1PackageVersion, engineDir)
			return engineDir, nil
		}
	}
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		return "", fmt.Errorf("creating WSL tool cache: %w", err)
	}

	if noCache {
		_ = os.Remove(msiPath)
	}
	if _, err := os.Stat(msiPath); os.IsNotExist(err) {
		url := fmt.Sprintf("https://github.com/microsoft/WSL/releases/download/%s/%s",
			WSL1PackageVersion, msiName)
		logf("downloading WSL %s ARM64 runtime", WSL1PackageVersion)
		if err := downloadWSL1Package(ctx, url, msiPath); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", fmt.Errorf("checking cached WSL package: %w", err)
	}

	if _, err := exec.LookPath("msiextract"); err != nil {
		return "", fmt.Errorf("extracting WSL runtime requires msiextract in PATH: %w", err)
	}
	tmpExtract := extractDir + ".part"
	if err := os.RemoveAll(tmpExtract); err != nil {
		return "", fmt.Errorf("clearing partial WSL extraction: %w", err)
	}
	if err := os.MkdirAll(tmpExtract, 0o755); err != nil {
		return "", fmt.Errorf("creating WSL extraction directory: %w", err)
	}
	logf("extracting WSL %s ARM64 runtime", WSL1PackageVersion)
	cmd := exec.CommandContext(ctx, "msiextract", "-C", tmpExtract, msiPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = os.RemoveAll(tmpExtract)
		return "", fmt.Errorf("extracting %s: %w\n%s", msiPath, err, out)
	}
	tmpEngine := filepath.Join(tmpExtract, "PFiles64", "WSL")
	if _, err := WSL1EnginePatchSet(tmpEngine); err != nil {
		_ = os.RemoveAll(tmpExtract)
		return "", fmt.Errorf("validating extracted WSL runtime: %w", err)
	}
	if err := os.RemoveAll(extractDir); err != nil {
		_ = os.RemoveAll(tmpExtract)
		return "", fmt.Errorf("replacing WSL extraction: %w", err)
	}
	if err := os.Rename(tmpExtract, extractDir); err != nil {
		_ = os.RemoveAll(tmpExtract)
		return "", fmt.Errorf("installing WSL extraction: %w", err)
	}
	return engineDir, nil
}

func downloadWSL1Package(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("downloading %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading %s: HTTP %s", url, resp.Status)
	}
	tmp := dest + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(f, resp.Body)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("downloading %s: %w", url, copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
