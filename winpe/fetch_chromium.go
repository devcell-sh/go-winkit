package winpe

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	ChromiumVolDir     = "chrome"
	ContentShellVolDir = "content-shell"

	chromiumSnapshotRev = "1710677"

	chromiumSnapshotBase = "https://www.googleapis.com/download/storage/v1/b/chromium-browser-snapshots/o/"
	chromiumChromeURL    = chromiumSnapshotBase + "Win_Arm64%2F" + chromiumSnapshotRev + "%2Fchrome-win.zip?alt=media"
	chromiumShellURL     = chromiumSnapshotBase + "Win_Arm64%2F" + chromiumSnapshotRev + "%2Fcontent-shell.zip?alt=media"
)

// skipChromiumFiles lists zip entries that are test-only and should not be
// packaged into the PE volume. They add ~330 MB with no runtime value.
var skipChromiumFiles = []string{
	"interactive_ui_tests",
	"_test",
}

// FetchChromiumFiles downloads the Chromium ARM64 snapshot build (full
// browser) and returns a file map ready for BaseImageConfig.ChromiumFiles.
// Files land at X:\winkit\chrome\ inside WinPE.
func FetchChromiumFiles(cacheDir string, logf func(string, ...any)) (map[string][]byte, error) {
	cacheKey := "chromium-arm64-" + chromiumSnapshotRev
	dir := filepath.Join(cacheDir, cacheKey)
	dest := filepath.Join(dir, "chrome-win.zip")

	if _, err := os.Stat(dest); err != nil {
		if logf != nil {
			logf("downloading Chromium ARM64 (snapshot %s, ~320 MB)", chromiumSnapshotRev)
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
		if err := downloadFile(dest, chromiumChromeURL); err != nil {
			return nil, fmt.Errorf("downloading Chromium: %w", err)
		}
	} else if logf != nil {
		logf("Chromium ARM64 cached")
	}

	return extractChromiumZip(dest, "chrome-win/", ChromiumVolDir, true, logf)
}

// FetchContentShellFiles downloads the Chromium content-shell ARM64 build
// (minimal browser shell) and returns a file map ready for
// BaseImageConfig.ContentShellFiles. Files land at X:\winkit\content-shell\
// inside WinPE.
func FetchContentShellFiles(cacheDir string, logf func(string, ...any)) (map[string][]byte, error) {
	cacheKey := "chromium-arm64-" + chromiumSnapshotRev
	dir := filepath.Join(cacheDir, cacheKey)
	dest := filepath.Join(dir, "content-shell.zip")

	if _, err := os.Stat(dest); err != nil {
		if logf != nil {
			logf("downloading content-shell ARM64 (snapshot %s, ~95 MB)", chromiumSnapshotRev)
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
		if err := downloadFile(dest, chromiumShellURL); err != nil {
			return nil, fmt.Errorf("downloading content-shell: %w", err)
		}
	} else if logf != nil {
		logf("content-shell ARM64 cached")
	}

	return extractChromiumZip(dest, "content-shell/", ContentShellVolDir, false, logf)
}

// MergeContentShellDeps copies locale paks and SwiftShader Vulkan files from
// the Chrome file map into the content-shell file map. Content-shell's zip
// doesn't ship these, but they're required for rendering in WinPE.
func MergeContentShellDeps(chromiumFiles, contentShellFiles map[string][]byte) int {
	merged := 0
	for volPath, data := range chromiumFiles {
		var dstPath string
		switch {
		case strings.HasPrefix(volPath, "/"+ChromiumVolDir+"/locales/"):
			dstPath = "/" + ContentShellVolDir + "/locales/" + strings.TrimPrefix(volPath, "/"+ChromiumVolDir+"/locales/")
		case strings.HasSuffix(volPath, "/vk_swiftshader.dll"):
			dstPath = "/" + ContentShellVolDir + "/vk_swiftshader.dll"
		case strings.HasSuffix(volPath, "/vk_swiftshader_icd.json"):
			dstPath = "/" + ContentShellVolDir + "/vk_swiftshader_icd.json"
		case strings.HasSuffix(volPath, "/vulkan-1.dll"):
			dstPath = "/" + ContentShellVolDir + "/vulkan-1.dll"
		default:
			continue
		}
		if _, exists := contentShellFiles[dstPath]; !exists {
			contentShellFiles[dstPath] = data
			merged++
		}
	}
	return merged
}

func extractChromiumZip(zipPath, prefix, volDir string, skipTests bool, logf func(string, ...any)) (map[string][]byte, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", zipPath, err)
	}
	defer r.Close()

	files := make(map[string][]byte)
	var skippedBytes int64
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if !strings.HasPrefix(f.Name, prefix) {
			continue
		}
		relPath := strings.TrimPrefix(f.Name, prefix)
		if relPath == "" {
			continue
		}

		if skipTests {
			skip := false
			lower := strings.ToLower(relPath)
			for _, pat := range skipChromiumFiles {
				if strings.Contains(lower, pat) {
					skip = true
					skippedBytes += int64(f.UncompressedSize64)
					break
				}
			}
			if skip {
				continue
			}
		}

		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("opening %s: %w", f.Name, err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", f.Name, err)
		}
		volPath := "/" + volDir + "/" + relPath
		files[volPath] = data
	}

	if logf != nil {
		var totalBytes int64
		for _, v := range files {
			totalBytes += int64(len(v))
		}
		logf("%s: %d files, %d MB", volDir, len(files), totalBytes/(1024*1024))
		if skippedBytes > 0 {
			logf("%s: skipped %d MB of test binaries", volDir, skippedBytes/(1024*1024))
		}
	}
	return files, nil
}
