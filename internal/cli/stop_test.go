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

func TestStopCmd_NoArgNoVMs(t *testing.T) {
	dir := t.TempDir()
	cmd := newStopCmd()
	cmd.SetArgs([]string{"--state-dir", dir})
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when no VMs running")
	}
	if !strings.Contains(err.Error(), "no running VMs") {
		t.Errorf("expected 'no running VMs', got: %v", err)
	}
}

func TestStopCmd_NotFound(t *testing.T) {
	dir := t.TempDir()
	cmd := newStopCmd()
	cmd.SetArgs([]string{"nonexistent", "--state-dir", dir})
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for unknown VM")
	}
}

func TestStopCmd_StaleState(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "run")

	s := &vmstate.State{
		Name:      "stale-vm",
		ImagePath: "/fake/disk.qcow2",
		PID:       999999999, // not alive
		Backend:   "qemu",
		StartedAt: time.Now(),
	}
	if err := vmstate.Save(stateDir, s); err != nil {
		t.Fatal(err)
	}

	out := new(bytes.Buffer)
	cmd := newStopCmd()
	cmd.SetArgs([]string{"stale-vm", "--state-dir", stateDir})
	cmd.SetOut(out)
	cmd.SetErr(new(bytes.Buffer))
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// State file should be cleaned up
	_, loadErr := vmstate.Load(stateDir, "stale-vm")
	if loadErr == nil {
		t.Error("state file should have been removed")
	}
}

func TestStopCmd_ByImagePath(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "run")
	disk := filepath.Join(dir, "test.qcow2")
	if err := os.WriteFile(disk, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := &vmstate.State{
		Name:      "test",
		ImagePath: disk,
		PID:       999999999,
		Backend:   "qemu",
		StartedAt: time.Now(),
	}
	if err := vmstate.Save(stateDir, s); err != nil {
		t.Fatal(err)
	}

	out := new(bytes.Buffer)
	cmd := newStopCmd()
	cmd.SetArgs([]string{disk, "--state-dir", stateDir})
	cmd.SetOut(out)
	cmd.SetErr(new(bytes.Buffer))
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
