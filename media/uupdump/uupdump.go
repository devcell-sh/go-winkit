package uupdump

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/devcell-sh/go-winkit/cache"
	"github.com/devcell-sh/go-winkit/media/isokit"
)

type FetchConfig struct {
	CacheDir    string
	Concurrency int

	// Spec names the media to fetch along orthogonal axes; zero values
	// resolve to the defaults (Windows 11 24H2 arm64 en-us PROFESSIONAL).
	Spec MediaSpec

	// Language, Edition and Version predate Spec and fold into it when
	// the corresponding Spec field is empty.
	Language string
	Edition  string
	Version  string

	OnProgress ProgressFunc

	// OnFileStart / OnFileDone bracket each ESD download; see
	// DownloadConfig.
	OnFileStart func(filename string)
	OnFileDone  func(filename string, size int64)

	// Logger receives leveled progress output (milestones at Info,
	// detail at Debug). LogFunc is the level-less predecessor, used
	// only when Logger is nil; it receives everything.
	Logger  *slog.Logger
	LogFunc func(format string, args ...any)

	// Assembly tuning, passed straight through to AssembleISO. Zero
	// values keep the formats and rebuild behaviour of shipping media.
	BootWimCompression    Compression
	InstallWimCompression Compression
	ReuseBootWim          bool

	// BootOnly skips install.wim creation: the ISO carries boot.wim and
	// setup media only. Use when the source ESDs cannot produce a
	// complete install image (the common case for UUP dump checkpoint
	// builds whose payload lives in cabs the assembler cannot consume).
	BootOnly bool
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

func FetchWindowsISO(ctx context.Context, cfg FetchConfig) (string, error) {
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 5
	}
	// An unset CacheDir falls back to the shared default rather than
	// failing, so every fetcher behaves the same way and a library caller
	// can opt out of the environment by setting it explicitly.
	if cfg.CacheDir == "" {
		cfg.CacheDir = cache.Dir()
	}

	spec := cfg.Spec
	if spec.Language == "" {
		spec.Language = cfg.Language
	}
	if spec.Edition == "" {
		spec.Edition = cfg.Edition
	}
	if spec.Version == "" && spec.Build == "" {
		spec.Version = cfg.Version
	}
	r, err := spec.Resolve()
	if err != nil {
		return "", err
	}
	cfg.Language, cfg.Edition = r.Language, r.Edition

	isoPath := filepath.Join(cfg.CacheDir, r.ISOName())

	// The legacy pre-spec cache name is still a valid hit for the default
	// spec — nothing else could have produced it.
	cachedPaths := []string{isoPath}
	if def, _ := (MediaSpec{}).Resolve(); r == def {
		cachedPaths = append(cachedPaths, filepath.Join(cfg.CacheDir, cache.WindowsISOName))
	}
	for _, p := range cachedPaths {
		info, statErr := os.Stat(p)
		if statErr != nil || info.Size() == 0 {
			continue
		}
		// A mastering failure can orphan a non-bootable image here (run
		// 20260812T090917: raw hdiutil -udf output with no El Torito). Only a
		// firmware-bootable image is a valid cache hit.
		if bootErr := isokit.RequireEFIBootable(p); bootErr != nil {
			cfg.warnf("existing ISO is unusable (%v) — rebuilding", bootErr)
			os.Remove(p)
			continue
		}
		cfg.logf("ISO already cached (%.1f GB)", float64(info.Size())/(1024*1024*1024))
		cfg.debugf("cached ISO: %s (%d bytes)", p, info.Size())
		return p, nil
	}

	client := NewClient()

	cfg.logf("searching %s builds for %s", r.Arch, describeSpec(r))
	candidates, err := client.FindCandidates(ctx, r)
	if err != nil {
		return "", fmt.Errorf("finding %s build: %w", r.Arch, err)
	}

	var matched []Build
	for _, b := range candidates {
		if r.MatchesBuild(b) {
			matched = append(matched, b)
		}
	}
	if len(matched) == 0 {
		return "", fmt.Errorf("no %s builds match %s (candidates: %s)",
			r.Arch, describeSpec(r), summarizeBuilds(candidates))
	}

	kept, skipped := r.FilterGA(matched)
	for _, b := range skipped {
		cfg.debugf("skipping pre-release build %s (%s)", b.Build, b.Title)
	}
	if len(skipped) > 0 {
		cfg.logf("skipped %d pre-release builds (ESD assembly needs GA media; pass --build to force one)",
			len(skipped))
	}
	if len(kept) == 0 {
		return "", fmt.Errorf("every build matching %s is pre-release and was skipped "+
			"(ESD assembly needs GA media; pass --build to force): %s",
			describeSpec(r), summarizeBuilds(matched))
	}

	// The newest build is not always assemblable even on the GA table —
	// the only way to know is to try. Fall back through the ladder.
	const maxAttempts = 4
	var lastErr error
	for i, build := range kept {
		if i >= maxAttempts {
			break
		}
		if lastErr != nil {
			cfg.warnf("falling back to previous build after failure: %v", lastErr)
		}
		cfg.logf("found %s", foundBuildMsg(build))
		if err := fetchBuild(ctx, client, &build, cfg, isoPath); err != nil {
			lastErr = fmt.Errorf("build %s: %w", build.Build, err)
			continue
		}
		refreshLegacyAlias(cfg.CacheDir, isoPath, cfg.logf)
		return isoPath, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no assemblable build found for %s", describeSpec(r))
	}
	return "", lastErr
}

