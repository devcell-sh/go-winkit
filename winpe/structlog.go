package winpe

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"strings"
	"time"
)

// RAMStats is the guest memory snapshot attached to structured events.
// PowerShell's math emits these as floats ("total_mb":4087.0), so the
// fields must not be integers.
type RAMStats struct {
	TotalMB float64 `json:"total_mb"`
	UsedMB  float64 `json:"used_mb"`
	FreeMB  float64 `json:"free_mb"`
}

// GuestEvent is one line of the structured build log (build.jsonl) that
// guest scripts stream over the second virtio-serial port. Every emitter
// goes through winkit-json (templates/partials/struct-json.tmpl and
// wim-builder.ps1.tmpl), so the wire shape is a flat JSON object with
// ts + event and event-specific extras.
//
// The typed fields cover what host-side logic branches on; Fields holds
// the complete decoded object so nothing an emitter sends is lost.
type GuestEvent struct {
	TS    time.Time `json:"ts"`
	Event string    `json:"event"`

	Stage   string    `json:"stage,omitempty"`   // event=stage
	Status  string    `json:"status,omitempty"`  // ok / fail / absent / skipped / missing / already_running
	Error   string    `json:"error,omitempty"`   // present when Status=fail
	Service string    `json:"service,omitempty"` // probe_service / try_start
	Op      string    `json:"op,omitempty"`      // dism_progress / dism_output
	Line    string    `json:"line,omitempty"`    // dism_output
	Pct     *int      `json:"pct,omitempty"`     // dism_progress
	Exit    *int      `json:"exit,omitempty"`    // command exit codes
	RAM     *RAMStats `json:"ram,omitempty"`

	// Fields is the full decoded object, including keys not modeled above
	// (sc, start, source, known, pwsh, present, content, path, pid, bytes,
	// out, hvEvents, vidEvents, hvVendor, shared, ...).
	Fields map[string]any `json:"-"`
	// Raw is the original line, for logs and error reports.
	Raw string `json:"-"`
}

// UnmarshalJSON decodes both the typed view and the lossless Fields map.
func (e *GuestEvent) UnmarshalJSON(data []byte) error {
	type typedOnly GuestEvent
	var t typedOnly
	if err := json.Unmarshal(data, &t); err != nil {
		return err
	}
	*e = GuestEvent(t)
	return json.Unmarshal(data, &e.Fields)
}

// ParseGuestEvents reads structured guest events from a JSONL stream.
// The stream is written by a guest that can lose power mid-line and is
// interleaved by QEMU chardev buffering, so malformed lines are counted
// and skipped, never fatal — losing one event must not hide the rest.
func ParseGuestEvents(r io.Reader) (events []GuestEvent, skipped int, err error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev GuestEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil || ev.Event == "" {
			skipped++
			continue
		}
		ev.Raw = line
		events = append(events, ev)
	}
	return events, skipped, sc.Err()
}

// ReadGuestEvents parses a build.jsonl file. A missing file yields no
// events and no error: a guest that never opened the structured port
// leaves the file empty, and that is a normal outcome.
func ReadGuestEvents(path string) (events []GuestEvent, skipped int, err error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, err
	}
	defer f.Close()
	return ParseGuestEvents(f)
}

// Stages returns the stage names in emission order.
func Stages(events []GuestEvent) []string {
	var out []string
	for _, e := range events {
		if e.Event == "stage" {
			out = append(out, e.Stage)
		}
	}
	return out
}

// Failures returns the events reporting Status=fail.
func Failures(events []GuestEvent) []GuestEvent {
	var out []GuestEvent
	for _, e := range events {
		if e.Status == "fail" {
			out = append(out, e)
		}
	}
	return out
}
