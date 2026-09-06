package cli

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewLogger_DefaultShowsInfoHidesDebug(t *testing.T) {
	var buf bytes.Buffer
	l := newLogger(&buf, false)
	l.Debug("verbose detail")
	l.Info("milestone")
	out := buf.String()
	assert.NotContains(t, out, "verbose detail")
	assert.Contains(t, out, "milestone")
	assert.NotRegexp(t, `\d{2}:\d{2}:\d{2}`, out, "no timestamps without --debug")
}

func TestNewLogger_DebugShowsLevelsAndTimestamps(t *testing.T) {
	var buf bytes.Buffer
	l := newLogger(&buf, true)
	l.Debug("verbose detail")
	l.Warn("heads up")
	out := buf.String()
	assert.Contains(t, out, "verbose detail")
	assert.Contains(t, out, "DEBU")
	assert.Contains(t, out, "WARN")
	assert.Regexp(t, `\d{2}:\d{2}:\d{2}\.\d{3}`, out)
}
