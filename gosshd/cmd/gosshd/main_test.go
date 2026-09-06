package main

import (
	"testing"

	"github.com/devcell-sh/go-winkit/gosshd"
)

// TestParseArgs pins the flag/positional layout. WinPE callers pass only
// positionals (log, structured-port) and must keep the default address; the
// wsl2 provisioning channel passes -addr before the positionals to move off
// :22 so it can coexist with the Windows OpenSSH the image ships there.
func TestParseArgs(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		addr       string
		shell      string
		vsockPort  uint
		logfile    string
		structPort string
	}{
		{"none", nil, gosshd.DefaultAddr, "cmd", 0, "", ""},
		{"winpe positionals", []string{`C:\gosshd.log`, `\\.\Global\winkit.structured.0`},
			gosshd.DefaultAddr, "cmd", 0, `C:\gosshd.log`, `\\.\Global\winkit.structured.0`},
		{"addr only", []string{"-addr", ":2222"}, ":2222", "cmd", 0, "", ""},
		{"wsl2 provisioning: addr+shell then log", []string{"-addr", ":2222", "-shell", "powershell", `C:\gosshd.log`},
			":2222", "powershell", 0, `C:\gosshd.log`, ""},
		{"log only", []string{`C:\gosshd.log`}, gosshd.DefaultAddr, "cmd", 0, `C:\gosshd.log`, ""},
		{"vsock port", []string{"-vsock-port", "1024", "-addr", ":2222"},
			":2222", "cmd", 1024, "", ""},
		{"vsock with positionals", []string{"-vsock-port", "2048", `C:\gosshd.log`},
			gosshd.DefaultAddr, "cmd", 2048, `C:\gosshd.log`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			addr, shell, vsockPort, logfile, structPort := parseArgs(tc.args)
			if addr != tc.addr || shell != tc.shell || vsockPort != tc.vsockPort || logfile != tc.logfile || structPort != tc.structPort {
				t.Errorf("parseArgs(%v) = (%q,%q,%d,%q,%q); want (%q,%q,%d,%q,%q)",
					tc.args, addr, shell, vsockPort, logfile, structPort,
					tc.addr, tc.shell, tc.vsockPort, tc.logfile, tc.structPort)
			}
		})
	}
}
