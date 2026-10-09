package wsl

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCachePathVariesWithRecipe(t *testing.T) {
	na, _ := NixRecipe("alice", "winkit", "")
	nb, _ := NixRecipe("bob", "winkit", "")
	a := cachePath("/c", na)
	b := cachePath("/c", nb)
	if a == b {
		t.Fatal("cache path must vary with the distro user")
	}
	nref, _ := NixRecipe("alice", "winkit", "github:owner/repo")
	if c := cachePath("/c", nref); c == a {
		t.Fatal("cache path must vary with the nixhome source")
	}
	alpRecipe, err := BaseRecipe("alpine", "alice", "winkit")
	if err != nil {
		t.Fatal(err)
	}
	if alp := cachePath("/c", alpRecipe); alp == a {
		t.Fatal("cache path must vary with the image")
	}
	if !strings.HasPrefix(filepath.Base(a), "wsl1-nix-") || !strings.HasSuffix(a, ".wsl") {
		t.Fatalf("unexpected cache path shape: %s", a)
	}
}

func TestBuildTarballCacheHit(t *testing.T) {
	// A pre-seeded cache file + marker must satisfy BuildTarball without
	// docker ever running.
	dir := t.TempDir()
	r, err := NixRecipe("alice", "winkit", "")
	if err != nil {
		t.Fatal(err)
	}
	dest := cachePath(dir, r)
	if err := os.WriteFile(dest, []byte("fake tarball"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest+".done", nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := BuildTarball(context.Background(), dir, r, false, t.Logf)
	if err != nil {
		t.Fatalf("BuildTarball with warm cache: %v", err)
	}
	if got != dest {
		t.Fatalf("got %s, want cached %s", got, dest)
	}
}

// TestBuildTarballWithDocker exercises the real build. Docker + network
// gated; several minutes cold.
func TestBuildTarballWithDocker(t *testing.T) {
	if os.Getenv("WINKIT_E2E") != "1" {
		t.Skip("set WINKIT_E2E=1 (docker build, ~5 min cold)")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not on PATH")
	}
	r, err := NixRecipe("testdev", "winkit", "")
	if err != nil {
		t.Fatal(err)
	}
	path, err := BuildTarball(context.Background(), t.TempDir(), r, false, t.Logf)
	if err != nil {
		t.Fatalf("BuildTarball: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Size() < 50<<20 {
		t.Fatalf("tarball suspiciously small or missing: %v %d", err, fi.Size())
	}
	assertTarballContents(t, path, "testdev")
}

// assertTarballContents validates the produced rootfs carries the WSL and
// Nix artifacts the guest import depends on.
func assertTarballContents(t *testing.T, path, wslUser string) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer gr.Close()

	want := map[string]bool{
		"etc/wsl.conf":              false,
		"etc/wsl-distribution.conf": false,
		"etc/wsl-oobe.sh":           false,
		"etc/fstab":                 false,
		"etc/nix/nix.conf":          false,
		"etc/profile.d/nix.sh":      false,
		"home/" + wslUser + "/":     false,
		// Home-manager activation artifacts: the generated .bashrc and the
		// flake shipped for in-distro iteration.
		"home/" + wslUser + "/.bashrc": false,
		"etc/nixhome/flake.nix":        false,
	}
	hasNixStore := false
	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		name := strings.TrimPrefix(strings.TrimPrefix(hdr.Name, "./"), "/")
		if _, ok := want[name]; ok {
			want[name] = true
		}
		if strings.HasPrefix(name, "nix/") {
			hasNixStore = true
		}
	}
	for p, found := range want {
		if !found {
			t.Errorf("tarball missing %s", p)
		}
	}
	if !hasNixStore {
		t.Error("tarball has no /nix store")
	}
}
