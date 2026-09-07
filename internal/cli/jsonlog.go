package cli

import (
	"io"
	"log/slog"

	"github.com/devcell-sh/go-winkit/winpe"
)

// newLoggerJSON emits host-side logs as winpe.GuestEvent JSONL — the same
// schema the guest streams as build.jsonl — so one parser
// (winpe.ParseGuestEvents) reads both sides of a build.
func newLoggerJSON(w io.Writer) *slog.Logger { return slog.New(winpe.NewGuestEventHandler(w)) }
