package winkit

import (
	"os"
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

func TestExampleFullWSL1Alpine(t *testing.T) {
	cfg := loadExample(t, "full-wsl1-alpine")

	if cfg.WSL == nil || cfg.WSL.Image != "alpine" {
		t.Fatal("full-wsl1-alpine example must use the opaque alpine image")
	}

	opts, err := cfg.ToBuildOpts()
	if err != nil {
		t.Fatal(err)
	}
	if opts.WSL.ServicesDir == "" {
		t.Fatal("full-wsl1-alpine example must declare wsl.services")
	}

	// The declared services dir must be a loadable s6 scan dir.
	svcs, err := s6.LoadDir(filepath.Join("examples", "full-wsl1-alpine", opts.WSL.ServicesDir))
	if err != nil {
		t.Fatalf("services dir must load as s6 scan dir: %v", err)
	}
	if len(svcs) == 0 {
		t.Fatal("full-wsl1-alpine example must ship at least one service")
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
		t.Fatal("full-wsl1-alpine example must carry a wsl-phase command")
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

func TestExamplePEWSL1NixHomeManager(t *testing.T) {
	cfg := loadExample(t, "pe-wsl1-nix-home-manager")

	opts, err := cfg.ToBuildOpts()
	if err != nil {
		t.Fatal(err)
	}
	if !opts.PE {
		t.Fatal("pe-wsl1-nix-home-manager example must set pe: true")
	}
	if opts.WSL == nil || opts.WSL.Image != "nix" {
		t.Fatal("pe-wsl1-nix-home-manager example must request the nix distro")
	}
	if opts.WSL.NixHome == "" || opts.WSL.ServicesDir == "" {
		t.Fatal("pe-wsl1-nix-home-manager example must point at its nixhome and s6 dirs")
	}
	if err := opts.Validate(); err != nil {
		t.Fatalf("pe-wsl1-nix-home-manager opts must validate: %v", err)
	}

	dir := filepath.Join("examples", "pe-wsl1-nix-home-manager")
	if _, err := os.Stat(filepath.Join(dir, opts.WSL.NixHome, "flake.nix")); err != nil {
		t.Fatalf("nixhome must carry a flake.nix: %v", err)
	}
	svcs, err := s6.LoadDir(filepath.Join(dir, opts.WSL.ServicesDir))
	if err != nil {
		t.Fatalf("s6 services must load: %v", err)
	}
	got := map[string]bool{}
	for _, svc := range svcs {
		if err := svc.Validate(); err != nil {
			t.Errorf("service %s: %v", svc.Name, err)
		}
		got[svc.Name] = true
	}
	for _, want := range []string{"xvfb", "dbus-session", "window-manager", "snixembed", "x11vnc"} {
		if !got[want] {
			t.Errorf("example must ship the %s service", want)
		}
	}
}
