package winpe

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// guestEventHandler emits slog records as GuestEvent JSONL — the same
// schema the guest's PowerShell scripts stream as build.jsonl over
// virtio-serial — so one parser (ParseGuestEvents) reads both sides of a
// build.
type guestEventHandler struct {
	mu *sync.Mutex
	w  io.Writer
}

// NewGuestEventHandler returns a slog.Handler writing GuestEvent JSONL.
func NewGuestEventHandler(w io.Writer) slog.Handler {
	return guestEventHandler{mu: &sync.Mutex{}, w: w}
}

func (h guestEventHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h guestEventHandler) Handle(_ context.Context, r slog.Record) error {
	ev := GuestEvent{
		TS:     r.Time,
		Event:  "log",
		Status: statusForLevel(r.Level),
		Line:   r.Message,
	}
	if ev.TS.IsZero() {
		ev.TS = time.Now()
	}
	extra := map[string]any{}
	r.Attrs(func(a slog.Attr) bool {
		switch a.Key {
		case "stage":
			ev.Stage = a.Value.String()
		case "error":
			ev.Error = a.Value.String()
		default:
			extra[a.Key] = a.Value.Any()
		}
		return true
	})
	// The wire shape is one flat object, like the guest emitters produce.
	line := map[string]any{}
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, &line); err != nil {
		return err
	}
	line["source"] = "host"
	for k, v := range extra {
		line[k] = v
	}
	out, err := marshalOrdered(line)
	if err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	_, err = h.w.Write(append(out, '\n'))
	return err
}

// marshalOrdered serializes one event with a stable key order — ts,
// event, line first, then the remaining keys alphabetically — so the
// unified JSONL reads chronologically at a glance and diffs cleanly.
func marshalOrdered(obj map[string]any) ([]byte, error) {
	head := []string{"ts", "event", "line"}
	rest := make([]string, 0, len(obj))
	for k := range obj {
		if k != "ts" && k != "event" && k != "line" {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)

	var b strings.Builder
	b.WriteByte('{')
	first := true
	writeKV := func(k string) error {
		v, ok := obj[k]
		if !ok {
			return nil
		}
		vb, err := json.Marshal(v)
		if err != nil {
			return err
		}
		if !first {
			b.WriteByte(',')
		}
		first = false
		kb, _ := json.Marshal(k)
		b.Write(kb)
		b.WriteByte(':')
		b.Write(vb)
		return nil
	}
	for _, k := range append(head, rest...) {
		if err := writeKV(k); err != nil {
			return nil, err
		}
	}
	b.WriteByte('}')
	return []byte(b.String()), nil
}

func statusForLevel(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return "error"
	case l >= slog.LevelWarn:
		return "warn"
	case l >= slog.LevelInfo:
		return "info"
	default:
		return "debug"
	}
}

func (h guestEventHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h guestEventHandler) WithGroup(string) slog.Handler      { return h }

// MultiHandler fans slog records out to multiple handlers so one Logger
// can write to both a display and a structured log file.
func MultiHandler(handlers ...slog.Handler) slog.Handler {
	return multiHandler{handlers: handlers}
}

type multiHandler struct {
	handlers []slog.Handler
}

func (m multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range m.handlers {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (m multiHandler) Handle(ctx context.Context, r slog.Record) error {
	for _, h := range m.handlers {
		if h.Enabled(ctx, r.Level) {
			_ = h.Handle(ctx, r)
		}
	}
	return nil
}

func (m multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	hs := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		hs[i] = h.WithAttrs(attrs)
	}
	return multiHandler{handlers: hs}
}

func (m multiHandler) WithGroup(name string) slog.Handler {
	hs := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		hs[i] = h.WithGroup(name)
	}
	return multiHandler{handlers: hs}
}

// AppendStreamJSONL appends the lines of srcPath to dst as GuestEvent
// JSONL, namespaced under source. Lines that already are JSON objects
// (the guest's structured stream) pass through with "source" injected;
// plain-text lines (serial console, progress logs) are wrapped as
// {"event":"log","line":...,"source":...} with the message JSON-escaped.
// Wrapped lines are stamped with the merge time — their original wall
// time is unknown — so consumers should treat them as coarse-ordered.
// A missing srcPath is not an error: not every run produces every stream.
func AppendStreamJSONL(dst io.Writer, srcPath, source string) error {
	src, err := os.Open(srcPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer src.Close()

	now := time.Now()
	sc := bufio.NewScanner(src)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err == nil && obj != nil {
			if _, ok := obj["source"]; !ok {
				obj["source"] = source
			}
		} else {
			obj = map[string]any{
				"ts":     now,
				"event":  "log",
				"status": "info",
				"line":   line,
				"source": source,
			}
		}
		out, err := marshalOrdered(obj)
		if err != nil {
			continue
		}
		if _, err := dst.Write(append(out, '\n')); err != nil {
			return err
		}
	}
	return sc.Err()
}
