package config

import (
	"testing"
)

func TestPackagesConfig(t *testing.T) {
	yaml := []byte(`
from: windows/11-pro-arm64
pe: true
packages:
  chocolatey:
    - 7zip
    - totalcommander
wsl:
  image: nix
`)
	cfg, err := Parse(yaml)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Packages == nil {
		t.Fatal("packages is nil")
	}
	if len(cfg.Packages.Chocolatey) != 2 {
		t.Fatalf("chocolatey packages: got %d, want 2", len(cfg.Packages.Chocolatey))
	}
	if cfg.Packages.Chocolatey[0] != "7zip" {
		t.Fatalf("chocolatey[0] = %q, want 7zip", cfg.Packages.Chocolatey[0])
	}
	if cfg.Packages.Chocolatey[1] != "totalcommander" {
		t.Fatalf("chocolatey[1] = %q, want totalcommander", cfg.Packages.Chocolatey[1])
	}

	opts, err := cfg.ToBuildOpts()
	if err != nil {
		t.Fatalf("ToBuildOpts: %v", err)
	}
	if len(opts.Packages.Chocolatey) != 2 {
		t.Fatalf("buildopts chocolatey: got %d, want 2", len(opts.Packages.Chocolatey))
	}
	if !opts.Packages.NeedsNetFx() {
		t.Fatal("NeedsNetFx() should be true")
	}
	if !opts.Packages.NeedsWoW64() {
		t.Fatal("NeedsWoW64() should be true")
	}
}

func TestPackagesConfig_Empty(t *testing.T) {
	yaml := []byte(`
from: windows/11-pro-arm64
pe: true
wsl:
  image: nix
`)
	cfg, err := Parse(yaml)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Packages != nil {
		t.Fatal("packages should be nil when not set")
	}

	opts, err := cfg.ToBuildOpts()
	if err != nil {
		t.Fatalf("ToBuildOpts: %v", err)
	}
	if opts.Packages.NeedsNetFx() {
		t.Fatal("NeedsNetFx() should be false with no packages")
	}
}
