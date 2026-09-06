package gosshd_test

import (
	"testing"

	"github.com/devcell-sh/go-winkit/gosshd"
)

func TestVsockAddr(t *testing.T) {
	a := gosshd.VsockAddr(1024)
	if a.Network() != "vsock" {
		t.Errorf("Network() = %q, want vsock", a.Network())
	}
	if a.String() != "vsock://:%d" {
		want := "vsock://:1024"
		if a.String() != want {
			t.Errorf("String() = %q, want %q", a.String(), want)
		}
	}
}

func TestListenVsock_ReturnsErrorOnNonWindows(t *testing.T) {
	_, err := gosshd.ListenVsock(1024)
	if err == nil {
		t.Fatal("ListenVsock should return error on non-Windows")
	}
}
