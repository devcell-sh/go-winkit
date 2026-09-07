package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devcell-sh/go-winkit/vmstate"
)

func TestStartCmd_NoArgNoImage(t *testing.T) {
	dir := t.TempDir()
	// Run from a dir with no images.
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	cmd := newStartCmd()
	cmd.SetArgs([]string{})
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when no image found")
	}
	if !strings.Contains(err.Error(), "no disk image found") {
		t.Errorf("expected discovery error, got: %v", err)
	}
}

func TestDiscoverImage(t *testing.T) {
	dir := t.TempDir()

	// No images yet.
	if got := discoverImage(dir); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}

	// Create winkit-base.qcow2.
	disk := filepath.Join(dir, "winkit-base.qcow2")
	os.WriteFile(disk, []byte("fake"), 0o644)
	got := discoverImage(dir)
	if got != disk {
		t.Errorf("discoverImage = %q, want %q", got, disk)
	}
}

func TestDiscoverImagePriority(t *testing.T) {
	dir := t.TempDir()

	// Create both base and wsl.
	os.WriteFile(filepath.Join(dir, "winkit-wsl.qcow2"), []byte("w"), 0o644)
	os.WriteFile(filepath.Join(dir, "winkit-base.qcow2"), []byte("b"), 0o644)

	got := discoverImage(dir)
	want := filepath.Join(dir, "winkit-base.qcow2")
	if got != want {
		t.Errorf("discoverImage = %q, want %q (base takes priority)", got, want)
	}
}

func TestStartCmd_ImageNotFound(t *testing.T) {
	cmd := newStartCmd()
	cmd.SetArgs([]string{"/nonexistent/disk.qcow2"})
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing image file")
	}
}

func TestStartCmd_NameFromImage(t *testing.T) {
	got := nameFromImage("/path/to/my-windows.qcow2")
	if got != "my-windows" {
		t.Errorf("nameFromImage = %q, want %q", got, "my-windows")
	}
}

func TestStartCmd_NameFromImageRaw(t *testing.T) {
	got := nameFromImage("/path/disk.raw")
	if got != "disk" {
		t.Errorf("nameFromImage = %q, want %q", got, "disk")
	}
}

func TestStartCmd_AlreadyRunning(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "run")

	// Create a fake disk
	disk := filepath.Join(dir, "test.qcow2")
	if err := os.WriteFile(disk, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Write a state file indicating it's "running"
	s := &vmstate.State{
		Name:      "test",
		ImagePath: disk,
		PID:       os.Getpid(), // current process, so IsAlive returns true
		Backend:   "qemu",
	}
	if err := vmstate.Save(stateDir, s); err != nil {
		t.Fatal(err)
	}

	cmd := newStartCmd()
	cmd.SetArgs([]string{disk, "--state-dir", stateDir})
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for already-running VM")
	}
}
