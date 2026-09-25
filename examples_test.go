package winkit

import (
	"path/filepath"
	"testing"

	"github.com/devcell-sh/go-winkit/internal/config"
	"github.com/devcell-sh/go-winkit/s6"
)

// The examples double as regression fixtures: schema changes that break
// them break these tests, not a user's first contact with winkit.

func loadExample(t *testing.T, name string) *config.Config {
	t.Helper()
	cfg, err := config.Load(filepath.Join("examples", name, "winkit.yaml"))
	if err != nil {
		t.Fatalf("examples/%s/winkit.yaml must load: %v", name, err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("examples/%s/winkit.yaml must validate: %v", name, err)
	}
	return cfg
}

func TestExampleWSLAlpine(t *testing.T) {
	cfg := loadExample(t, "wsl-alpine")

	if cfg.WSL == nil || cfg.WSL.Image != "alpine" {
		t.Fatal("wsl-alpine example must use the opaque alpine image")
	}

	opts, err := cfg.ToBuildOpts()
	if err != nil {
		t.Fatal(err)
	}
	if opts.WSL.ServicesDir == "" {
		t.Fatal("wsl-alpine example must declare wsl.services")
	}

	// The declared services dir must be a loadable s6 scan dir.
	svcs, err := s6.LoadDir(filepath.Join("examples", "wsl-alpine", opts.WSL.ServicesDir))
	if err != nil {
		t.Fatalf("services dir must load as s6 scan dir: %v", err)
	}
	if len(svcs) == 0 {
		t.Fatal("wsl-alpine example must ship at least one service")
	}
	for _, s := range svcs {
		if err := s.Validate(); err != nil {
			t.Errorf("service %s invalid: %v", s.Name, err)
		}
	}

	// A wsl-phase hook must be present (the "install htop" consumer noun).
	var wslHooks int
	for _, h := range opts.Hooks {
		if h.Phase.String() == "wsl" {
			wslHooks++
		}
	}
	if wslHooks == 0 {
		t.Fatal("wsl-alpine example must carry a wsl-phase command")
	}
}

func TestExamplePEMinimal(t *testing.T) {
	cfg := loadExample(t, "pe-minimal")

	opts, err := cfg.ToBuildOpts()
	if err != nil {
		t.Fatal(err)
	}
	if !opts.PE {
		t.Fatal("pe-minimal example must set pe: true")
	}
	if opts.WSL != nil {
		t.Fatal("pe-minimal example must not enable WSL")
	}
	if err := opts.Validate(); err != nil {
		t.Fatalf("pe-minimal opts must validate: %v", err)
	}
}

func TestExamplePEWSL1Alpine(t *testing.T) {
	cfg := loadExample(t, "pe-wsl1-alpine")

	opts, err := cfg.ToBuildOpts()
	if err != nil {
		t.Fatal(err)
	}
	if !opts.PE {
		t.Fatal("pe-wsl1-alpine example must set pe: true")
	}
	if opts.WSL == nil || opts.WSL.Image != "alpine" {
		t.Fatal("pe-wsl1-alpine example must request the Alpine WSL1 distro")
	}
	if err := opts.Validate(); err != nil {
		t.Fatalf("pe-wsl1-alpine opts must validate: %v", err)
	}
}
