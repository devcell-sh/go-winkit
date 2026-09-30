// Package media is winkit's install-media fetch policy: which source serves
// the Windows installer ISO, in what order, and with what fallback. It sits
// above the per-source packages (mctcatalog, uupdump, virtio), which each
// know how to fetch from one place but not which place to try first.
//
// The winkit CLI calls these same functions, so a library consumer gets the
// exact media the CLI would use for the same options.
package media

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/devcell-sh/go-winkit/cache"
	"github.com/devcell-sh/go-winkit/media/mctcatalog"
	"github.com/devcell-sh/go-winkit/media/uupdump"
	"github.com/devcell-sh/go-winkit/media/virtio"
)

// Spec names Windows installation media along orthogonal axes (OS,
// product, arch, version, build, edition, language). Every field is
// optional; zero values resolve to Windows 11 24H2 arm64 en-us
// Professional. It is uupdump.MediaSpec, re-exported so callers of this
// package need not import the source packages.
type Spec = uupdump.MediaSpec

// Source identifies which pipeline produced a Windows ISO.
type Source string

const (
	// SourceMCT is the Media Creation Tool catalog: complete,
	// self-contained install media for the current GA build.
	SourceMCT Source = "mct"
	// SourceUUPDump is UUP dump, used for pinned builds and as the
	// fallback when the MCT catalog is unavailable.
	SourceUUPDump Source = "uupdump"
)

// FetchOptions configures FetchWindowsISO and FetchVirtioISO. Every field
// is optional.
type FetchOptions struct {
	// CacheDir holds the assembled ISOs and the intermediate downloads.
	// Empty means cache.Dir().
	CacheDir string

	// Spec selects the Windows media. A build pin (Spec.Build, or a build
	// number in Spec.Version) routes straight to UUP dump. Ignored by
	// FetchVirtioISO.
	Spec Spec

	// Concurrency is the number of parallel UUP dump downloads. Zero
	// means the uupdump default.
	Concurrency int

	// OnProgress reports per-file download progress from whichever
	// source runs. total is 0 when the size is unknown. FetchVirtioISO
	// reports under cache.VirtIOISOName.
	OnProgress func(filename string, downloaded, total int64)

	// OnFileStart / OnFileDone bracket each UUP dump ESD download. The
	// MCT catalog fetches a single ESD and does not call them.
	OnFileStart func(filename string)
	OnFileDone  func(filename string, size int64)

	// Logger receives leveled progress output, including the lane
	// decisions made here. LogFunc is the level-less alternative, used
	// only when Logger is nil.
	Logger  *slog.Logger
	LogFunc func(format string, args ...any)
}

// FetchResult describes the Windows ISO FetchWindowsISO produced.
type FetchResult struct {
	// Path is the cached, EFI-bootable ISO.
	Path string
	// Source is the pipeline that produced Path.
	Source Source
	// BootOnly reports that the ISO carries boot.wim and setup media but
	// no install.wim. That suffices for WinPE builds; a full install
	// needs complete media.
	BootOnly bool
	// FallbackErr is the MCT catalog failure that forced the UUP dump
	// fallback. It is nil when MCT served the ISO or a build pin skipped
	// the catalog.
	FallbackErr error
}

// Seams for tests: the network-bound fetchers this policy routes between.
var (
	fetchMCT     = mctcatalog.FetchWindowsISO
	fetchUUPDump = uupdump.FetchWindowsISO
	fetchVirtio  = virtio.FetchISO
)

