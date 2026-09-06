package cli

import (
	"bytes"
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
