package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParse_MinimalFromOnly(t *testing.T) {
	cfg, err := Parse([]byte(`from: windows/11-pro-arm64`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.From != "windows/11-pro-arm64" {
		t.Fatalf("from = %q", cfg.From)
	}
	if cfg.PE {
		t.Fatal("pe should be false")
	}
	if cfg.WSL != nil {
		t.Fatal("wsl should be nil")
	}
}

func TestParse_FullConfig(t *testing.T) {
	yaml := `
from: windows/11-pro-arm64
wsl:
  image: alpine
features:
  - OpenSSH.Server
  - Containers
files:
  - src: ./tools.ps1
    dest: C:/scripts/tools.ps1
commands:
  specialize:
    - reg add HKLM\SOFTWARE\MyApp /v Key /d Value
  boot:
    - powershell C:/scripts/tools.ps1
  wsl:
    - wsl -d alpine -- apk add curl
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}

	if cfg.WSL == nil || cfg.WSL.Image != "alpine" {
		t.Fatalf("wsl = %+v", cfg.WSL)
	}
	if len(cfg.Features) != 2 {
		t.Fatalf("features = %v", cfg.Features)
	}
	if len(cfg.Files) != 1 || cfg.Files[0].Dest != "C:/scripts/tools.ps1" {
		t.Fatalf("files = %+v", cfg.Files)
	}
	if len(cfg.Commands.Phases) != 3 {
		t.Fatalf("phases = %v", cfg.Commands.Phases)
	}
	if len(cfg.Commands.Phases["specialize"]) != 1 {
		t.Fatalf("specialize commands = %v", cfg.Commands.Phases["specialize"])
	}
	if len(cfg.Commands.Phases["boot"]) != 1 {
		t.Fatalf("boot commands = %v", cfg.Commands.Phases["boot"])
	}
	if len(cfg.Commands.Phases["wsl"]) != 1 {
		t.Fatalf("wsl commands = %v", cfg.Commands.Phases["wsl"])
	}
}

func TestParse_FlatCommandsDefaultToBoot(t *testing.T) {
	yaml := `
from: windows/11-pro-arm64
commands:
  - powershell C:/scripts/tools.ps1
  - choco install git -y
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}

	boot := cfg.Commands.Phases["boot"]
	if len(boot) != 2 {
		t.Fatalf("boot commands = %d, want 2", len(boot))
	}
	if boot[0].Cmd != "powershell C:/scripts/tools.ps1" {
		t.Fatalf("cmd[0] = %q", boot[0].Cmd)
	}
	if boot[1].Cmd != "choco install git -y" {
		t.Fatalf("cmd[1] = %q", boot[1].Cmd)
	}
}

func TestParse_CommandWithOptions(t *testing.T) {
	yaml := `
from: windows/11-pro-arm64
commands:
  boot:
    - cmd: "powershell C:/scripts/long-task.ps1"
      timeout: 10m
      retries: 2
      valid-exit-codes: [0, 3010]
      reboot: true
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}

	boot := cfg.Commands.Phases["boot"]
	if len(boot) != 1 {
		t.Fatalf("boot commands = %d", len(boot))
	}
	cmd := boot[0]
	if cmd.Cmd != "powershell C:/scripts/long-task.ps1" {
		t.Fatalf("cmd = %q", cmd.Cmd)
	}
	if cmd.Timeout.Duration() != 10*time.Minute {
		t.Fatalf("timeout = %v", cmd.Timeout.Duration())
	}
	if cmd.Retries != 2 {
		t.Fatalf("retries = %d", cmd.Retries)
	}
	if len(cmd.ValidExitCodes) != 2 || cmd.ValidExitCodes[0] != 0 || cmd.ValidExitCodes[1] != 3010 {
		t.Fatalf("valid-exit-codes = %v", cmd.ValidExitCodes)
	}
	if !cmd.Reboot {
		t.Fatal("reboot should be true")
	}
}

func TestParse_MissingFromUsesEmpty(t *testing.T) {
	cfg, err := Parse([]byte(`features: [OpenSSH.Server]`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.From != "" {
		t.Fatalf("from should be empty, got %q", cfg.From)
	}
}

func TestParse_PEMode(t *testing.T) {
	yaml := `
from: windows/11-pro-arm64
pe: true
features:
  - OpenSSH.Server
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.PE {
		t.Fatal("pe should be true")
	}
}

func TestValidate_PEAndWSLError(t *testing.T) {
	cfg := &Config{
		From: "windows/11-pro-arm64",
		PE:   true,
		WSL:  &WSLConfig{Image: "alpine"},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for PE + WSL")
	}
}

func TestValidate_PENonBootPhaseError(t *testing.T) {
	cfg := &Config{
		From: "windows/11-pro-arm64",
		PE:   true,
		Commands: commandsField{
			Phases: map[string][]CommandEntry{
				"specialize": {{Cmd: "reg add ..."}},
			},
		},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for specialize in PE mode")
	}
}

func TestValidate_WSLPhaseWithoutWSLError(t *testing.T) {
	cfg := &Config{
		From: "windows/11-pro-arm64",
		Commands: commandsField{
			Phases: map[string][]CommandEntry{
				"wsl": {{Cmd: "wsl -d alpine"}},
			},
		},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for wsl phase without wsl config")
	}
}

func TestValidate_UnknownPhaseError(t *testing.T) {
	cfg := &Config{
		From: "windows/11-pro-arm64",
		Commands: commandsField{
			Phases: map[string][]CommandEntry{
				"postinstall": {{Cmd: "echo hi"}},
			},
		},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for unknown phase")
	}
}

func TestValidate_ValidFullConfig(t *testing.T) {
	cfg := &Config{
		From: "windows/11-pro-arm64",
		WSL:  &WSLConfig{Image: "alpine"},
		Commands: commandsField{
			Phases: map[string][]CommandEntry{
				"boot": {{Cmd: "echo hi"}},
				"wsl":  {{Cmd: "wsl -d alpine"}},
			},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestParse_InvalidYAML(t *testing.T) {
	_, err := Parse([]byte(`{{{not yaml`))
	if err == nil {
		t.Fatal("expected error for invalid yaml")
	}
}

func TestDiscover_Yaml(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "winkit.yaml"), []byte("from: windows/11-pro-arm64\n"), 0o644)

	path, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "winkit.yaml" {
		t.Fatalf("path = %q", path)
	}
}

func TestDiscover_Yml(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "winkit.yml"), []byte("from: windows/11-pro-arm64\n"), 0o644)

	path, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "winkit.yml" {
		t.Fatalf("path = %q", path)
	}
}

