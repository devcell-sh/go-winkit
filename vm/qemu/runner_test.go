package qemu

import (
	"testing"

	"github.com/devcell-sh/go-winkit/winpe"
)

func TestRunner_ImplementsInterface(t *testing.T) {
	var _ winpe.Runner = (*Runner)(nil)
}

func TestNewRunner_SetsFields(t *testing.T) {
	r := NewRunner("/usr/bin/qemu-system-aarch64", "tcg")
	if r.QEMUBin != "/usr/bin/qemu-system-aarch64" {
		t.Errorf("QEMUBin = %q, want /usr/bin/qemu-system-aarch64", r.QEMUBin)
	}
	if r.Accel != "tcg" {
		t.Errorf("Accel = %q, want tcg", r.Accel)
	}
}