// FetchWindowsISO returns a cached Windows installer ISO for opts.Spec,
// downloading and assembling it when missing.
//
// Lane routing: the default (no build pin) fetches from the MCT catalog,
// the only source that produces a complete, self-contained install image.
// A build pin routes to UUP dump in boot-only mode, since a pinned build's
// ESDs cannot produce a complete install.wim but boot.wim always exports
// clean. If the MCT catalog fails, the default lane falls back to UUP dump
// boot-only with a warning.
func FetchWindowsISO(ctx context.Context, opts FetchOptions) (FetchResult, error) {
	spec := opts.Spec
	r, err := spec.Resolve()
	if err != nil {
		return FetchResult{}, fmt.Errorf("resolving media spec: %w", err)
	}

	if r.BuildPinned {
		opts.info("build pinned: using uupdump lane (boot-only)", "build", spec.Build)
		return opts.fetchUUPDump(ctx, nil)
	}

	opts.info("using mct lane (complete install media)")
	lang := spec.Language
	if lang == "" {
		lang = "en-us"
	}
	edition := spec.Edition
	if edition == "" {
		edition = "Professional"
	}
	isoPath, err := fetchMCT(ctx, mctcatalog.FetchConfig{
		CacheDir:   opts.CacheDir,
		Language:   lang,
		Edition:    edition,
		LogFunc:    opts.infof,
		OnProgress: opts.OnProgress,
	})
	if err == nil {
		return FetchResult{Path: isoPath, Source: SourceMCT}, nil
	}

	opts.warn("mct catalog unavailable, falling back to uupdump boot-only", "error", err)
	return opts.fetchUUPDump(ctx, err)
}

// fetchUUPDump runs the boot-only UUP dump lane. fallbackErr is the MCT
// failure that led here, recorded on the result.
func (o *FetchOptions) fetchUUPDump(ctx context.Context, fallbackErr error) (FetchResult, error) {
	isoPath, err := fetchUUPDump(ctx, uupdump.FetchConfig{
		CacheDir:    o.CacheDir,
		Spec:        o.Spec,
		Concurrency: o.Concurrency,
		OnProgress:  o.OnProgress,
		OnFileStart: o.OnFileStart,
		OnFileDone:  o.OnFileDone,
		Logger:      o.Logger,
		LogFunc:     o.LogFunc,
		BootOnly:    true,
	})
	if err != nil {
		return FetchResult{}, fmt.Errorf("fetching Windows ISO: %w", err)
	}
	return FetchResult{Path: isoPath, Source: SourceUUPDump, BootOnly: true, FallbackErr: fallbackErr}, nil
}

// FetchVirtioISO returns the cached virtio-win driver ISO, downloading it
// when missing or unusable. It delegates to virtio.FetchISO; only
// CacheDir, OnProgress and the logging fields of opts apply. Call
// virtio.FetchISO directly to override the download URL or HTTP client.
func FetchVirtioISO(ctx context.Context, opts FetchOptions) (string, error) {
	cfg := virtio.FetchConfig{
		CacheDir: opts.CacheDir,
		Logger:   opts.Logger,
		LogFunc:  opts.LogFunc,
	}
	if opts.OnProgress != nil {
		cfg.OnProgress = func(downloaded, total int64) {
			opts.OnProgress(cache.VirtIOISOName, downloaded, total)
		}
	}
	return fetchVirtio(ctx, cfg)
}

func (o *FetchOptions) info(msg string, attrs ...any) {
	if o.Logger != nil {
		o.Logger.Info(msg, attrs...)
		return
	}
	if o.LogFunc != nil {
		o.LogFunc("%s", withAttrs(msg, attrs))
	}
}

func (o *FetchOptions) warn(msg string, attrs ...any) {
	if o.Logger != nil {
		o.Logger.Warn(msg, attrs...)
		return
	}
	if o.LogFunc != nil {
		o.LogFunc("WARNING: %s", withAttrs(msg, attrs))
	}
}

// infof adapts the level-less printf hook the MCT fetcher takes: with a
// Logger its output lands at Info, otherwise it goes to LogFunc as-is.
func (o *FetchOptions) infof(format string, args ...any) {
	if o.Logger != nil {
		o.Logger.Info(fmt.Sprintf(format, args...))
		return
	}
	if o.LogFunc != nil {
		o.LogFunc(format, args...)
	}
}

// withAttrs renders slog-style key/value pairs onto msg for LogFunc,
// which has no structured form.
func withAttrs(msg string, attrs []any) string {
	if len(attrs) == 0 {
		return msg
	}
	var b strings.Builder
	b.WriteString(msg)
	rec := slog.NewRecord(time.Time{}, slog.LevelInfo, "", 0)
	rec.Add(attrs...)
	rec.Attrs(func(a slog.Attr) bool {
		fmt.Fprintf(&b, " %s=%s", a.Key, a.Value)
		return true
	})
	return b.String()
}
