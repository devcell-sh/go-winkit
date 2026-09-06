package qemu

import (
	"strings"
	"testing"
)

// Directory sharing moved to the host-side WebDAV server (webdavshare);
// QEMU's slirp smb= option required a real Samba smbd on the host and never
// worked on macOS. No argv builder may emit it.
func TestNoSlirpSMBInArgv(t *testing.T) {
	spec := Spec{VMName: "t", DiskPath: "/tmp/d.qcow2", SSHPort: 2222}
	for name, argv := range map[string][]string{
		"run":   BuildRunCommand(spec),
		"setup": BuildSetupBootArgv(spec, "/tmp/boot.img", "/tmp/win.iso", "/tmp/ans.img"),
	} {
		joined := strings.Join(argv, " ")
		if strings.Contains(joined, "smb=") {
			t.Errorf("%s argv still contains slirp smb=: %s", name, joined)
		}
	}
}
