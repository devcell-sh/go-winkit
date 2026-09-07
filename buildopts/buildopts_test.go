package buildopts

import (
	"testing"
	"time"
)

func TestValidate_NoHooksIsValid(t *testing.T) {
	opts := BuildOpts{From: "windows/11-pro-arm64"}
	if err := opts.Validate(); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}
}

func TestValidate_PEAndWSLMutuallyExclusive(t *testing.T) {
	opts := BuildOpts{
		From: "windows/11-pro-arm64",
		PE:   true,
		WSL:  &WSLConfig{Image: "alpine"},
	}
	err := opts.Validate()
	if err == nil {
		t.Fatal("expected error for PE + WSL")
	}
	if want := "mutually exclusive"; !contains(err.Error(), want) {
		t.Fatalf("error %q should contain %q", err, want)
	}
}

func TestValidate_PEOnlyBootPhase(t *testing.T) {
	opts := BuildOpts{
		From: "windows/11-pro-arm64",
		PE:   true,
		Hooks: []Hook{
			{Phase: Specialize, Cmd: "reg add ..."},
		},
	}
	err := opts.Validate()
	if err == nil {
		t.Fatal("expected error for non-boot hook in PE mode")
	}

	opts.Hooks[0].Phase = Boot
	if err := opts.Validate(); err != nil {
		t.Fatalf("boot hook in PE mode should be valid: %v", err)
	}
}

func TestValidate_WSLPhaseRequiresWSL(t *testing.T) {
	opts := BuildOpts{
		From:  "windows/11-pro-arm64",
		Hooks: []Hook{{Phase: WSLPhase, Cmd: "wsl -d alpine -- echo hi"}},
	}
	err := opts.Validate()
	if err == nil {
		t.Fatal("expected error for wsl hook without wsl enabled")
	}

	opts.WSL = &WSLConfig{Image: "alpine"}
	if err := opts.Validate(); err != nil {
		t.Fatalf("wsl hook with wsl enabled should be valid: %v", err)
	}
}

func TestApplyDefaults_From(t *testing.T) {
	opts := BuildOpts{}
	opts.ApplyDefaults()
	if opts.From != "windows/11-pro-arm64" {
		t.Fatalf("default from = %q, want windows/11-pro-arm64", opts.From)
	}
}

func TestApplyDefaults_HookDefaults(t *testing.T) {
	opts := BuildOpts{
		Hooks: []Hook{{Phase: Boot, Cmd: "echo hello"}},
	}
	opts.ApplyDefaults()

	h := opts.Hooks[0]
	if h.Timeout != 5*time.Minute {
		t.Fatalf("timeout = %v, want 5m", h.Timeout)
	}
	if len(h.ValidExitCodes) != 1 || h.ValidExitCodes[0] != 0 {
		t.Fatalf("valid-exit-codes = %v, want [0]", h.ValidExitCodes)
	}
	if h.Retries != 0 {
		t.Fatalf("retries = %d, want 0", h.Retries)
	}
	if h.Reboot {
		t.Fatal("reboot should default to false")
	}
}

func TestApplyDefaults_PreservesExplicitValues(t *testing.T) {
	opts := BuildOpts{
		From: "windows/10-pro-arm64",
		Hooks: []Hook{{
			Phase:          Boot,
			Cmd:            "long.ps1",
			Timeout:        10 * time.Minute,
			ValidExitCodes: []int{0, 3010},
			Retries:        2,
			Reboot:         true,
		}},
	}
	opts.ApplyDefaults()

	if opts.From != "windows/10-pro-arm64" {
		t.Fatalf("from overwritten: %q", opts.From)
	}
	h := opts.Hooks[0]
	if h.Timeout != 10*time.Minute {
		t.Fatalf("timeout overwritten: %v", h.Timeout)
	}
	if len(h.ValidExitCodes) != 2 {
		t.Fatalf("valid-exit-codes overwritten: %v", h.ValidExitCodes)
	}
	if h.Retries != 2 {
		t.Fatalf("retries overwritten: %d", h.Retries)
	}
}

func TestSortHooks_PhaseOrder(t *testing.T) {
	opts := BuildOpts{
		WSL: &WSLConfig{Image: "alpine"},
		Hooks: []Hook{
			{Phase: Boot, Cmd: "boot1"},
			{Phase: Specialize, Cmd: "spec1"},
			{Phase: WSLPhase, Cmd: "wsl1"},
			{Phase: OOBE, Cmd: "oobe1"},
		},
	}
	opts.SortHooks()

	want := []HookPhase{Specialize, OOBE, Boot, WSLPhase}
	for i, h := range opts.Hooks {
		if h.Phase != want[i] {
			t.Fatalf("hook[%d].Phase = %s, want %s", i, h.Phase, want[i])
		}
	}
}

func TestSortHooks_StableWithinPhase(t *testing.T) {
	opts := BuildOpts{
		Hooks: []Hook{
			{Phase: Boot, Cmd: "b1"},
			{Phase: Boot, Cmd: "b2"},
			{Phase: Specialize, Cmd: "s1"},
		},
	}
	opts.SortHooks()

	if opts.Hooks[1].Cmd != "b1" || opts.Hooks[2].Cmd != "b2" {
		t.Fatalf("boot hooks reordered: %s, %s", opts.Hooks[1].Cmd, opts.Hooks[2].Cmd)
	}
}

func TestDetectFrom(t *testing.T) {
	tests := []struct {
		input string
		want  FromKind
	}{
		{"windows/11-pro-arm64", FromMCT},
		{"windows/10-enterprise-amd64", FromMCT},
		{"./path/to/windows.iso", FromISO},
		{"./install.ISO", FromISO},
		{"./path/to/install.wim", FromWIM},
		{"./boot.WIM", FromWIM},
	}
	for _, tt := range tests {
		got := DetectFrom(tt.input)
		if got != tt.want {
			t.Errorf("DetectFrom(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestHookPhase_String(t *testing.T) {
	tests := []struct {
		phase HookPhase
		want  string
	}{
		{Specialize, "specialize"},
		{OOBE, "oobe"},
		{Boot, "boot"},
		{WSLPhase, "wsl"},
	}
	for _, tt := range tests {
		if got := tt.phase.String(); got != tt.want {
			t.Errorf("HookPhase(%d).String() = %q, want %q", tt.phase, got, tt.want)
		}
	}
}

func TestWSLNilMeansDisabled(t *testing.T) {
	opts := BuildOpts{From: "windows/11-pro-arm64"}
	if opts.WSL != nil {
		t.Fatal("WSL should be nil by default")
	}
	if err := opts.Validate(); err != nil {
		t.Fatalf("nil WSL should be valid: %v", err)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsAt(s, sub))
}

func containsAt(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
