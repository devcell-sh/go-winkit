package winpe_test

import (
	"testing"

	"github.com/devcell-sh/go-winkit/winpe"
	"github.com/devcell-sh/go-winkit/winpe/qemu"
)

// Compile-time interface satisfaction checks.
var _ winpe.VMBackend = (*qemu.Backend)(nil)

func TestDiskFormat_String(t *testing.T) {
	tests := []struct {
		f    winpe.DiskFormat
		want string
	}{
		{winpe.DiskFormatDefault, "default"},
		{winpe.DiskFormatQcow2, "qcow2"},
		{winpe.DiskFormatRaw, "raw"},
	}
	for _, tt := range tests {
		if got := tt.f.String(); got != tt.want {
			t.Errorf("DiskFormat(%d).String() = %q, want %q", tt.f, got, tt.want)
		}
	}
}

func TestDefaultDiskFormat_QEMU(t *testing.T) {
	b := &qemu.Backend{}
	if got := b.DefaultDiskFormat(); got != winpe.DiskFormatQcow2 {
		t.Errorf("qemu.Backend.DefaultDiskFormat() = %v, want DiskFormatQcow2", got)
	}
}

func TestResolveBackend_Explicit(t *testing.T) {
	reg := map[string]winpe.VMBackend{
		"qemu": &qemu.Backend{},
	}
	b, _, err := winpe.ResolveBackend("qemu", reg)
	if err != nil {
		t.Fatalf("ResolveBackend(qemu): %v", err)
	}
	if b == nil {
		t.Fatal("ResolveBackend(qemu) returned nil")
	}
}

func TestResolveBackend_Unknown(t *testing.T) {
	reg := map[string]winpe.VMBackend{
		"qemu": &qemu.Backend{},
	}
	_, _, err := winpe.ResolveBackend("bogus", reg)
	if err == nil {
		t.Fatal("ResolveBackend(bogus) should return error")
	}
}

func TestResolveBackend_EnvOverride(t *testing.T) {
	reg := map[string]winpe.VMBackend{
		"qemu": &qemu.Backend{},
	}
	t.Setenv("WINKIT_VM_BACKEND", "qemu")
	b, _, err := winpe.ResolveBackend("", reg)
	if err != nil {
		t.Fatalf("ResolveBackend with env: %v", err)
	}
	if b == nil {
		t.Fatal("ResolveBackend with env returned nil")
	}
}

func TestResolveBackend_PlatformDefault(t *testing.T) {
	reg := map[string]winpe.VMBackend{
		winpe.DefaultBackendName(): &qemu.Backend{},
	}
	t.Setenv("WINKIT_VM_BACKEND", "")
	b, _, err := winpe.ResolveBackend("", reg)
	if err != nil {
		t.Fatalf("ResolveBackend platform default: %v", err)
	}
	if b == nil {
		t.Fatal("ResolveBackend platform default returned nil")
	}
}
