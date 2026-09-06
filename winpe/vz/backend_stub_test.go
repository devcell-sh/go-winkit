//go:build !darwin

package vz_test

import (
	"testing"

	"github.com/devcell-sh/go-winkit/winpe"
	"github.com/devcell-sh/go-winkit/winpe/vz"
)

var _ winpe.VMBackend = (*vz.Backend)(nil)

func TestStub_DefaultDiskFormat(t *testing.T) {
	b := &vz.Backend{}
	if got := b.DefaultDiskFormat(); got != winpe.DiskFormatRaw {
		t.Errorf("DefaultDiskFormat() = %v, want DiskFormatRaw", got)
	}
}

func TestStub_AllMethodsReturnError(t *testing.T) {
	b := &vz.Backend{}
	if err := b.CreateDisk("/tmp/test.raw", 1, winpe.DiskFormatRaw); err == nil {
		t.Error("CreateDisk should error on non-darwin")
	}
	if err := b.CreateOverlay("/tmp/overlay.raw", "/tmp/base.raw"); err == nil {
		t.Error("CreateOverlay should error on non-darwin")
	}
	if err := b.Flatten("/tmp/src.raw", "/tmp/dst.raw"); err == nil {
		t.Error("Flatten should error on non-darwin")
	}
	if _, err := b.StartInstall(nil, winpe.VMInstallConfig{}); err == nil {
		t.Error("StartInstall should error on non-darwin")
	}
	if _, err := b.StartRun(nil, winpe.VMRunConfig{}); err == nil {
		t.Error("StartRun should error on non-darwin")
	}
}
