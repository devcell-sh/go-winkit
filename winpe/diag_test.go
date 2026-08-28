package winpe

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// DiagCommand must be read-only: run 20260812T143146 died with 0x80070103
// (ERROR_NO_MORE_ITEMS) after the agent's diag drvloaded the same INF that
// wpeinit had already picked up from $WinPEDriver$. The diagnostic must
// observe, never mutate.
func TestDiagCommand_ReadOnlyDiagnostics(t *testing.T) {
	assert.NotContains(t, DiagCommand, "drvload", "diag must not load drivers: it caused the double-load abort")
	assert.Contains(t, DiagCommand, "diskpart.exe")
	assert.Contains(t, DiagCommand, `reg.exe query HKLM\SYSTEM\CurrentControlSet\Services\vioscsi`)
	assert.Contains(t, DiagCommand, "Panther")
	assert.False(t, strings.Contains(DiagCommand, "\n"), "must stay a single line: the agent reads the first line via Get-Content")
}
