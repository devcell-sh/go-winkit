package gosshd

import (
	"reflect"
	"testing"
)

// TestSessionCommandShell locks the shell selection. cmd (default) preserves the
// WinPE-safe behavior; powershell passes the request as a single -Command arg so
// the wsl2 host can send a PowerShell one-liner verbatim (no cmd re-parsing),
// which is why feature-enable over gosshd exited 255 before this existed.
func TestSessionCommandShell(t *testing.T) {
	cases := []struct {
		name    string
		request string
		shell   string
		want    []string
	}{
		{"cmd default command", "whoami", "", []string{"cmd.exe", "/c", "whoami"}},
		{"cmd default shell", "", "cmd", []string{"cmd.exe"}},
		{"powershell command", "$x=1; 'ok'", "powershell",
			[]string{"powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "$x=1; 'ok'"}},
		{"powershell interactive", "", "powershell", []string{"powershell.exe", "-NoProfile"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SessionCommandShell(tc.request, tc.shell); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("SessionCommandShell(%q,%q) = %v; want %v", tc.request, tc.shell, got, tc.want)
			}
		})
	}
	// SessionCommand stays the cmd form for existing (WinPE) callers.
	if got := SessionCommand("dir"); !reflect.DeepEqual(got, []string{"cmd.exe", "/c", "dir"}) {
		t.Errorf("SessionCommand default drifted: %v", got)
	}
}
