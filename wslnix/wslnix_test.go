package wslnix

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

func TestCachePathVariesWithUserAndDockerfile(t *testing.T) {
	a := cachePath("/c", "alice", "fp")
	b := cachePath("/c", "bob", "fp")
	if a == b {
		t.Fatal("cache path must vary with the distro user")
	}
	if c := cachePath("/c", "alice", "other-fp"); c == a {
		t.Fatal("cache path must vary with the nixhome fingerprint")
	}
	if !strings.HasPrefix(filepath.Base(a), "nix-wsl1-") || !strings.HasSuffix(a, ".wsl") {
		t.Fatalf("unexpected cache path shape: %s", a)
	}
}

func TestDockerfileCarriesWSLEssentials(t *testing.T) {
	// The pieces that cost real debugging time when missing (CELL-532):
	// fstab (WSL1 runs mount -a at boot), util-linux (/bin/mount for the
	// drvfs automount of C:), and the user build-arg.
	for _, want := range []string{
		"ARG WSL_USER",
		"touch /etc/fstab",
		"util-linux",
		"/bin/mount",
		"/bin/umount",
		"/bin/bash",
		"/etc/wsl.conf",
		"touch /etc/profile",
		"COPY nixhome /etc/nixhome",
		"ARG NIXHOME_REF",
		"homeConfigurations.${WSL_USER}.activationPackage",
	} {
		if !strings.Contains(Dockerfile, want) {
			t.Errorf("Dockerfile missing %q", want)
		}
	}
}

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

// nixhome fingerprints: defaults are stable, local dirs hash content, refs
// hash the ref string — each variant must produce a distinct cache key.
func TestNixHomeFingerprintVariants(t *testing.T) {
	def, err := nixHomeFingerprint("")
	if err != nil {
		t.Fatal(err)
	}
	ref, _ := nixHomeFingerprint("github:owner/repo")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "flake.nix"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	local, err := nixHomeFingerprint(dir)
	if err != nil {
		t.Fatal(err)
	}
	if def == ref || def == local || ref == local {
		t.Fatalf("fingerprints must differ: def=%q ref=%q local=%q", def, ref, local)
	}
	if _, err := nixHomeFingerprint(filepath.Join(dir, "missing")); err == nil {
		t.Error("a missing local nixhome dir must error, not silently fall back")
	}
	local2, _ := nixHomeFingerprint(dir)
	if local != local2 {
		t.Error("local dir fingerprint must be deterministic")
	}
}

func TestBuildTarballCacheHit(t *testing.T) {
	// A pre-seeded cache file + marker must satisfy BuildTarball without
	// docker ever running.
	dir := t.TempDir()
	fp, err := nixHomeFingerprint("")
	if err != nil {
		t.Fatal(err)
	}
	dest := cachePath(dir, "alice", fp)
	if err := os.WriteFile(dest, []byte("fake tarball"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest+".done", nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := BuildTarball(context.Background(), dir, "alice", "", "", false, t.Logf)
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
	path, err := BuildTarball(context.Background(), t.TempDir(), "testdev", "", "", false, t.Logf)
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
