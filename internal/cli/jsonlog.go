package cli

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/devcell-sh/go-winkit/winpe"
)

// guestEventHandler emits host-side logs as winpe.GuestEvent JSONL — the
// same schema the guest's PowerShell scripts stream as build.jsonl over
// virtio-serial — so one parser (winpe.ParseGuestEvents) reads both
// sides of a build.
type guestEventHandler struct {
	mu *sync.Mutex
	w  io.Writer
}

// newLoggerJSON wraps the handler in a slog.Logger.
func newLoggerJSON(w io.Writer) *slog.Logger { return slog.New(newGuestEventHandler(w)) }

func newGuestEventHandler(w io.Writer) guestEventHandler {
	return guestEventHandler{mu: &sync.Mutex{}, w: w}
}

func (h guestEventHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h guestEventHandler) Handle(_ context.Context, r slog.Record) error {
	ev := winpe.GuestEvent{
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
