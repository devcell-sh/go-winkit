package cli

import (
	"bytes"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/devcell-sh/go-winkit/winpe"
)

// The host log's JSONL mode must speak the exact schema the guest's
// build.jsonl uses, so winpe.ParseGuestEvents reads both.
func TestGuestEventHandler_RoundTripsThroughGuestParser(t *testing.T) {
	var buf bytes.Buffer
	l := newLoggerJSON(&buf)
	l.Info("found build", "stage", "search")
	l.Warn("falling back")
	l.Error("assembly failed", "error", "file resource missing")

	events, skipped, err := winpe.ParseGuestEvents(&buf)
	require.NoError(t, err)
	assert.Zero(t, skipped)
	require.Len(t, events, 3)

	assert.Equal(t, "log", events[0].Event)
	assert.Equal(t, "info", events[0].Status)
	assert.Equal(t, "found build", events[0].Line)
	assert.Equal(t, "search", events[0].Stage)
	assert.False(t, events[0].TS.IsZero())

	assert.Equal(t, "warn", events[1].Status)

	assert.Equal(t, "error", events[2].Status)
	assert.Equal(t, "file resource missing", events[2].Error)
}

func TestMultiHandler_FansOutToFileAndDisplay(t *testing.T) {
	var display, file bytes.Buffer
	displayH := slog.NewTextHandler(&display, &slog.HandlerOptions{Level: slog.LevelInfo})
	fileH := newGuestEventHandler(&file)

	logger := slog.New(multiHandler{handlers: []slog.Handler{displayH, fileH}})
	logger.Info("hello", "key", "val")
	logger.Debug("skipped by display")

	assert.Contains(t, display.String(), "hello")
	assert.NotContains(t, display.String(), "skipped by display")

	events, _, err := winpe.ParseGuestEvents(&file)
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, "hello", events[0].Line)
	assert.Equal(t, "skipped by display", events[1].Line)
}

func TestAttachLogFile_WritesStructuredJSONL(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "host.jsonl")

	ui := &runUI{Logger: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), w: &bytes.Buffer{}}
	require.NoError(t, ui.AttachLogFile(logPath))

	ui.Logger.Info("vm started", "pid", 1234)
	ui.Logger.Info("vm stopped")
	ui.Finish(nil)

	events, _, err := winpe.ReadGuestEvents(logPath)
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, "vm started", events[0].Line)
	assert.Equal(t, "vm stopped", events[1].Line)
}
