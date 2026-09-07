package config

import (
	"testing"
	"time"

	"github.com/devcell-sh/go-winkit/build/buildopts"
)

func TestToBuildOpts_BasicConversion(t *testing.T) {
	cfg := &Config{
		From:     "windows/11-pro-arm64",
		Features: []string{"OpenSSH.Server"},
		Commands: commandsField{
			Phases: map[string][]CommandEntry{
				"boot": {{Cmd: "echo hello"}},
			},
		},
	}

	opts, err := cfg.ToBuildOpts()
	if err != nil {
		t.Fatal(err)
	}
	if opts.From != "windows/11-pro-arm64" {
		t.Fatalf("from = %q", opts.From)
	}
	if len(opts.Features) != 1 || opts.Features[0] != "OpenSSH.Server" {
		t.Fatalf("features = %v", opts.Features)
	}
	if len(opts.Hooks) != 1 {
		t.Fatalf("hooks = %d", len(opts.Hooks))
	}
	if opts.Hooks[0].Phase != buildopts.Boot {
		t.Fatalf("phase = %s", opts.Hooks[0].Phase)
	}
}

func TestToBuildOpts_WSLConversion(t *testing.T) {
	cfg := &Config{
		From: "windows/11-pro-arm64",
		WSL:  &WSLConfig{Image: "alpine", Services: "./s6"},
		Commands: commandsField{
			Phases: map[string][]CommandEntry{
				"wsl": {{Cmd: "wsl -d alpine -- apk add curl"}},
			},
		},
	}

	opts, err := cfg.ToBuildOpts()
	if err != nil {
		t.Fatal(err)
	}
	if opts.WSL == nil || opts.WSL.Image != "alpine" {
		t.Fatalf("wsl = %+v", opts.WSL)
	}
	if opts.WSL.ServicesDir != "./s6" {
		t.Fatalf("wsl.ServicesDir = %q", opts.WSL.ServicesDir)
	}
}

func TestToBuildOpts_PEConversion(t *testing.T) {
	cfg := &Config{
		From: "windows/11-pro-arm64",
		PE:   true,
		Commands: commandsField{
			Phases: map[string][]CommandEntry{
				"boot": {{Cmd: "echo pe"}},
			},
		},
	}

	opts, err := cfg.ToBuildOpts()
	if err != nil {
		t.Fatal(err)
	}
	if !opts.PE {
		t.Fatal("PE should be true")
	}
}

func TestToBuildOpts_PEWSLError(t *testing.T) {
	cfg := &Config{
		From:     "windows/11-pro-arm64",
		PE:       true,
		WSL:      &WSLConfig{Image: "alpine"},
		Commands: commandsField{Phases: make(map[string][]CommandEntry)},
	}

	_, err := cfg.ToBuildOpts()
	if err == nil {
		t.Fatal("expected PE+WSL error")
	}
}

func TestToBuildOpts_HooksSortedByPhase(t *testing.T) {
	cfg := &Config{
		From: "windows/11-pro-arm64",
		Commands: commandsField{
			Phases: map[string][]CommandEntry{
				"boot":       {{Cmd: "boot-cmd"}},
				"specialize": {{Cmd: "spec-cmd"}},
				"oobe":       {{Cmd: "oobe-cmd"}},
			},
		},
	}

	opts, err := cfg.ToBuildOpts()
	if err != nil {
		t.Fatal(err)
	}

	if len(opts.Hooks) != 3 {
		t.Fatalf("hooks = %d", len(opts.Hooks))
	}
	if opts.Hooks[0].Phase != buildopts.Specialize {
		t.Fatalf("first hook phase = %s, want specialize", opts.Hooks[0].Phase)
	}
	if opts.Hooks[1].Phase != buildopts.OOBE {
		t.Fatalf("second hook phase = %s, want oobe", opts.Hooks[1].Phase)
	}
	if opts.Hooks[2].Phase != buildopts.Boot {
		t.Fatalf("third hook phase = %s, want boot", opts.Hooks[2].Phase)
	}
}

func TestToBuildOpts_DefaultsApplied(t *testing.T) {
	cfg := &Config{
		Commands: commandsField{
			Phases: map[string][]CommandEntry{
				"boot": {{Cmd: "echo hello"}},
			},
		},
	}

	opts, err := cfg.ToBuildOpts()
	if err != nil {
		t.Fatal(err)
	}
	if opts.From != "windows/11-pro-arm64" {
		t.Fatalf("default from = %q", opts.From)
	}

	h := opts.Hooks[0]
	if h.Timeout != 5*time.Minute {
		t.Fatalf("default timeout = %v", h.Timeout)
	}
	if len(h.ValidExitCodes) != 1 || h.ValidExitCodes[0] != 0 {
		t.Fatalf("default valid-exit-codes = %v", h.ValidExitCodes)
	}
}

func TestToBuildOpts_CommandOptionsPreserved(t *testing.T) {
	cfg := &Config{
		From: "windows/11-pro-arm64",
		Commands: commandsField{
			Phases: map[string][]CommandEntry{
				"boot": {{
					Cmd:            "long.ps1",
					Timeout:        duration(10 * time.Minute),
					Retries:        2,
					ValidExitCodes: []int{0, 3010},
					Reboot:         true,
				}},
			},
		},
	}

	opts, err := cfg.ToBuildOpts()
	if err != nil {
		t.Fatal(err)
	}

	h := opts.Hooks[0]
	if h.Timeout != 10*time.Minute {
		t.Fatalf("timeout = %v", h.Timeout)
	}
	if h.Retries != 2 {
		t.Fatalf("retries = %d", h.Retries)
	}
	if len(h.ValidExitCodes) != 2 {
		t.Fatalf("valid-exit-codes = %v", h.ValidExitCodes)
	}
	if !h.Reboot {
		t.Fatal("reboot should be true")
	}
}

func TestToBuildOpts_EmptyConfig(t *testing.T) {
	cfg := &Config{
		Commands: commandsField{Phases: make(map[string][]CommandEntry)},
	}

	opts, err := cfg.ToBuildOpts()
	if err != nil {
		t.Fatal(err)
	}
	if opts.From != "windows/11-pro-arm64" {
		t.Fatalf("from = %q", opts.From)
	}
	if len(opts.Hooks) != 0 {
		t.Fatalf("hooks = %d", len(opts.Hooks))
	}
}

func TestToBuildOpts_FileInject(t *testing.T) {
	cfg := &Config{
		From: "windows/11-pro-arm64",
		Files: []FileEntry{
			{Src: "./tools.ps1", Dest: "C:/scripts/tools.ps1"},
		},
		Commands: commandsField{
			Phases: map[string][]CommandEntry{
				"boot": {{Cmd: "echo hello"}},
			},
		},
	}

	opts, err := cfg.ToBuildOpts()
	if err != nil {
		t.Fatal(err)
	}

	var bootHookWithFiles *buildopts.Hook
	for i := range opts.Hooks {
		if opts.Hooks[i].Phase == buildopts.Boot && len(opts.Hooks[i].Files) > 0 {
			bootHookWithFiles = &opts.Hooks[i]
			break
		}
	}
	if bootHookWithFiles == nil {
		t.Fatal("no boot hook with files found")
	}
	if bootHookWithFiles.Files[0].Dest != "C:/scripts/tools.ps1" {
		t.Fatalf("file dest = %q", bootHookWithFiles.Files[0].Dest)
	}
}
