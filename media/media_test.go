package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"github.com/devcell-sh/go-winkit/cache"
	"github.com/devcell-sh/go-winkit/media/mctcatalog"
	"github.com/devcell-sh/go-winkit/media/uupdump"
	"github.com/devcell-sh/go-winkit/media/virtio"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeFetchers swaps the three network-bound fetchers for recorders so the
// lane routing can be exercised without touching the network. calls holds
// the lane names in the order they ran.
type fakeFetchers struct {
	calls  []string
	mct    mctcatalog.FetchConfig
	uup    uupdump.FetchConfig
	virtio virtio.FetchConfig

	mctPath, uupPath, virtioPath string
	mctErr, uupErr, virtioErr    error
}

func installFakes(t *testing.T) *fakeFetchers {
	t.Helper()
	f := &fakeFetchers{
		mctPath:    "/cache/windows-11-mct-arm64-en-us.iso",
		uupPath:    "/cache/windows-11-24h2-arm64-en-us.iso",
		virtioPath: "/cache/virtio-win.iso",
	}
	origMCT, origUUP, origVirtio := fetchMCT, fetchUUPDump, fetchVirtio
	t.Cleanup(func() { fetchMCT, fetchUUPDump, fetchVirtio = origMCT, origUUP, origVirtio })

	fetchMCT = func(_ context.Context, cfg mctcatalog.FetchConfig) (string, error) {
		f.calls = append(f.calls, "mct")
		f.mct = cfg
		if f.mctErr != nil {
			return "", f.mctErr
		}
		return f.mctPath, nil
	}
	fetchUUPDump = func(_ context.Context, cfg uupdump.FetchConfig) (string, error) {
		f.calls = append(f.calls, "uupdump")
		f.uup = cfg
		if f.uupErr != nil {
			return "", f.uupErr
		}
		return f.uupPath, nil
	}
	fetchVirtio = func(_ context.Context, cfg virtio.FetchConfig) (string, error) {
		f.calls = append(f.calls, "virtio")
		f.virtio = cfg
		if f.virtioErr != nil {
			return "", f.virtioErr
		}
		return f.virtioPath, nil
	}
	return f
}

// The MCT catalog is the only source of complete, self-contained install
// media, so an unpinned spec must go there and stop on success.
func TestFetchWindowsISO_DefaultLaneUsesMCTOnly(t *testing.T) {
	f := installFakes(t)

	res, err := FetchWindowsISO(context.Background(), FetchOptions{CacheDir: "/cache"})

	require.NoError(t, err)
	assert.Equal(t, []string{"mct"}, f.calls, "a working MCT catalog must not fall through to UUP dump")
	assert.Equal(t, FetchResult{Path: f.mctPath, Source: SourceMCT}, res)
	assert.Equal(t, "/cache", f.mct.CacheDir)
	assert.Equal(t, "en-us", f.mct.Language, "empty language defaults like the CLI")
	assert.Equal(t, "Professional", f.mct.Edition, "empty edition defaults to the MCT spelling")
}

func TestFetchWindowsISO_ForwardsLanguageAndEditionToMCT(t *testing.T) {
	f := installFakes(t)

	_, err := FetchWindowsISO(context.Background(), FetchOptions{
		Spec: Spec{Language: "de-de", Edition: "Enterprise"},
	})

	require.NoError(t, err)
	assert.Equal(t, "de-de", f.mct.Language)
	assert.Equal(t, "Enterprise", f.mct.Edition)
}

// When the catalog is unreachable the default lane still has to produce
// bootable media: UUP dump in boot-only mode, after MCT, never before.
func TestFetchWindowsISO_FallsBackToUUPDumpBootOnlyWhenMCTFails(t *testing.T) {
	f := installFakes(t)
	f.mctErr = errors.New("catalog unreachable")
	spec := Spec{Language: "en-gb", Version: "24H2"}

	res, err := FetchWindowsISO(context.Background(), FetchOptions{
		CacheDir:    "/cache",
		Spec:        spec,
		Concurrency: 3,
	})

	require.NoError(t, err)
	assert.Equal(t, []string{"mct", "uupdump"}, f.calls)
	assert.Equal(t, f.uupPath, res.Path)
	assert.Equal(t, SourceUUPDump, res.Source)
	assert.True(t, res.BootOnly, "the fallback yields boot.wim-only media")
	assert.ErrorIs(t, res.FallbackErr, f.mctErr, "the caller must be able to see why MCT was skipped")

	assert.True(t, f.uup.BootOnly)
	assert.Equal(t, "/cache", f.uup.CacheDir)
	assert.Equal(t, spec, f.uup.Spec)
	assert.Equal(t, 3, f.uup.Concurrency)
}

// A pinned build is something only UUP dump can serve (MCT publishes the
// current GA build only), so the catalog must not even be queried.
func TestFetchWindowsISO_BuildPinSkipsMCT(t *testing.T) {
	f := installFakes(t)

	res, err := FetchWindowsISO(context.Background(), FetchOptions{Spec: Spec{Build: "26100"}})

	require.NoError(t, err)
	assert.Equal(t, []string{"uupdump"}, f.calls)
	assert.Equal(t, FetchResult{Path: f.uupPath, Source: SourceUUPDump, BootOnly: true}, res)
	assert.True(t, f.uup.BootOnly)
	assert.Equal(t, "26100", f.uup.Spec.Build)
}

// A build number passed as --version is still a pin (MediaSpec.Resolve
// back-compat), so it must route the same way as --build.
func TestFetchWindowsISO_BuildNumberAsVersionIsAPin(t *testing.T) {
	f := installFakes(t)

	_, err := FetchWindowsISO(context.Background(), FetchOptions{Spec: Spec{Version: "26100.1742"}})

	require.NoError(t, err)
	assert.Equal(t, []string{"uupdump"}, f.calls)
}

