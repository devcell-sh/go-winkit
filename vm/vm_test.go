package vm_test

import (
	"testing"

	"github.com/devcell-sh/go-winkit/vm"
	"github.com/devcell-sh/go-winkit/vm/qemu"
)

// Compile-time interface satisfaction checks.
var _ vm.VMBackend = (*qemu.Backend)(nil)

func TestDiskFormat_String(t *testing.T) {
	tests := []struct {
		f    vm.DiskFormat
		want string
	}{
		{vm.DiskFormatDefault, "default"},
		{vm.DiskFormatQcow2, "qcow2"},
		{vm.DiskFormatRaw, "raw"},
	}
	for _, tt := range tests {
		if got := tt.f.String(); got != tt.want {
			t.Errorf("DiskFormat(%d).String() = %q, want %q", tt.f, got, tt.want)
		}
	}
}

func TestDefaultDiskFormat_QEMU(t *testing.T) {
	b := &qemu.Backend{}
	if got := b.DefaultDiskFormat(); got != vm.DiskFormatQcow2 {
		t.Errorf("qemu.Backend.DefaultDiskFormat() = %v, want DiskFormatQcow2", got)
	}
}

func TestResolveBackend_Explicit(t *testing.T) {
	reg := map[string]vm.VMBackend{
		"qemu": &qemu.Backend{},
	}
	b, _, err := vm.ResolveBackend("qemu", reg)
	if err != nil {
		t.Fatalf("ResolveBackend(qemu): %v", err)
	}
	if b == nil {
		t.Fatal("ResolveBackend(qemu) returned nil")
	}
}

func TestResolveBackend_Unknown(t *testing.T) {
	reg := map[string]vm.VMBackend{
		"qemu": &qemu.Backend{},
	}
	_, _, err := vm.ResolveBackend("bogus", reg)
	if err == nil {
		t.Fatal("ResolveBackend(bogus) should return error")
	}
}

func TestResolveBackend_EnvOverride(t *testing.T) {
	reg := map[string]vm.VMBackend{
		"qemu": &qemu.Backend{},
	}
	t.Setenv("WINKIT_VM_BACKEND", "qemu")
	b, _, err := vm.ResolveBackend("", reg)
	if err != nil {
		t.Fatalf("ResolveBackend with env: %v", err)
	}
	if b == nil {
		t.Fatal("ResolveBackend with env returned nil")
	}
}

func TestResolveBackend_PlatformDefault(t *testing.T) {
	reg := map[string]vm.VMBackend{
		vm.DefaultBackendName(): &qemu.Backend{},
	}
	t.Setenv("WINKIT_VM_BACKEND", "")
	b, _, err := vm.ResolveBackend("", reg)
	if err != nil {
		t.Fatalf("ResolveBackend platform default: %v", err)
	}
	if b == nil {
		t.Fatal("ResolveBackend platform default returned nil")
	}
}
