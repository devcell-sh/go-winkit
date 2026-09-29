package winpe

import (
	"encoding/json"
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

func TestGenerateInitManifest(t *testing.T) {
	drivers := []string{`X:\winkit\drivers\netkvm.inf`, `X:\winkit\drivers\vioscsi.inf`}
	data := GenerateInitManifest(drivers, ":2222", "winkit", "Winkit1234", `X:\winkit\bootstrap-wsl1.ps1`)

	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if m["gosshd"] != `X:\winkit\gosshd.exe` {
		t.Fatalf("gosshd = %v", m["gosshd"])
	}
	if m["gosshdAddr"] != ":2222" {
		t.Fatalf("gosshdAddr = %v", m["gosshdAddr"])
	}
	if m["user"] != "winkit" {
		t.Fatalf("user = %v", m["user"])
	}
	if m["wsl1Bootstrap"] != `X:\winkit\bootstrap-wsl1.ps1` {
		t.Fatalf("wsl1Bootstrap = %v", m["wsl1Bootstrap"])
	}
	ds := m["drivers"].([]interface{})
	if len(ds) != 2 {
		t.Fatalf("drivers count = %d", len(ds))
	}
	// Sorted: netkvm before vioscsi
	if ds[0] != `X:\winkit\drivers\netkvm.inf` {
		t.Fatalf("drivers[0] = %v (expected sorted)", ds[0])
	}
	if m["wsl1CatalogDir"] != `X:\Windows\winkit\WSL1Catalogs` {
		t.Fatalf("wsl1CatalogDir = %v", m["wsl1CatalogDir"])
	}
	svcs := m["wsl1Services"].([]interface{})
	if len(svcs) != 5 {
		t.Fatalf("wsl1Services count = %d", len(svcs))
	}
}

func TestGenerateInitManifest_NoWSL1(t *testing.T) {
	data := GenerateInitManifest(nil, "", "winkit", "pass", "")
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, ok := m["wsl1Bootstrap"]; ok {
		t.Fatal("wsl1Bootstrap should be omitted when empty")
	}
	if _, ok := m["wsl1CatalogDir"]; ok {
		t.Fatal("wsl1CatalogDir should be omitted when no WSL1")
	}
	if _, ok := m["wsl1Services"]; ok {
		t.Fatal("wsl1Services should be omitted when no WSL1")
	}
}