func TestFetchWindowsISO_BothLanesFailingWrapsTheUUPDumpError(t *testing.T) {
	f := installFakes(t)
	f.mctErr = errors.New("catalog unreachable")
	f.uupErr = errors.New("uupdump unreachable")

	_, err := FetchWindowsISO(context.Background(), FetchOptions{})

	require.Error(t, err)
	assert.ErrorIs(t, err, f.uupErr)
	assert.Contains(t, err.Error(), "fetching Windows ISO",
		"the CLI and its tests key on this prefix")
}

// Spec validation happens before any download so a typo never costs a
// multi-GB transfer.
func TestFetchWindowsISO_InvalidSpecFailsBeforeAnyFetch(t *testing.T) {
	f := installFakes(t)

	_, err := FetchWindowsISO(context.Background(), FetchOptions{Spec: Spec{Arch: "x64"}})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "resolving media spec")
	assert.Empty(t, f.calls)
}

// Progress and per-file hooks are the observer surface consumers drive
// their UI from; they must reach whichever fetcher runs.
func TestFetchWindowsISO_ForwardsProgressAndFileHooks(t *testing.T) {
	f := installFakes(t)
	f.mctErr = errors.New("catalog unreachable")

	var progress, started, done []string
	opts := FetchOptions{
		OnProgress: func(name string, downloaded, total int64) {
			progress = append(progress, fmt.Sprintf("%s %d/%d", name, downloaded, total))
		},
		OnFileStart: func(name string) { started = append(started, name) },
		OnFileDone:  func(name string, _ int64) { done = append(done, name) },
	}

	_, err := FetchWindowsISO(context.Background(), opts)
	require.NoError(t, err)

	require.NotNil(t, f.mct.OnProgress)
	f.mct.OnProgress("mct.esd", 1, 2)
	require.NotNil(t, f.uup.OnProgress)
	f.uup.OnProgress("uup.esd", 3, 4)
	require.NotNil(t, f.uup.OnFileStart)
	f.uup.OnFileStart("uup.esd")
	require.NotNil(t, f.uup.OnFileDone)
	f.uup.OnFileDone("uup.esd", 4)

	assert.Equal(t, []string{"mct.esd 1/2", "uup.esd 3/4"}, progress)
	assert.Equal(t, []string{"uup.esd"}, started)
	assert.Equal(t, []string{"uup.esd"}, done)
}

// With a Logger the lane decisions are leveled, structured records (the
// CLI's build log depends on it), and the MCT fetcher's level-less output
// lands at Info.
func TestFetchWindowsISO_LogsLaneDecisionsToLogger(t *testing.T) {
	f := installFakes(t)
	f.mctErr = errors.New("catalog unreachable")
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	_, err := FetchWindowsISO(context.Background(), FetchOptions{Logger: logger})
	require.NoError(t, err)
	require.NotNil(t, f.mct.LogFunc)
	f.mct.LogFunc("searching %s", "catalog")

	out := buf.String()
	assert.Contains(t, out, `level=INFO msg="using mct lane (complete install media)"`)
	assert.Contains(t, out, `level=WARN msg="mct catalog unavailable, falling back to uupdump boot-only" error="catalog unreachable"`)
	assert.Contains(t, out, `level=INFO msg="searching catalog"`)
	assert.Same(t, logger, f.uup.Logger, "UUP dump levels its own output")
}

// Consumers without slog (devcell's Observer) get the same decisions
// through the printf-style LogFunc.
func TestFetchWindowsISO_LogFuncReceivesLaneDecisions(t *testing.T) {
	f := installFakes(t)
	f.mctErr = errors.New("catalog unreachable")
	var lines []string
	logf := func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) }

	_, err := FetchWindowsISO(context.Background(), FetchOptions{LogFunc: logf})
	require.NoError(t, err)

	assert.Contains(t, lines, "using mct lane (complete install media)")
	assert.Contains(t, lines, "WARNING: mct catalog unavailable, falling back to uupdump boot-only error=catalog unreachable")
	require.NotNil(t, f.mct.LogFunc)
	require.NotNil(t, f.uup.LogFunc)
}

func TestFetchVirtioISO_DelegatesToVirtioFetchISO(t *testing.T) {
	f := installFakes(t)
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	var progress []string

	path, err := FetchVirtioISO(context.Background(), FetchOptions{
		CacheDir: "/cache",
		Logger:   logger,
		OnProgress: func(name string, downloaded, total int64) {
			progress = append(progress, fmt.Sprintf("%s %d/%d", name, downloaded, total))
		},
	})

	require.NoError(t, err)
	assert.Equal(t, []string{"virtio"}, f.calls, "virtio fetching must not touch the Windows lanes")
	assert.Equal(t, f.virtioPath, path)
	assert.Equal(t, "/cache", f.virtio.CacheDir)
	assert.Same(t, logger, f.virtio.Logger)

	require.NotNil(t, f.virtio.OnProgress)
	f.virtio.OnProgress(5, 10)
	assert.Equal(t, []string{cache.VirtIOISOName + " 5/10"}, progress,
		"one OnProgress callback serves both fetches, named by file")
}

func TestFetchVirtioISO_ReturnsTheFetchError(t *testing.T) {
	f := installFakes(t)
	f.virtioErr = errors.New("HTTP 503")

	_, err := FetchVirtioISO(context.Background(), FetchOptions{})

	assert.ErrorIs(t, err, f.virtioErr)
}
