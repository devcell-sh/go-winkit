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
	WebView2VolDir = "webview2"

	webView2Version = "154.0.4258.53"

	webView2NuGetBase    = "https://api.nuget.org/v3-flatcontainer/webview2.runtime.arm64/"
	webView2NuGetCorePkg = "https://api.nuget.org/v3-flatcontainer/webview2.runtime.arm64.core/"

	webView2ContentPrefix = "contentFiles/any/any/WebView2/"
)

// FetchWebView2Files downloads the WebView2 fixed-version runtime for ARM64
// (if not already cached) and returns the extracted file map ready for
// BaseImageConfig.WebView2Files. The map is keyed by volume paths
// (e.g. "/webview2/msedgewebview2.exe").
func FetchWebView2Files(cacheDir string, logf func(string, ...any)) (map[string][]byte, error) {
	cacheKey := "webview2-arm64-" + webView2Version
	dir := filepath.Join(cacheDir, cacheKey)

	pkgs := []struct {
		name string
		url  string
	}{
		{"runtime", fmt.Sprintf("%s%s/webview2.runtime.arm64.%s.nupkg", webView2NuGetBase, webView2Version, webView2Version)},
		{"core", fmt.Sprintf("%s%s/webview2.runtime.arm64.core.%s.nupkg", webView2NuGetCorePkg, webView2Version, webView2Version)},
	}

	for _, pkg := range pkgs {
		dest := filepath.Join(dir, pkg.name+".nupkg")
		if _, err := os.Stat(dest); err == nil {
			if logf != nil {
				logf("WebView2 %s cached", pkg.name)
			}
			continue
		}
		if logf != nil {
			logf("downloading WebView2 %s ARM64 (%s)", pkg.name, webView2Version)
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
		if err := downloadFile(dest, pkg.url); err != nil {
			return nil, fmt.Errorf("downloading WebView2 %s: %w", pkg.name, err)
		}
	}

	files := make(map[string][]byte)
	for _, pkg := range pkgs {
		nupkg := filepath.Join(dir, pkg.name+".nupkg")
		extracted, err := extractWebView2Nupkg(nupkg)
		if err != nil {
			return nil, fmt.Errorf("extracting WebView2 %s: %w", pkg.name, err)
		}
		for k, v := range extracted {
			files[k] = v
		}
	}

	if logf != nil {
		var totalBytes int64
		for _, v := range files {
			totalBytes += int64(len(v))
		}
		logf("WebView2 runtime: %d files, %d MB", len(files), totalBytes/(1024*1024))
	}
	return files, nil
}

func extractWebView2Nupkg(nupkgPath string) (map[string][]byte, error) {
	r, err := zip.OpenReader(nupkgPath)
	if err != nil {
		return nil, fmt.Errorf("opening nupkg: %w", err)
	}
	defer r.Close()

	files := make(map[string][]byte)
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if !strings.HasPrefix(f.Name, webView2ContentPrefix) {
			continue
		}
		relPath := strings.TrimPrefix(f.Name, webView2ContentPrefix)
		if relPath == "" {
			continue
		}
		// Skip cross-arch EBWebView DLLs to save space.
		if strings.HasPrefix(relPath, "EBWebView/x86/") || strings.HasPrefix(relPath, "EBWebView/x64/") {
			continue
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
		volPath := "/" + WebView2VolDir + "/" + relPath
		files[volPath] = data
	}
	return files, nil
}
