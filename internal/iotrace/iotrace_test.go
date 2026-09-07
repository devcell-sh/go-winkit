package iotrace

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tracing is off unless asked for, so library code can call Default()
// unconditionally without a nil check and without cost when disabled.
func TestDefault_IsSilentUntilEnabled(t *testing.T) {
	assert.False(t, Default().Enabled(),
		"tracing must be off by default: library code traces unconditionally")

	var buf bytes.Buffer
	restore := SetDefault(NewLogger(&buf))
	defer restore()

	assert.True(t, Default().Enabled())
}

// SetDefault returns a restore func so a caller (or a test) can scope the
// change instead of leaking a global.
func TestSetDefault_RestoreputsThePreviousTracerBack(t *testing.T) {
	var buf bytes.Buffer
	restore := SetDefault(NewLogger(&buf))
	require.True(t, Default().Enabled())

	restore()

	assert.False(t, Default().Enabled(), "restore must undo the override")
}

func TestNop_RecordsNothing(t *testing.T) {
	n := Nop{}
	assert.False(t, n.Enabled())
	n.Trace(Op{Kind: KindWrite, Path: "/tmp/x"}) // must not panic
}

func TestLogger_ReportsKindPathAndSize(t *testing.T) {
	var buf bytes.Buffer
	lg := NewLogger(&buf)

	lg.Trace(Op{Kind: KindWrite, Path: "/tmp/boot.wim", Bytes: 5 << 20})

	out := buf.String()
	assert.Contains(t, out, KindWrite)
	assert.Contains(t, out, "/tmp/boot.wim")
	assert.Contains(t, out, "5.0 MB", "sizes must be human readable at a glance")
}

// A failed operation is the whole reason to turn tracing on, so the error
// has to survive into the output rather than being dropped.
func TestLogger_ReportsErrors(t *testing.T) {
	var buf bytes.Buffer
	lg := NewLogger(&buf)

	lg.Trace(Op{Kind: KindExportImage, Path: "boot.wim", Err: errors.New("duplicate image")})

	out := buf.String()
	assert.Contains(t, out, "duplicate image")
	assert.Contains(t, out, "ERR")
}

// Detail is where an operation says what makes it distinct: which source
// image went to which destination, for instance.
func TestLogger_ReportsDetail(t *testing.T) {
	var buf bytes.Buffer
	lg := NewLogger(&buf)

	lg.Trace(Op{Kind: KindExportImage, Path: "boot.wim", Detail: "src image 2 -> dest"})

	assert.Contains(t, buf.String(), "src image 2 -> dest")
}

func TestJSONLogger_EmitsOneObjectPerOperation(t *testing.T) {
	var buf bytes.Buffer
	jl := NewJSONLogger(&buf)

	jl.Trace(Op{Kind: KindRead, Path: "/a", Bytes: 1})
	jl.Trace(Op{Kind: KindWrite, Path: "/b", Bytes: 2})

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	require.Len(t, lines, 2, "one JSON object per line, so it streams")

	var first map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &first))
	assert.Equal(t, KindRead, first["kind"])
	assert.Equal(t, "/a", first["path"])
}

func TestJSONLogger_SerialisesErrorAsText(t *testing.T) {
	var buf bytes.Buffer
	jl := NewJSONLogger(&buf)

	jl.Trace(Op{Kind: KindWrite, Path: "/a", Err: errors.New("disk full")})

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &got))
	assert.Equal(t, "disk full", got["error"],
		"errors must serialise as text: error values do not survive JSON")
}

// Start times the operation and reports it on completion, so a caller
// wraps an IO call without hand-rolling timing at every site.
func TestStart_TimesAndReportsTheOperation(t *testing.T) {
	var buf bytes.Buffer
	restore := SetDefault(NewLogger(&buf))
	defer restore()

	done := Start(KindWrite, "/tmp/x")
	time.Sleep(2 * time.Millisecond)
	done(1024, nil)

	out := buf.String()
	assert.Contains(t, out, "/tmp/x")
	assert.Contains(t, out, "1.0 KB")
	assert.Regexp(t, `\dm?s`, out, "elapsed time must be reported")
}

func TestStart_ReportsFailure(t *testing.T) {
	var buf bytes.Buffer
	restore := SetDefault(NewLogger(&buf))
	defer restore()

	Start(KindOpenWIM, "/tmp/x.wim")(0, errors.New("no such file"))

	assert.Contains(t, buf.String(), "no such file")
}

// The disabled path must stay allocation-light and side-effect free: it is
// on every IO call site in the library.
func TestStart_IsSafeAndSilentWhenDisabled(t *testing.T) {
	require.False(t, Default().Enabled())

	done := Start(KindRead, "/tmp/x")
	assert.NotPanics(t, func() { done(10, errors.New("ignored")) })
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{5 << 20, "5.0 MB"},
		{3 << 30, "3.0 GB"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, formatBytes(tt.in), "formatBytes(%d)", tt.in)
	}
}
