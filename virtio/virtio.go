// Package virtio downloads the virtio-win driver ISO, the source of the
// storage, serial and network drivers WinPE needs to see a QEMU guest's
// devices.
//
// Unlike the Windows installer there is nothing to assemble here: the ISO
// ships ready to mount, so fetching is a download plus a validation that
// the ARM64 drivers winkit actually loads are present inside it.
package virtio

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/devcell-sh/go-winkit/cache"
	"github.com/devcell-sh/go-winkit/isokit"
)

// StableURL is the upstream "stable" channel published by the Fedora
// virt group, the same build the virtio-win documentation points at.
const StableURL = "https://fedorapeople.org/groups/virt/virtio-win/direct-downloads/stable-virtio/virtio-win.iso"

// validationPath is a file that only a real virtio-win ISO carries. It is
// also the exact driver winpe.LoadWinPEStorageDrivers extracts, so a
// download that passes this check is one that a WinPE build can use.
const validationPath = "/vioscsi/w11/ARM64/vioscsi.inf"

// minSize guards against caching an error page or a truncated transfer.
// The real ISO is several hundred MB. It is a var so tests can build a
// small but genuine ISO to exercise the validation and caching paths.
var minSize int64 = 100 << 20

// ProgressFunc reports download progress. total is 0 when the server
// does not send a Content-Length.
type ProgressFunc func(downloaded, total int64)

// FetchConfig configures FetchISO.
type FetchConfig struct {
	// CacheDir is where the ISO is stored. Defaults to cache.Dir().
	CacheDir string
	// URL overrides the upstream download location.
	URL string
	// HTTPClient overrides the client used for the download.
	HTTPClient *http.Client

	OnProgress ProgressFunc

	// Logger receives leveled progress output. LogFunc is the level-less
	// predecessor, used only when Logger is nil.
	Logger  *slog.Logger
	LogFunc func(format string, args ...any)
}

func (c *FetchConfig) logf(format string, args ...any) {
	if c.Logger != nil {
		c.Logger.Info(fmt.Sprintf(format, args...))
		return
	}
	if c.LogFunc != nil {
		c.LogFunc(format, args...)
	}
}

func (c *FetchConfig) debugf(format string, args ...any) {
	if c.Logger != nil {
		c.Logger.Debug(fmt.Sprintf(format, args...))
		return
	}
	if c.LogFunc != nil {
		c.LogFunc(format, args...)
	}
}

func (c *FetchConfig) warnf(format string, args ...any) {
	if c.Logger != nil {
		c.Logger.Warn(fmt.Sprintf(format, args...))
		return
	}
	if c.LogFunc != nil {
		c.LogFunc("WARNING: "+format, args...)
	}
}

// FetchISO downloads the virtio-win ISO into the cache and returns its
// path. A usable cached copy is returned as-is; a corrupt or truncated
// one is discarded and re-downloaded.
func FetchISO(ctx context.Context, cfg FetchConfig) (string, error) {
	if cfg.CacheDir == "" {
		cfg.CacheDir = cache.Dir()
	}
	if cfg.URL == "" {
		cfg.URL = StableURL
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 0}
	}

	isoPath := filepath.Join(cfg.CacheDir, cache.VirtIOISOName)

	if err := Validate(isoPath); err == nil {
		info, _ := os.Stat(isoPath)
		cfg.logf("virtio-win ISO already cached (%.1f MB)", float64(info.Size())/(1024*1024))
		cfg.debugf("cached virtio-win ISO: %s", isoPath)
		return isoPath, nil
	} else if !os.IsNotExist(err) {
		if _, statErr := os.Stat(isoPath); statErr == nil {
			cfg.warnf("cached virtio-win ISO is unusable (%v) — re-downloading", err)
			os.Remove(isoPath)
		}
	}

	if err := os.MkdirAll(cfg.CacheDir, 0o755); err != nil {
		return "", fmt.Errorf("creating cache dir: %w", err)
	}

	// Download to a sibling temp file so an interrupted transfer never
	// leaves a half-written ISO at the path tests treat as a cache hit.
	tmpPath := isoPath + ".part"
	cfg.logf("downloading virtio-win ISO")
	cfg.debugf("virtio-win URL: %s", cfg.URL)
	if err := download(ctx, cfg, tmpPath); err != nil {
		return "", err
	}

	if err := Validate(tmpPath); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("downloaded virtio-win ISO is unusable: %w", err)
	}

	if err := os.Rename(tmpPath, isoPath); err != nil {
		return "", fmt.Errorf("moving ISO into place: %w", err)
	}

	info, _ := os.Stat(isoPath)
	if info != nil {
		cfg.logf("virtio-win ISO ready (%.1f MB)", float64(info.Size())/(1024*1024))
		cfg.debugf("virtio-win ISO: %s", isoPath)
	}
	return isoPath, nil
}

// Validate reports whether path is a virtio-win ISO carrying the ARM64
// drivers winkit needs. A missing file is reported as os.ErrNotExist.
func Validate(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Size() < minSize {
		return fmt.Errorf("ISO is %.1f MB, expected at least %d MB",
			float64(info.Size())/(1024*1024), minSize>>20)
	}

	if _, err := isokit.ReadFileFromISO(path, validationPath); err != nil {
		return fmt.Errorf("no ARM64 vioscsi driver at %s: %w", validationPath, err)
	}
	return nil
}

func download(ctx context.Context, cfg FetchConfig, dest string) error {
	var resumeFrom int64
	if info, err := os.Stat(dest); err == nil {
		resumeFrom = info.Size()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.URL, nil)
	if err != nil {
		return err
	}
	if resumeFrom > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", resumeFrom))
		cfg.logf("resuming from %.1f MB", float64(resumeFrom)/(1024*1024))
	}

	resp, err := cfg.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("downloading virtio-win ISO: %w", err)
	}
	defer resp.Body.Close()

	// A stale .part longer than the current upstream file makes the range
	// unsatisfiable; start over rather than keep a file we cannot finish.
	if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable {
		resp.Body.Close()
		os.Remove(dest)
		cfg2 := cfg
		return download(ctx, cfg2, dest)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("HTTP %d downloading virtio-win ISO", resp.StatusCode)
	}

	flags := os.O_CREATE | os.O_WRONLY
	if resp.StatusCode == http.StatusOK {
		flags |= os.O_TRUNC
		resumeFrom = 0
	} else {
		flags |= os.O_APPEND
	}

	out, err := os.OpenFile(dest, flags, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()

	total := resp.ContentLength + resumeFrom
	written := resumeFrom
	var lastReport time.Time
	pr := &progressReader{r: resp.Body, onRead: func(n int) {
		written += int64(n)
		if cfg.OnProgress != nil && time.Since(lastReport) > 200*time.Millisecond {
			lastReport = time.Now()
			cfg.OnProgress(written, total)
		}
	}}

	if _, err := io.Copy(out, pr); err != nil {
		return fmt.Errorf("downloading virtio-win ISO: %w", err)
	}
	if cfg.OnProgress != nil {
		cfg.OnProgress(written, total)
	}
	return nil
}

type progressReader struct {
	r      io.Reader
	onRead func(n int)
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.r.Read(p)
	if n > 0 {
		pr.onRead(n)
	}
	return n, err
}
