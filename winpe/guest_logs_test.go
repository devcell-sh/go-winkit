package winpe

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/devcell-sh/go-winkit/media/isokit"
	"github.com/stretchr/testify/require"
)

func TestCollectGuestLogs_ReturnsEveryLogTheGuestWrote(t *testing.T) {
	img := filepath.Join(t.TempDir(), "answer.img")
	require.NoError(t, isokit.CreateFATImage(img, map[string][]byte{
		"/" + BootstrapLogName:        []byte("winkit-bootstrap: starting"),
		"/" + GuestDiagnosticsLogName: []byte("=== NETWORK ADAPTERS ==="),
		"/" + SetupActSnapshotName:    []byte("setupact contents"),
	}))

	logs := CollectGuestLogs(img)

	byName := map[string]GuestLog{}
	for _, l := range logs {
		byName[l.Name] = l
	}
	require.Contains(t, byName, BootstrapLogName)
	require.Contains(t, byName, GuestDiagnosticsLogName)
	require.Contains(t, byName, SetupActSnapshotName)
	require.Equal(t, "winkit-bootstrap: starting", string(byName[BootstrapLogName].Content))
	require.NoError(t, byName[BootstrapLogName].Err)
}

func TestCollectGuestLogs_ReportsMissingLogsRatherThanSkippingThem(t *testing.T) {
	img := filepath.Join(t.TempDir(), "answer.img")
	require.NoError(t, isokit.CreateFATImage(img, map[string][]byte{
		"/" + BootstrapLogName: []byte("only this one"),
	}))

	logs := CollectGuestLogs(img)

	var missing []string
	for _, l := range logs {
		if l.Err != nil {
			missing = append(missing, l.Name)
			require.Nil(t, l.Content, "a log that failed to read must carry no content")
		}
	}
	require.Contains(t, missing, GuestDiagnosticsLogName,
		"a log the guest never wrote must be reported, not omitted")
	require.Len(t, logs, len(GuestLogNames), "every known log must be accounted for")
}

func TestGuestLogNames_CoverWinPEAndFirstLogonChannels(t *testing.T) {
	require.ElementsMatch(t, []string{
		SetupActSnapshotName,
		SetupErrSnapshotName,
		AgentResultFile,
		BootstrapLogName,
		GuestDiagnosticsLogName,
	}, GuestLogNames)
}

func TestFormatGuestLogs_RendersContentAndAbsence(t *testing.T) {
	out := FormatGuestLogs([]GuestLog{
		{Name: "winkit-bootstrap.log", Content: []byte("line one\nline two")},
		{Name: "winkit-diag.log", Err: ErrNoSuchGuestLog},
	})

	require.Contains(t, out, "winkit-bootstrap.log")
	require.Contains(t, out, "line two")
	require.Contains(t, out, "winkit-diag.log")
	require.True(t, strings.Contains(out, "not written by the guest"),
		"absence must be stated in words, not left blank: %s", out)
}

func TestParseBootstrapSteps_SeparatesOkFromFailed(t *testing.T) {
	transcript := strings.Join([]string{
		"winkit-bootstrap: starting (answer volume: D:)",
		"winkit-bootstrap: step: install OpenSSH server",
		"winkit-bootstrap: ok: install OpenSSH server",
		"winkit-bootstrap: step: start sshd",
		"winkit-bootstrap: FAILED: start sshd -- service did not start",
		"winkit-bootstrap: step: open the firewall for SSH",
		"winkit-bootstrap: ok: open the firewall for SSH",
	}, "\r\n")

	steps := ParseBootstrapSteps(transcript)

	require.Equal(t, []string{"install OpenSSH server", "open the firewall for SSH"}, steps.OK)
	require.Equal(t, []string{"start sshd -- service did not start"}, steps.Failed)
	require.Equal(t, []string{}, steps.Unfinished, "every started step here reached a verdict")
}

func TestParseBootstrapSteps_ReportsStepsThatNeverFinished(t *testing.T) {
	transcript := "winkit-bootstrap: step: install OpenSSH server\r\n"

	steps := ParseBootstrapSteps(transcript)

	require.Empty(t, steps.OK)
	require.Empty(t, steps.Failed)
	require.Equal(t, []string{"install OpenSSH server"}, steps.Unfinished)
}

func TestBootstrapSteps_SSHReadyRequiresSshdStarted(t *testing.T) {
	partial := ParseBootstrapSteps("winkit-bootstrap: ok: install OpenSSH server\r\n")
	require.False(t, partial.SSHReady(), "installing OpenSSH is not the same as running it")

	full := ParseBootstrapSteps(strings.Join([]string{
		"winkit-bootstrap: ok: install OpenSSH server",
		"winkit-bootstrap: ok: authorize SSH key for administrators",
		"winkit-bootstrap: ok: start sshd",
	}, "\r\n"))
	require.True(t, full.SSHReady())
}
