package winpe

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Lines below are verbatim from a real passing run
// (test/results/20260830T082334-TestWimBuilder_tcg/build.jsonl), including
// PowerShell's float RAM values and a power-loss-truncated final line.
const realBuildJSONL = `{"ts":"2026-08-30T08:25:10.6866209-08:00","event":"start","shared":"C:","ram":{"free_mb":2999.0,"used_mb":1088.0,"total_mb":4087.0}}
{"ts":"2026-08-30T08:25:18.6893447-08:00","event":"stage","stage":"Locating virtio-win ISO","ram":{"free_mb":2989.0,"used_mb":1098.0,"total_mb":4087.0}}
{"ts":"2026-08-30T08:25:19.3295654-08:00","event":"locate_iso","drive":"F:","status":"ok","target":"virtio-win","ram":{"free_mb":2988.0,"used_mb":1099.0,"total_mb":4087.0}}
{"ts":"2026-08-30T08:26:20.1770427-08:00","event":"internet","available":false,"ram":{"free_mb":2949.0,"used_mb":1138.0,"total_mb":4087.0}}
{"ts":"2026-08-30T08:30:00.0000000-08:00","event":"dism_progress","pct":40,"op":"Add-Driver NetKVM\\w11\\ARM64","ram":{"free_mb":2100.0,"used_mb":1987.0,"total_mb":4087.0}}
{"ts":"2026-08-30T08:31:00.0000000-08:00","event":"vmcompute","status":"fail","error":"The service did not respond","ram":{"free_mb":2100.0,"used_mb":1987.0,"total_mb":4087.0}}

{"ts":"2026-08-30T08:32:00.0000000-08:00","event":"internet","available":false,"ram":{"free_mb":2949.0,"`

func TestParseGuestEvents_RealLines(t *testing.T) {
	events, skipped, err := ParseGuestEvents(strings.NewReader(realBuildJSONL))
	require.NoError(t, err)

	assert.Equal(t, 1, skipped, "the truncated final line must be skipped, not fatal")
	require.Len(t, events, 6)

	start := events[0]
	assert.Equal(t, "start", start.Event)
	assert.Equal(t, "C:", start.Fields["shared"])
	require.NotNil(t, start.RAM)
	assert.Equal(t, 4087.0, start.RAM.TotalMB, "PowerShell float RAM values must decode")
	assert.Equal(t, 2026, start.TS.Year())

	assert.Equal(t, []string{"Locating virtio-win ISO"}, Stages(events))

	locate := events[2]
	assert.Equal(t, "ok", locate.Status)
	assert.Equal(t, "F:", locate.Fields["drive"], "unmodeled keys must survive in Fields")

	dism := events[4]
	require.NotNil(t, dism.Pct)
	assert.Equal(t, 40, *dism.Pct)
	assert.Equal(t, `Add-Driver NetKVM\w11\ARM64`, dism.Op)

	fails := Failures(events)
	require.Len(t, fails, 1)
	assert.Equal(t, "vmcompute", fails[0].Event)
	assert.Contains(t, fails[0].Error, "did not respond")

	assert.NotEmpty(t, events[0].Raw, "raw line must be preserved")
}

func TestReadGuestEvents_MissingFileIsNormal(t *testing.T) {
	events, skipped, err := ReadGuestEvents("/nonexistent/build.jsonl")
	require.NoError(t, err, "a guest that never opened the port leaves no file — not an error")
	assert.Empty(t, events)
	assert.Zero(t, skipped)
}
