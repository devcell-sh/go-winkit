package winpe

import (
	"errors"
	"fmt"
	"strings"

	"github.com/devcell-sh/go-winkit/isokit"
)

// GuestLog is one file the guest wrote to the answer volume, or the reason it
// could not be read.
type GuestLog struct {
	Name    string
	Content []byte
	Err     error
}

// ErrNoSuchGuestLog is the sentinel for a log the guest never wrote.
var ErrNoSuchGuestLog = errors.New("not written by the guest")

const (
	// BootstrapLogName mirrors unattend.BootstrapLogName to avoid an import
	// cycle (unattend imports winpe).
	BootstrapLogName = "devcell-bootstrap.log"
	// GuestDiagnosticsLogName mirrors unattend.GuestDiagnosticsLogName.
	GuestDiagnosticsLogName = "devcell-diag.log"
)

// GuestLogNames is the contract with the guest side.
var GuestLogNames = []string{
	SetupActSnapshotName,
	SetupErrSnapshotName,
	AgentResultFile,
	BootstrapLogName,
	GuestDiagnosticsLogName,
}

// CollectGuestLogs reads every log the guest may have written to the answer
// volume. It always returns one entry per known log so the caller can report
// absence as clearly as content.
func CollectGuestLogs(answerImagePath string) []GuestLog {
	logs := make([]GuestLog, 0, len(GuestLogNames))
	for _, name := range GuestLogNames {
		data, err := isokit.ReadFileFromFAT(answerImagePath, "/"+name)
		if err != nil {
			logs = append(logs, GuestLog{Name: name, Err: fmt.Errorf("%w: %v", ErrNoSuchGuestLog, err)})
			continue
		}
		logs = append(logs, GuestLog{Name: name, Content: data})
	}
	return logs
}

// FormatGuestLogs renders collected logs for a terminal.
func FormatGuestLogs(logs []GuestLog) string {
	var b strings.Builder
	for _, l := range logs {
		if l.Err != nil {
			fmt.Fprintf(&b, "=== %s: not written by the guest ===\n", l.Name)
			continue
		}
		fmt.Fprintf(&b, "=== %s (%d bytes) ===\n%s\n", l.Name, len(l.Content), strings.TrimRight(string(l.Content), "\r\n"))
	}
	return b.String()
}

// BootstrapSteps is the guest's first-logon provisioning, read back from its
// own transcript.
type BootstrapSteps struct {
	OK         []string
	Failed     []string
	Unfinished []string
}

const bootstrapPrefix = "devcell-bootstrap: "

// ParseBootstrapSteps reads step outcomes out of a bootstrap transcript.
func ParseBootstrapSteps(transcript string) BootstrapSteps {
	steps := BootstrapSteps{OK: []string{}, Failed: []string{}, Unfinished: []string{}}
	started := map[string]bool{}
	var order []string

	for _, line := range strings.Split(transcript, "\n") {
		line = strings.TrimSpace(line)
		i := strings.Index(line, bootstrapPrefix)
		if i < 0 {
			continue
		}
		msg := line[i+len(bootstrapPrefix):]
		switch {
		case strings.HasPrefix(msg, "step: "):
			name := strings.TrimPrefix(msg, "step: ")
			if !started[name] {
				started[name] = true
				order = append(order, name)
			}
		case strings.HasPrefix(msg, "ok: "):
			name := strings.TrimPrefix(msg, "ok: ")
			steps.OK = append(steps.OK, name)
			delete(started, name)
		case strings.HasPrefix(msg, "FAILED: "):
			detail := strings.TrimPrefix(msg, "FAILED: ")
			steps.Failed = append(steps.Failed, detail)
			name, _, _ := strings.Cut(detail, " -- ")
			delete(started, name)
		}
	}

	for _, name := range order {
		if started[name] {
			steps.Unfinished = append(steps.Unfinished, name)
		}
	}
	return steps
}

// SSHReady reports whether the guest got far enough for the build to talk to
// it: sshd installed AND started.
func (s BootstrapSteps) SSHReady() bool {
	var installed, started bool
	for _, name := range s.OK {
		if strings.Contains(name, "install OpenSSH server") {
			installed = true
		}
		if strings.Contains(name, "start sshd") {
			started = true
		}
	}
	return installed && started
}
