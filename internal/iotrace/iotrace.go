// Package iotrace records the IO a build performs: files opened and
// written, WIM images exported, images mastered, bytes downloaded.
//
// Building Windows media is mostly opaque IO against large binaries, so
// when a step fails the useful question is usually "what did it actually
// touch, in what order?". A log line per operation answers that; without
// one the only evidence is a single error from deep inside wimlib.
//
// Tracing is off by default and costs a nil-ish interface call when
// disabled, so library code can trace unconditionally. Callers turn it on
// with SetDefault, which the winkit CLI does behind --debug.
package iotrace

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"
)

// Operation kinds. They are strings rather than an enum so a JSONL trace
// stays readable without a decoder ring.
const (
	KindRead        = "read"
	KindWrite       = "write"
	KindMkdir       = "mkdir"
	KindRemove      = "remove"
	KindOpenWIM     = "open-wim"
	KindCreateWIM   = "create-wim"
	KindExportImage = "export-image"
	KindExtract     = "extract"
	KindUpdateWIM   = "update-wim"
	KindWriteWIM    = "write-wim"
	KindRefISO      = "reference-esd"
	KindCreateISO   = "create-iso"
	KindCreateFAT   = "create-fat"
	KindHTTP        = "http"
)

// Op is a single IO operation.
type Op struct {
	Kind    string        `json:"kind"`
	Path    string        `json:"path,omitempty"`
	Bytes   int64         `json:"bytes,omitempty"`
	Detail  string        `json:"detail,omitempty"`
	Elapsed time.Duration `json:"elapsed_ms,omitempty"`
	Err     error         `json:"-"`
}

// Tracer records operations.
type Tracer interface {
	Trace(Op)
	// Enabled reports whether anything is recorded. Call sites use it to
	// skip building expensive Detail strings when tracing is off.
	Enabled() bool
}

// Nop discards everything and is the default.
type Nop struct{}

func (Nop) Trace(Op)      {}
func (Nop) Enabled() bool { return false }

var (
	mu      sync.RWMutex
	current Tracer = Nop{}
)

// Default returns the active tracer. It is never nil.
func Default() Tracer {
	mu.RLock()
	defer mu.RUnlock()
	return current
}

// SetDefault installs t and returns a function restoring the previous
// tracer, so a caller can scope the change rather than leak a global.
func SetDefault(t Tracer) (restore func()) {
	if t == nil {
		t = Nop{}
	}
	mu.Lock()
	prev := current
	current = t
	mu.Unlock()

	return func() {
		mu.Lock()
		current = prev
		mu.Unlock()
	}
}

// Logger writes one human-readable line per operation.
type Logger struct {
	mu sync.Mutex
	w  io.Writer
}

func NewLogger(w io.Writer) *Logger { return &Logger{w: w} }

func (l *Logger) Enabled() bool { return true }

func (l *Logger) Trace(op Op) {
	l.mu.Lock()
	defer l.mu.Unlock()

	line := fmt.Sprintf("io %-14s %s", op.Kind, op.Path)
	if op.Bytes > 0 {
		line += " " + formatBytes(op.Bytes)
	}
	if op.Elapsed > 0 {
		line += " " + formatDuration(op.Elapsed)
	}
	if op.Detail != "" {
		line += " (" + op.Detail + ")"
	}
	if op.Err != nil {
		line += " ERR: " + op.Err.Error()
	}
	fmt.Fprintln(l.w, line)
}

// JSONLogger writes one JSON object per line, for machine reading.
type JSONLogger struct {
	mu  sync.Mutex
	enc *json.Encoder
}

func NewJSONLogger(w io.Writer) *JSONLogger {
	return &JSONLogger{enc: json.NewEncoder(w)}
}

func (l *JSONLogger) Enabled() bool { return true }

func (l *JSONLogger) Trace(op Op) {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Op.Err is an interface and marshals to {}, so it is flattened to
	// text here rather than silently vanishing from the trace.
	record := struct {
		Op
		ElapsedMS float64 `json:"elapsed_ms,omitempty"`
		Error     string  `json:"error,omitempty"`
	}{Op: op, ElapsedMS: float64(op.Elapsed.Microseconds()) / 1000}
	record.Op.Elapsed = 0
	if op.Err != nil {
		record.Error = op.Err.Error()
	}
	l.enc.Encode(record)
}

// Start begins timing an operation and returns the function that records
// it. Wrapping an IO call is then a two-line affair:
//
//	done := iotrace.Start(iotrace.KindWrite, path)
//	err := os.WriteFile(path, data, 0o644)
//	done(int64(len(data)), err)
//
// The returned function is safe to call when tracing is disabled.
func Start(kind, path string) func(bytes int64, err error) {
	started := time.Now()
	return func(bytes int64, err error) {
		t := Default()
		if !t.Enabled() {
			return
		}
		t.Trace(Op{
			Kind:    kind,
			Path:    path,
			Bytes:   bytes,
			Elapsed: time.Since(started),
			Err:     err,
		})
	}
}

// StartDetail is Start with a fixed detail string describing what makes
// the operation distinct, such as which image is being exported where.
func StartDetail(kind, path, detail string) func(bytes int64, err error) {
	started := time.Now()
	return func(bytes int64, err error) {
		t := Default()
		if !t.Enabled() {
			return
		}
		t.Trace(Op{
			Kind:    kind,
			Path:    path,
			Bytes:   bytes,
			Detail:  detail,
			Elapsed: time.Since(started),
			Err:     err,
		})
	}
}

// Record reports an operation that was not timed.
func Record(op Op) {
	t := Default()
	if !t.Enabled() {
		return
	}
	t.Trace(op)
}

func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for i := n / unit; i >= unit && exp < 3; i /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}

func formatDuration(d time.Duration) string {
	if d < time.Millisecond {
		return fmt.Sprintf("%dus", d.Microseconds())
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}
