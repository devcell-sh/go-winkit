package winpe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWSL1PERuntimeHelpersContainNonCodeAssets(t *testing.T) {
	helpers := WSL1PERuntimeHelpers()
	for _, name := range []string{
		"prepare-wsl1-disk.txt",
		"wsl.cmd",
	} {
		if helpers[name] == "" {
			t.Fatalf("missing helper %s", name)
		}
	}
	for name, helper := range helpers {
		if strings.Contains(helper, "{{") {
			t.Errorf("helper %s contains an unresolved template placeholder", name)
		}
	}
	wrapper := helpers["wsl.cmd"]
	if !strings.Contains(wrapper, "winkit-service.exe") || !strings.Contains(wrapper, "run-user") || !strings.Contains(wrapper, "%*") {
		t.Fatalf("SSH wsl shim must delegate arguments through winkit-service run-user:\n%s", wrapper)
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
