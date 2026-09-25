package winpe

import (
	"strings"
	"testing"
)

func TestGenerateGosshdShellCmdLaunchesStartupBeforeForegroundSSH(t *testing.T) {
	out := string(generateGosshdShellCmd(nil, false, ":2222", WSL1PEStartupCommand))
	startup := strings.Index(out, `start "" /min cmd.exe /d /c `+WSL1PEStartupCommand)
	server := strings.Index(out, `X:\winkit\gosshd.exe -addr :2222`)
	if startup < 0 || server < 0 || startup >= server {
		t.Fatalf("startup must run before gosshd:\n%s", out)
	}
	if !strings.Contains(out, `pwsh.exe -NoLogo -NoProfile -NonInteractive`) {
		t.Fatalf("WSL1 startup must immediately delegate to PowerShell:\n%s", out)
	}
}

func TestGenerateGosshdShellCmdKeepsLegacySignature(t *testing.T) {
	out := string(GenerateGosshdShellCmd(nil, false, ":2222"))
	if strings.Contains(out, "bootstrap-wsl1") {
		t.Fatalf("legacy generator unexpectedly added a bootstrap:\n%s", out)
	}
	if !strings.Contains(out, `-addr :2222`) {
		t.Fatalf("listen address missing:\n%s", out)
	}
}
