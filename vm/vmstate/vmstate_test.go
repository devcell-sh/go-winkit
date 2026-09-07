package vmstate

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	s := &State{
		Name:      "test-vm",
		ImagePath: "/path/to/disk.qcow2",
		PID:       12345,
		Backend:   "qemu",
		StartedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		SSHPort:   20022,
		RDPPort:   23389,
		VNCPort:   5900,
	}

	if err := Save(dir, s); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load(dir, "test-vm")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Name != s.Name {
		t.Errorf("Name = %q, want %q", got.Name, s.Name)
	}
	if got.PID != s.PID {
		t.Errorf("PID = %d, want %d", got.PID, s.PID)
	}
	if got.SSHPort != s.SSHPort {
		t.Errorf("SSHPort = %d, want %d", got.SSHPort, s.SSHPort)
	}
	if got.RDPPort != s.RDPPort {
		t.Errorf("RDPPort = %d, want %d", got.RDPPort, s.RDPPort)
	}
	if got.VNCPort != s.VNCPort {
		t.Errorf("VNCPort = %d, want %d", got.VNCPort, s.VNCPort)
	}
	if got.ImagePath != s.ImagePath {
		t.Errorf("ImagePath = %q, want %q", got.ImagePath, s.ImagePath)
	}
	if !got.StartedAt.Equal(s.StartedAt) {
		t.Errorf("StartedAt = %v, want %v", got.StartedAt, s.StartedAt)
	}
}

func TestLoadNotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := Load(dir, "nonexistent")
	if err == nil {
		t.Fatal("expected error for missing state")
	}
}

func TestList(t *testing.T) {
	dir := t.TempDir()
	names := []string{"vm-a", "vm-b", "vm-c"}
	for _, n := range names {
		s := &State{
			Name:      n,
			ImagePath: "/disk/" + n + ".qcow2",
			PID:       1000,
			Backend:   "qemu",
			StartedAt: time.Now(),
		}
		if err := Save(dir, s); err != nil {
			t.Fatalf("Save %s: %v", n, err)
		}
	}

	got, err := List(dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("List returned %d states, want 3", len(got))
	}
	seen := map[string]bool{}
	for _, s := range got {
		seen[s.Name] = true
	}
	for _, n := range names {
		if !seen[n] {
			t.Errorf("List missing %q", n)
		}
	}
}

func TestListEmpty(t *testing.T) {
	dir := t.TempDir()
	got, err := List(dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("List returned %d, want 0", len(got))
	}
}

func TestRemove(t *testing.T) {
	dir := t.TempDir()
	s := &State{Name: "doomed", PID: 1, Backend: "qemu", StartedAt: time.Now()}
	if err := Save(dir, s); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := Remove(dir, "doomed"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	_, err := Load(dir, "doomed")
	if err == nil {
		t.Fatal("expected error after Remove")
	}
}

func TestRemoveNotFound(t *testing.T) {
	dir := t.TempDir()
	err := Remove(dir, "ghost")
	if err != nil {
		t.Fatalf("Remove non-existent should not error, got: %v", err)
	}
}

func TestFindByImage(t *testing.T) {
	dir := t.TempDir()
	s := &State{
		Name:      "my-vm",
		ImagePath: "/data/windows.qcow2",
		PID:       999,
		Backend:   "qemu",
		StartedAt: time.Now(),
	}
	if err := Save(dir, s); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := FindByImage(dir, "/data/windows.qcow2")
	if err != nil {
		t.Fatalf("FindByImage: %v", err)
	}
	if got == nil {
		t.Fatal("FindByImage returned nil")
	}
	if got.Name != "my-vm" {
		t.Errorf("Name = %q, want %q", got.Name, "my-vm")
	}
}

func TestFindByImageNotFound(t *testing.T) {
	dir := t.TempDir()
	got, err := FindByImage(dir, "/nope.qcow2")
	if err != nil {
		t.Fatalf("FindByImage: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestIsAlive(t *testing.T) {
	if !IsAlive(os.Getpid()) {
		t.Error("current process should be alive")
	}
	if IsAlive(999999999) {
		t.Error("PID 999999999 should not be alive")
	}
}

func TestStateFilePath(t *testing.T) {
	dir := "/run/winkit"
	p := stateFile(dir, "my-vm")
	want := filepath.Join(dir, "my-vm.json")
	if p != want {
		t.Errorf("stateFile = %q, want %q", p, want)
	}
}

func TestDefaultDir(t *testing.T) {
	d := DefaultDir()
	if d == "" {
		t.Fatal("DefaultDir returned empty string")
	}
}
