package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogTailer_IncrementalRead(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "setupact.log")

	require.NoError(t, os.WriteFile(logFile, []byte("line one\nline two\n"), 0o644))

	var buf bytes.Buffer
	tailer := newLogTailer(&buf)
	tailer.tail(logFile)

	lines := nonEmptyLines(buf.String())
	require.Len(t, lines, 2)
	assertJSONField(t, lines[0], "line", "line one")
	assertJSONField(t, lines[1], "line", "line two")

	var buf2 bytes.Buffer
	tailer.out = &buf2
	tailer.tail(logFile)
	assert.Empty(t, buf2.String(), "no new lines, should produce no output")

	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, _ = f.WriteString("line three\n")
	f.Close()

	var buf3 bytes.Buffer
	tailer.out = &buf3
	tailer.tail(logFile)
	lines = nonEmptyLines(buf3.String())
	require.Len(t, lines, 1)
	assertJSONField(t, lines[0], "line", "line three")
}

func TestLogTailer_TruncatedFile(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "setuperr.log")

	require.NoError(t, os.WriteFile(logFile, []byte("aaa\nbbb\nccc\n"), 0o644))

	var buf bytes.Buffer
	tailer := newLogTailer(&buf)
	tailer.tail(logFile)
	require.Len(t, nonEmptyLines(buf.String()), 3)

	require.NoError(t, os.WriteFile(logFile, []byte("new\n"), 0o644))
	buf.Reset()
	tailer.tail(logFile)
	lines := nonEmptyLines(buf.String())
	require.Len(t, lines, 1)
	assertJSONField(t, lines[0], "line", "new")
}

func TestLogTailer_PartialLine(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "setupact.log")

	require.NoError(t, os.WriteFile(logFile, []byte("complete\npartial"), 0o644))

	var buf bytes.Buffer
	tailer := newLogTailer(&buf)
	tailer.tail(logFile)

	lines := nonEmptyLines(buf.String())
	require.Len(t, lines, 1, "partial line without trailing newline should not be emitted")
	assertJSONField(t, lines[0], "line", "complete")
}

func TestLogTailer_CRLFHandling(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "setupact.log")

	require.NoError(t, os.WriteFile(logFile, []byte("windows line\r\n"), 0o644))

	var buf bytes.Buffer
	tailer := newLogTailer(&buf)
	tailer.tail(logFile)

	lines := nonEmptyLines(buf.String())
	require.Len(t, lines, 1)
	assertJSONField(t, lines[0], "line", "windows line")
}

func TestLogTailer_EventName(t *testing.T) {
	for _, tc := range []struct {
		file string
		want string
	}{
		{"setupact.log", "setupact"},
		{"setuperr.log", "setuperr"},
		{"setupapi.dev.log", "setupapi"},
		{"unknown.log", "setup"},
	} {
		assert.Equal(t, tc.want, eventNameFromPath(`X:\Windows\Panther\`+tc.file))
	}
}

func TestFindVolume_NotFound(t *testing.T) {
	assert.Equal(t, "", findVolume())
}

func TestSnapshotLogs(t *testing.T) {
	vol := t.TempDir()
	snapshotLogs(vol, []byte{'X', 'C'})
}

func TestFindTargetDrive_NotFound(t *testing.T) {
	assert.Equal(t, byte(0), findTargetDrive())
}

func TestPantherLogPaths(t *testing.T) {
	paths := pantherLogPaths([]byte{'X', 'I'})
	assert.Contains(t, paths, `X:\Windows\Panther\setupact.log`)
	assert.Contains(t, paths, `I:\Windows\Panther\setupact.log`)
	assert.Equal(t, len(pantherLogSuffixes)*2, len(paths))
}

func TestSnapshotSuffix(t *testing.T) {
	assert.Equal(t, "x-winkit-setupact.log", snapshotSuffix('X', `Windows\Panther\setupact.log`))
	assert.Equal(t, "i-winkit-setuperr.log", snapshotSuffix('I', `Windows\Panther\setuperr.log`))
	assert.Equal(t, "c-winkit-setupapi.dev.log", snapshotSuffix('C', `Windows\INF\setupapi.dev.log`))
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}

func assertJSONField(t *testing.T, jsonLine, key, want string) {
	t.Helper()
	var m map[string]string
	require.NoError(t, json.Unmarshal([]byte(jsonLine), &m), "invalid JSON: %s", jsonLine)
	assert.Equal(t, want, m[key])
}