func TestDiscover_YamlPreferredOverYml(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "winkit.yaml"), []byte("from: windows/11-pro-arm64\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "winkit.yml"), []byte("from: windows/10-pro-arm64\n"), 0o644)

	path, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "winkit.yaml" {
		t.Fatalf("expected .yaml to win, got %q", path)
	}
}

func TestDiscover_NoneFound(t *testing.T) {
	dir := t.TempDir()
	_, err := Discover(dir)
	if err == nil {
		t.Fatal("expected error when no config found")
	}
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := Load("/nonexistent/winkit.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoad_FromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "winkit.yaml")
	os.WriteFile(path, []byte("from: windows/11-pro-arm64\n"), 0o644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.From != "windows/11-pro-arm64" {
		t.Fatalf("from = %q", cfg.From)
	}
}

func TestLoadDirectoryHooks(t *testing.T) {
	dir := t.TempDir()

	bootDir := filepath.Join(dir, "hooks", "boot")
	os.MkdirAll(bootDir, 0o755)
	os.WriteFile(filepath.Join(bootDir, "02_install_tools.ps1"), []byte("choco install git"), 0o644)
	os.WriteFile(filepath.Join(bootDir, "01_setup.ps1"), []byte("Set-ExecutionPolicy Bypass"), 0o644)

	specDir := filepath.Join(dir, "hooks", "specialize")
	os.MkdirAll(specDir, 0o755)
	os.WriteFile(filepath.Join(specDir, "01_reg.ps1"), []byte("reg add HKLM\\Test"), 0o644)

	cfg := &Config{
		From: "windows/11-pro-arm64",
		Commands: commandsField{
			Phases: map[string][]CommandEntry{
				"boot": {{Cmd: "yaml-cmd"}},
			},
		},
	}

	if err := cfg.LoadDirectoryHooks(dir); err != nil {
		t.Fatal(err)
	}

	boot := cfg.Commands.Phases["boot"]
	if len(boot) != 3 {
		t.Fatalf("boot commands = %d, want 3 (1 yaml + 2 dir)", len(boot))
	}
	if boot[0].Cmd != "yaml-cmd" {
		t.Fatalf("first boot cmd should be yaml cmd, got %q", boot[0].Cmd)
	}
	if boot[1].Cmd != "Set-ExecutionPolicy Bypass" {
		t.Fatalf("second boot cmd should be 01_setup, got %q", boot[1].Cmd)
	}
	if boot[2].Cmd != "choco install git" {
		t.Fatalf("third boot cmd should be 02_install_tools, got %q", boot[2].Cmd)
	}

	spec := cfg.Commands.Phases["specialize"]
	if len(spec) != 1 || spec[0].Cmd != "reg add HKLM\\Test" {
		t.Fatalf("specialize = %+v", spec)
	}
}

func TestLoadDirectoryHooks_NoHooksDir(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{From: "windows/11-pro-arm64", Commands: commandsField{Phases: make(map[string][]CommandEntry)}}
	if err := cfg.LoadDirectoryHooks(dir); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Commands.Phases) != 0 {
		t.Fatalf("expected no phases, got %v", cfg.Commands.Phases)
	}
}
