package config

import "testing"

func TestExample_ParsesCleanly(t *testing.T) {
	cfg, err := Parse([]byte(Example))
	if err != nil {
		t.Fatalf("Example yaml failed to parse: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Example yaml failed validation: %v", err)
	}
	if cfg.From != "windows/11-pro-arm64" {
		t.Fatalf("from = %q", cfg.From)
	}
}