// esdDisplayName strips the .esd extension (either case) for display.
func esdDisplayName(name string) string {
	if strings.HasSuffix(strings.ToLower(name), ".esd") {
		return name[:len(name)-4]
	}
	return name
}

// foundBuildMsg renders a build for the log without repeating the build
// number when the title already carries it.
func foundBuildMsg(b Build) string {
	if strings.Contains(b.Title, b.Build) {
		return b.Title
	}
	return fmt.Sprintf("%s (%s)", b.Title, b.Build)
}

// describeSpec renders the deciding axes for log and error text.
func describeSpec(r ResolvedSpec) string {
	name := "Windows " + r.Product
	switch {
	case r.Build != "":
		return fmt.Sprintf("%s build %s", name, r.Build)
	case r.Version != "":
		return fmt.Sprintf("%s %s (%s.x)", name, r.Version, r.Series)
	case r.Series != "":
		return fmt.Sprintf("%s series %s", name, r.Series)
	default:
		return name + " latest"
	}
}

// refreshLegacyAlias keeps the pre-spec cache name pointing at the most
// recently fetched ISO so older tooling and the boot tests keep finding
// one. A real file at the legacy name is left alone — it is a user's
// pre-migration download, not ours to delete.
func refreshLegacyAlias(cacheDir, isoPath string, logf func(string, ...any)) {
	legacy := filepath.Join(cacheDir, cache.WindowsISOName)
	if legacy == isoPath {
		return
	}
	if info, err := os.Lstat(legacy); err == nil && info.Mode()&os.ModeSymlink == 0 {
		return
	}
	os.Remove(legacy)
	if err := os.Symlink(filepath.Base(isoPath), legacy); err != nil {
		logf("could not refresh legacy alias %s: %v", legacy, err)
	}
}

