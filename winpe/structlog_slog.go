package winpe

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
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
	for k, v := range extra {
		line[k] = v
	}
	out, err := json.Marshal(line)
	if err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	_, err = h.w.Write(append(out, '\n'))
	return err
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
