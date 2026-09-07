package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devcell-sh/go-winkit/vm/vmstate"
)

func TestStatusCmd_NoVMs(t *testing.T) {
	dir := t.TempDir()
	out := new(bytes.Buffer)
	cmd := newStatusCmd()
	cmd.SetArgs([]string{"--state-dir", dir})
	cmd.SetOut(out)
	cmd.SetErr(new(bytes.Buffer))
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "No running VMs") {
		t.Errorf("expected 'No running VMs', got %q", out.String())
	}
}

func TestStatusCmd_ShowsRunning(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "run")

	s := &vmstate.State{
		Name:      "my-vm",
		ImagePath: "/data/windows.qcow2",
		PID:       os.Getpid(),
		Backend:   "qemu",
		StartedAt: time.Now().Add(-5 * time.Minute),
		SSHPort:   20022,
		RDPPort:   23389,
		VNCPort:   5900,
	}
	if err := vmstate.Save(stateDir, s); err != nil {
		t.Fatal(err)
	}

	out := new(bytes.Buffer)
	cmd := newStatusCmd()
	cmd.SetArgs([]string{"--state-dir", stateDir})
	cmd.SetOut(out)
	cmd.SetErr(new(bytes.Buffer))
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "my-vm") {
		t.Errorf("expected VM name in output, got %q", output)
	}
	if !strings.Contains(output, "running") {
		t.Errorf("expected 'running' in output, got %q", output)
	}
	if !strings.Contains(output, "20022") {
		t.Errorf("expected SSH port in output, got %q", output)
	}
}

func TestStatusCmd_ShowsStale(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "run")

	s := &vmstate.State{
		Name:      "dead-vm",
		ImagePath: "/data/old.qcow2",
		PID:       999999999,
		Backend:   "qemu",
		StartedAt: time.Now().Add(-1 * time.Hour),
		SSHPort:   20022,
	}
	if err := vmstate.Save(stateDir, s); err != nil {
		t.Fatal(err)
	}

	out := new(bytes.Buffer)
	cmd := newStatusCmd()
	cmd.SetArgs([]string{"--state-dir", stateDir})
	cmd.SetOut(out)
	cmd.SetErr(new(bytes.Buffer))
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "dead-vm") {
		t.Errorf("expected VM name in output, got %q", output)
	}
	if !strings.Contains(output, "stale") {
		t.Errorf("expected 'stale' in output, got %q", output)
	}
}