// fetchBuild downloads one build's ESD set into a build-scoped directory
// and assembles the ISO from it. The per-build directory matters: ESD
// filenames repeat across builds (professional_en-us.esd), so a shared
// directory would silently reuse another build's blobs on fallback.
func fetchBuild(ctx context.Context, client *Client, build *Build, cfg FetchConfig, isoPath string) error {
	cfg.debugf("fetching package for edition=%s lang=%s", cfg.Edition, cfg.Language)
	pkg, err := client.GetPackage(ctx, build.UUID, cfg.Language, []string{cfg.Edition})
	if err != nil {
		return fmt.Errorf("getting package: %w", err)
	}
	cfg.debugf("package has %d files", len(pkg.Files))

	esdFiles := map[string]File{}
	var esdNames []string
	var totalESDSize int64
	for name, f := range pkg.Files {
		if strings.HasSuffix(strings.ToLower(name), ".esd") {
			esdFiles[name] = f
			esdNames = append(esdNames, name)
			totalESDSize += f.Size
		}
	}
	cfg.debugf("ESD files in package: %d (total %.1f MB)", len(esdFiles), float64(totalESDSize)/(1024*1024))
	cfg.debugf("ESD file list: %v", esdNames)
	// Say out loud what is being thrown away. AssembleISO only speaks ESD, so
	// dropping the rest is consistent — but for the current ARM64 build it is
	// 45 of 65 files and 4.2 GB, including the cumulative update and the FoD
	// payloads a later provisioning step will then fail to find (CELL-385).
	cfg.debugf("%s", SummarizeSkipped(pkg.Files, ".esd"))

	installESD := findESD(pkg.Files, cfg.Edition, cfg.Language)
	if installESD == "" {
		return fmt.Errorf("no install ESD found in package (ESD files: %v)", esdNames)
	}
	cfg.debugf("install ESD: %s", installESD)

	downloadDir := filepath.Join(cfg.CacheDir, "uupdump-download", build.Build)
	if err := os.MkdirAll(downloadDir, 0o755); err != nil {
		return fmt.Errorf("creating download dir: %w", err)
	}

	cfg.logf("fetching %d ESDs (%s, %.1f GB)",
		len(esdFiles), esdDisplayName(installESD), float64(totalESDSize)/(1024*1024*1024))
	cfg.debugf("download dir: %s", downloadDir)
	results, err := DownloadFiles(ctx, esdFiles, DownloadConfig{
		Dir:         downloadDir,
		Concurrency: cfg.Concurrency,
		OnProgress:  cfg.OnProgress,
		OnFileStart: cfg.OnFileStart,
		OnFileDone:  cfg.OnFileDone,
	})
	if err != nil {
		return fmt.Errorf("downloading ESD files: %w", err)
	}

	var downloadedSize int64
	for _, r := range results {
		downloadedSize += r.Size
		cfg.debugf("  %s: %.1f MB", r.Filename, float64(r.Size)/(1024*1024))
	}
	cfg.debugf("all ESDs downloaded: %.1f MB total", float64(downloadedSize)/(1024*1024))

	esdPath := filepath.Join(downloadDir, installESD)
	if info, err := os.Stat(esdPath); err != nil {
		return fmt.Errorf("install ESD missing after download: %w", err)
	} else {
		cfg.debugf("install ESD on disk: %s (%.1f MB)", esdPath, float64(info.Size())/(1024*1024))
	}

	workDir := filepath.Join(cfg.CacheDir, "uupdump-work")
	refGlob := filepath.Join(downloadDir, "*.esd")

	cfg.debugf("assembling ISO from ESD (ref=%s)", refGlob)
	if err := AssembleISO(ctx, esdPath, AssembleConfig{
		WorkDir: workDir,
		ISOPath: isoPath,
		Label:   fmt.Sprintf("W11_%s", strings.ToUpper(cfg.Language)),
		RefESDs: []string{refGlob},
		Logger:  cfg.Logger,
		LogFunc: cfg.LogFunc,

		BootWimCompression:    cfg.BootWimCompression,
		InstallWimCompression: cfg.InstallWimCompression,
		ReuseBootWim:          cfg.ReuseBootWim,
		BootOnly:              cfg.BootOnly,
	}); err != nil {
		return fmt.Errorf("assembling ISO: %w", err)
	}

	return nil
}

func summarizeBuilds(builds []Build) string {
	var parts []string
	for i, b := range builds {
		if i >= 6 {
			parts = append(parts, "...")
			break
		}
		parts = append(parts, b.Title)
	}
	return strings.Join(parts, "; ")
}

func findESD(files map[string]File, edition, language string) string {
	edLower := strings.ToLower(edition)
	langLower := strings.ToLower(language)

	for name := range files {
		lower := strings.ToLower(name)
		if strings.HasSuffix(lower, ".esd") && strings.Contains(lower, edLower) && strings.Contains(lower, langLower) {
			return name
		}
	}

	for name := range files {
		lower := strings.ToLower(name)
		if strings.HasSuffix(lower, ".esd") && strings.Contains(lower, edLower) {
			return name
		}
	}

	for name := range files {
		lower := strings.ToLower(name)
		if strings.HasSuffix(lower, ".esd") && !strings.Contains(lower, "metadata") {
			return name
		}
	}
	return ""
}
