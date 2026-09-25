package winpe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWSL1PERuntimeHelpersOwnBootstrapAndSSHCLI(t *testing.T) {
	helpers := WSL1PERuntimeHelpers()
	for _, name := range []string{
		"prepare-wsl1-disk.txt", "relocate-wsl.ps1", "setup-wsl-user.ps1",
		"probe-wsl1.ps1", "bootstrap-wsl1.ps1", "status-wsl1.ps1", "invoke-wsl.ps1",
		"wsl.cmd",
	} {
		if helpers[name] == "" {
			t.Fatalf("missing helper %s", name)
		}
	}
	bootstrap := helpers["bootstrap-wsl1.ps1"]
	for _, want := range []string{"add-catalogs", "ensure-service", "ensure-user", "WSLService", "probe-wsl1.ps1", "wsl1-bootstrap.ok"} {
		if !strings.Contains(bootstrap, want) {
			t.Errorf("bootstrap missing %q", want)
		}
	}
	for name, helper := range helpers {
		if strings.Contains(helper, "{{") {
			t.Errorf("helper %s contains an unresolved template placeholder", name)
		}
		for _, forbidden := range []string{"findstr", "sc query", "for /f"} {
			if strings.Contains(strings.ToLower(helper), forbidden) {
				t.Errorf("helper %s contains legacy batch parsing %q", name, forbidden)
			}
		}
	}
	wrapper := helpers["wsl.cmd"]
	if !strings.Contains(wrapper, "pwsh.exe") || !strings.Contains(wrapper, "invoke-wsl.ps1") || !strings.Contains(wrapper, "%*") {
		t.Fatalf("SSH wsl shim must immediately delegate arguments to PowerShell:\n%s", wrapper)
	}
	if !strings.Contains(helpers["invoke-wsl.ps1"], "run-user") ||
		!strings.Contains(helpers["invoke-wsl.ps1"], `E:\Program Files\WSL\wsl.exe`) ||
		!strings.Contains(helpers["invoke-wsl.ps1"], "+ $args") {
		t.Fatal("PowerShell WSL wrapper must preserve arguments in the WSL user context")
	}
	if !strings.Contains(helpers["probe-wsl1.ps1"], "run-user") {
		t.Fatal("PowerShell probe must launch each wsl.exe operation through the user-token helper")
	}
	setupUser := helpers["setup-wsl-user.ps1"]
	for _, want := range []string{"RuntimePath", "DistroPath", "(OI)(CI)RX", "(OI)(CI)F"} {
		if !strings.Contains(setupUser, want) {
			t.Errorf("user setup missing ACL contract %q", want)
		}
	}
}

func TestWSL1PEPatchSetStagesEngineRegistryAndHelpers(t *testing.T) {
	wslDir := t.TempDir()
	for _, name := range WSL1EngineFiles() {
		path := filepath.Join(wslDir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("payload"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	patch, err := WSL1PEPatchSet(t.TempDir(), wslDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(patch.KeyWrites) == 0 {
		t.Fatal("service/COM registry writes missing")
	}
	for name := range WSL1PERuntimeHelpers() {
		path := patch.Files[`\winkit\`+name]
		if path == "" {
			t.Errorf("helper %s missing from patch", name)
		}
	}
}
