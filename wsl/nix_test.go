package wsl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsFlakeRef(t *testing.T) {
	for ref, want := range map[string]bool{
		"github:dimmkirr/nixhome":         true,
		"git+https://example.com/x.git":   true,
		"https://example.com/tarball.tgz": true,
		"flake:nixhome":                   true,
		"/Users/dmitry/nixhome":           false,
		"./nixhome":                       false,
		"":                                false,
	} {
		if got := IsFlakeRef(ref); got != want {
			t.Errorf("IsFlakeRef(%q) = %v, want %v", ref, got, want)
		}
	}
}

// The embedded flake must expose homeConfigurations.<user> — the Dockerfile
// activation resolves exactly that attribute — and the module must own the
// shell UX that used to be imperative .bashrc appends.
func TestDefaultNixHomeShape(t *testing.T) {
	for _, want := range []string{`homeConfigurations."@USER@"`, "home-manager", "./home.nix"} {
		if !strings.Contains(DefaultFlakeNix, want) {
			t.Errorf("flake.nix missing %q", want)
		}
	}
	for _, want := range []string{`home.username = "@USER@"`, "programs.bash", "pkgs.bash", "enableCompletion", "initExtra", "@DISTRO@", "profile.d/nix.sh", "openssh", "coreutils"} {
		if !strings.Contains(DefaultHomeNix, want) {
			t.Errorf("home.nix missing %q", want)
		}
	}
}

// NixRecipe context files vary per nixhome source: defaults render the
// embedded flake, local dirs load their content (so the cache key tracks
// it), refs pass through as a build arg.
func TestNixRecipeNixHomeVariants(t *testing.T) {
	def, err := NixRecipe("dev", "winkit", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := def.ContextFiles["nixhome/flake.nix"]; !ok {
		t.Error("default recipe must carry the embedded flake")
	}
	if !strings.Contains(string(def.ContextFiles["nixhome/flake.nix"]), `"dev"`) {
		t.Error("embedded flake must be rendered with the user")
	}

	ref, err := NixRecipe("dev", "winkit", "github:owner/repo")
	if err != nil {
		t.Fatal(err)
	}
	if ref.BuildArgs["NIXHOME_REF"] != "github:owner/repo" {
		t.Error("flake ref must pass through as a build arg")
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "flake.nix"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	local, err := NixRecipe("dev", "winkit", dir)
	if err != nil {
		t.Fatal(err)
	}
	if string(local.ContextFiles["nixhome/flake.nix"]) != "x" {
		t.Error("local nixhome files must be loaded into the context")
	}

	if _, err := NixRecipe("dev", "winkit", filepath.Join(dir, "missing")); err == nil {
		t.Error("a missing local nixhome dir must error, not silently fall back")
	}

	// Each variant must produce a distinct cache key.
	a, b, c := cachePath("/c", def), cachePath("/c", ref), cachePath("/c", local)
	if a == b || a == c || b == c {
		t.Fatalf("cache keys must differ: def=%q ref=%q local=%q", a, b, c)
	}
}
