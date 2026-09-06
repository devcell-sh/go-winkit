package gosshd

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// syncBuffer is a goroutine-safe sink for structured events: handle() emits
// from the session goroutine while the test reads from another.
type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func shellServer(events *syncBuffer) Server {
	return Server{
		User:     DefaultUser,
		Password: DefaultPassword,
		Events:   events,
		command: func(request string) []string {
			if request == "" {
				return []string{"/bin/sh"}
			}
			return []string{"/bin/sh", "-c", request}
		},
	}
}

// The structured event must record the exact command, its exit code, and the
// captured stdout — a 1:1 record of what ran over SSH, independent of the
// live stream to the client.
func TestEvents_RecordsCommandExitAndOutput(t *testing.T) {
	var events syncBuffer
	addr := startTestServer(t, shellServer(&events))

	c, err := Dial(context.Background(), addr)
	require.NoError(t, err)
	defer c.Close()

	stdout, _, code, err := c.Run(context.Background(), "echo winkit-marker")
	require.NoError(t, err)
	require.Equal(t, 0, code)
	require.Contains(t, string(stdout), "winkit-marker")

	c.Close() // ends the session so its event is emitted

	var ev sessionEvent
	require.Eventually(t, func() bool {
		line := lastJSONLine(events.String())
		if line == "" {
			return false
		}
		return json.Unmarshal([]byte(line), &ev) == nil && ev.Event == "ssh_session"
	}, 5e9, 20e6, "structured event never arrived; got: %q", events.String())

	assert.Equal(t, "echo winkit-marker", ev.Command)
	assert.Equal(t, 0, ev.Exit)
	assert.Contains(t, ev.Stdout, "winkit-marker")
	assert.NotEmpty(t, ev.TS, "event must be timestamped")
}

// A non-zero exit must be captured faithfully, alongside stderr.
func TestEvents_RecordsNonZeroExitAndStderr(t *testing.T) {
	var events syncBuffer
	addr := startTestServer(t, shellServer(&events))

	c, err := Dial(context.Background(), addr)
	require.NoError(t, err)
	defer c.Close()

	_, _, code, err := c.Run(context.Background(), "echo oops 1>&2; exit 7")
	require.NoError(t, err)
	require.Equal(t, 7, code)
	c.Close()

	var ev sessionEvent
	require.Eventually(t, func() bool {
		line := lastJSONLine(events.String())
		return line != "" && json.Unmarshal([]byte(line), &ev) == nil && ev.Event == "ssh_session"
	}, 5e9, 20e6)

	assert.Equal(t, 7, ev.Exit)
	assert.Contains(t, ev.Stderr, "oops")
}

// A nil Events writer is the default and must never break a session.
func TestEvents_NilWriterIsSafe(t *testing.T) {
	addr := startTestServer(t, Server{
		User:     DefaultUser,
		Password: DefaultPassword,
		command: func(request string) []string {
			return []string{"/bin/sh", "-c", request}
		},
	})
	c, err := Dial(context.Background(), addr)
	require.NoError(t, err)
	defer c.Close()

	_, _, code, err := c.Run(context.Background(), "echo ok")
	require.NoError(t, err)
	assert.Equal(t, 0, code)
}

// capBuffer keeps at most limit bytes and flags the overflow rather than
// silently claiming complete output.
func TestCapBuffer_TruncatesAndReportsDrop(t *testing.T) {
	c := capBuffer{limit: 10}
	n, err := c.Write([]byte("0123456789ABCDEF"))
	require.NoError(t, err)
	assert.Equal(t, 16, n, "Write reports all bytes consumed even when capped")
	s := c.String()
	assert.True(t, strings.HasPrefix(s, "0123456789"))
	assert.Contains(t, s, "6 bytes truncated")
}

func lastJSONLine(s string) string {
	var last string
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "{") {
			last = strings.TrimSpace(l)
		}
	}
	return last
}
