//go:build darwin

package vz_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/devcell-sh/go-winkit/winpe"
	"github.com/devcell-sh/go-winkit/winpe/vz"
)

var _ winpe.VMBackend = (*vz.Backend)(nil)

func TestDefaultDiskFormat(t *testing.T) {
	b := &vz.Backend{}
	if got := b.DefaultDiskFormat(); got != winpe.DiskFormatRaw {
		t.Errorf("DefaultDiskFormat() = %v, want DiskFormatRaw", got)
	}
}

func TestCreateDisk_Raw(t *testing.T) {
	b := &vz.Backend{}
	path := filepath.Join(t.TempDir(), "test.raw")
	if err := b.CreateDisk(path, 1, winpe.DiskFormatRaw); err != nil {
		t.Fatalf("CreateDisk: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	want := int64(1 * 1024 * 1024 * 1024)
	if fi.Size() != want {
		t.Errorf("disk size = %d, want %d", fi.Size(), want)
	}
}

func TestCreateDisk_RejectsQcow2(t *testing.T) {
	b := &vz.Backend{}
	path := filepath.Join(t.TempDir(), "test.qcow2")
	if err := b.CreateDisk(path, 1, winpe.DiskFormatQcow2); err == nil {
		t.Error("CreateDisk should reject qcow2 on vz backend")
	}
}

func TestCreateOverlay_CopiesBase(t *testing.T) {
	b := &vz.Backend{}
	dir := t.TempDir()
	base := filepath.Join(dir, "base.raw")
	if err := os.WriteFile(base, []byte("base-content"), 0o644); err != nil {
		t.Fatal(err)
	}
	overlay := filepath.Join(dir, "overlay.raw")
	if err := b.CreateOverlay(overlay, base); err != nil {
		t.Fatalf("CreateOverlay: %v", err)
	}
	got, err := os.ReadFile(overlay)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "base-content" {
		t.Errorf("overlay content = %q, want %q", got, "base-content")
	}
	// Verify base is untouched
	if err := os.WriteFile(overlay, []byte("modified"), 0o644); err != nil {
		t.Fatal(err)
	}
	baseContent, _ := os.ReadFile(base)
	if string(baseContent) != "base-content" {
		t.Error("base was modified by overlay write")
	}
}

func TestFlatten_CopiesSrcToDst(t *testing.T) {
	b := &vz.Backend{}
	dir := t.TempDir()
	src := filepath.Join(dir, "src.raw")
	if err := os.WriteFile(src, []byte("flat-content"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "dst.raw")
	if err := b.Flatten(src, dst); err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "flat-content" {
		t.Errorf("flatten result = %q, want %q", got, "flat-content")
	}
}
