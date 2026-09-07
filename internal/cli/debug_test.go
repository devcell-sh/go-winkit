package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devcell-sh/go-winkit/internal/iotrace"
	"github.com/devcell-sh/go-winkit/media/isokit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRootCmd_HasDebugFlags(t *testing.T) {
	root := NewRootCmd()
	assert.NotNil(t, root.PersistentFlags().Lookup("debug"))
	assert.NotNil(t, root.PersistentFlags().Lookup("debug-json"))
}

// The flags are persistent so every subcommand is covered without each
// one opting in.
func TestDebugFlag_ReachesSubcommands(t *testing.T) {
	root := NewRootCmd()
	sub, _, err := root.Find([]string{"iso", "extract"})
	require.NoError(t, err)
	assert.NotNil(t, sub.InheritedFlags().Lookup("debug"),
		"subcommands must inherit --debug from the root")
}

// Without the flag, output must be byte-for-byte what it was before
// tracing existed.
func TestWithoutDebug_TracingStaysOff(t *testing.T) {
	restore := iotrace.SetDefault(iotrace.Nop{})
	defer restore()

	_, stderr := runExtract(t, false, false)

	assert.False(t, iotrace.Default().Enabled(), "tracing must remain off")
	assert.NotContains(t, stderr, "io ")
}

func TestDebugFlag_TracesIOToStderr(t *testing.T) {
	restore := iotrace.SetDefault(iotrace.Nop{})
	defer restore()

	stdout, stderr := runExtract(t, true, false)

	assert.Contains(t, stderr, "io "+iotrace.KindRead,
		"reading a member out of an ISO must be traced")
	assert.NotContains(t, stdout, "io ",
		"traces belong on stderr so stdout stays pipeable")
}

func TestDebugJSONFlag_EmitsParseableRecords(t *testing.T) {
	restore := iotrace.SetDefault(iotrace.Nop{})
	defer restore()

	_, stderr := runExtract(t, false, true)

	var found bool
	for _, line := range strings.Split(strings.TrimSpace(stderr), "\n") {
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var rec map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &rec), "line: %s", line)
		if rec["kind"] == iotrace.KindRead {
			found = true
		}
	}
	assert.True(t, found, "a read operation must appear as a JSON record")
}

// runExtract drives a real command that performs IO, so the test exercises
// the wiring rather than the flag definition alone.
func runExtract(t *testing.T, debug, debugJSON bool) (stdout, stderr string) {
	t.Helper()

	dir := t.TempDir()
	isoPath := filepath.Join(dir, "t.iso")
	buildTestISO(t, isoPath)

	args := []string{"iso", "extract", isoPath, "/hello.txt",
		"--out", filepath.Join(dir, "out.txt")}
	if debug {
		args = append(args, "--debug")
	}
	if debugJSON {
		args = append(args, "--debug-json")
	}

	var outBuf, errBuf bytes.Buffer
	root := NewRootCmd()
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		t.Fatalf("iso extract: %v\nstderr: %s", err, errBuf.String())
	}
	return outBuf.String(), errBuf.String()
}

func buildTestISO(t *testing.T, isoPath string) {
	t.Helper()
	// Built through the same package the command reads with, so the
	// fixture cannot drift from the reader.
	require.NoError(t, isokit.CreateSimpleISO(isoPath, map[string][]byte{
		"/hello.txt": []byte("world"),
	}))
	_, err := os.Stat(isoPath)
	require.NoError(t, err)
}
