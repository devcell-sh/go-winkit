package cli

import (
	"strings"
	"testing"
)

func TestResolveWSLBackendDefaultsToQemu(t *testing.T) {
	t.Setenv("WINKIT_VM_BACKEND", "")
	backend, name, err := resolveWSLBackend()
	if err != nil {
		t.Fatalf("resolveWSLBackend: %v", err)
	}
	if name != "qemu" || backend == nil {
		t.Fatalf("got backend %q, want qemu (even on darwin: vz cannot boot Windows)", name)
	}
}

func TestResolveWSLBackendRejectsVZ(t *testing.T) {
	t.Setenv("WINKIT_VM_BACKEND", "vz")
	_, _, err := resolveWSLBackend()
	if err == nil {
		t.Fatal("vz backend accepted for a Windows install; want fail-fast error")
	}
	for _, want := range []string{"cannot boot Windows guests", "CELL-523"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

func TestResolveWSLBackendExplicitQemu(t *testing.T) {
	t.Setenv("WINKIT_VM_BACKEND", "qemu")
	_, name, err := resolveWSLBackend()
	if err != nil || name != "qemu" {
		t.Fatalf("got (%q, %v), want qemu", name, err)
	}
}

func TestResolveWSLBackendUnknown(t *testing.T) {
	t.Setenv("WINKIT_VM_BACKEND", "xen")
	if _, _, err := resolveWSLBackend(); err == nil {
		t.Fatal("unknown backend accepted")
	}
}
