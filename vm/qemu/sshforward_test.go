package qemu

import (
	"strings"
	"testing"
)

// netdevArg returns the -netdev value from an argv, or "" if absent.
func netdevArg(argv []string) string {
	for i, a := range argv {
		if a == "-netdev" && i+1 < len(argv) {
			return argv[i+1]
		}
	}
	return ""
}

// TestApplySSHForward_GuestPortAndOpenSSH locks the wsl forwarding shape: the
// SSH forward targets the gosshd provisioning guest port (2222), and a separate
// forward exposes the Windows OpenSSH the image ships on :22. Zero SSHGuestPort
// must keep the historical :22 target so WinPE/base are unaffected.
func TestApplySSHForward_GuestPortAndOpenSSH(t *testing.T) {
	t.Run("wsl: gosshd guest port + openssh forward", func(t *testing.T) {
		spec := Spec{SSHPort: 20022, SSHGuestPort: 2222, OpenSSHHostPort: 20122, RDPPort: 23389}
		nd := netdevArg(applySSHForward(spec, []string{"-netdev", "user,id=net0"}))
		for _, want := range []string{
			"hostfwd=tcp:127.0.0.1:20022-:2222", // host → gosshd provisioning
			"hostfwd=tcp:127.0.0.1:20122-:22",   // host → Windows OpenSSH
			"hostfwd=tcp:127.0.0.1:23389-:3389", // RDP
		} {
			if !strings.Contains(nd, want) {
				t.Errorf("netdev %q missing %q", nd, want)
			}
		}
	})

	t.Run("default guest port stays :22", func(t *testing.T) {
		spec := Spec{SSHPort: 20022}
		nd := netdevArg(applySSHForward(spec, []string{"-netdev", "user,id=net0"}))
		if !strings.Contains(nd, "hostfwd=tcp:127.0.0.1:20022-:22") {
			t.Errorf("netdev %q should forward to guest :22 by default", nd)
		}
		if strings.Contains(nd, "-:2222") {
			t.Errorf("netdev %q should not mention 2222 without SSHGuestPort", nd)
		}
	})
}
