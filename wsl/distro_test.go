package wsl

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDistroFor(t *testing.T) {
	cases := []struct {
		image  string
		docker bool
	}{
		{"", true},
		{"nix", true},
		{"alpine", true},
		{"https://cdimages.ubuntu.com/ubuntu-wsl/noble/daily-live/current/noble-wsl-arm64.wsl", false},
		{"https://dl-cdn.alpinelinux.org/alpine/v3.21/releases/aarch64/alpine-minirootfs-3.21.3-aarch64.tar.gz", false},
		{"./local-rootfs.wsl", false},
		{"/abs/path/rootfs.tar.xz", false},
		{"ubuntu:24.04", true},
		{"ghcr.io/org/img:tag", true},
		{"ubuntu", true},
	}
	for _, c := range cases {
		d, err := DistroFor(c.image, "dev", "winkit", "")
		if err != nil {
			t.Fatalf("DistroFor(%q): %v", c.image, err)
		}
		if d.NeedsDocker() != c.docker {
			t.Errorf("DistroFor(%q).NeedsDocker() = %v, want %v", c.image, d.NeedsDocker(), c.docker)
		}
		if d.VerifyCommand == "" || d.VerifyContains == "" {
			t.Errorf("DistroFor(%q) must carry a verify command", c.image)
		}
	}
	if _, err := DistroFor("not a ref", "dev", "winkit", ""); err == nil {
		t.Error("an image value with spaces must error")
	}
}

// Every non-nix distro resolves through the universal base template;
// known base names get distro-specific verify commands, unknown ones the
// generic Linux check.
func TestDistroForDockerRefs(t *testing.T) {
	alp, err := DistroFor("", "dev", "winkit", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(alp.Recipe.Dockerfile, "FROM alpine") {
		t.Error("default image must build from the base template FROM alpine")
	}
	if alp.VerifyContains != "Alpine" {
		t.Errorf("alpine verify must check os-release, got %q", alp.VerifyContains)
	}

	deb, err := DistroFor("debian:12", "dev", "winkit", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(deb.Recipe.Dockerfile, "FROM debian:12") {
		t.Error("docker ref must build from the base template")
	}
	if deb.VerifyContains != "Debian" {
		t.Errorf("debian verify must check os-release, got %q", deb.VerifyContains)
	}

	custom, err := DistroFor("ghcr.io/org/img:tag", "dev", "winkit", "")
	if err != nil {
		t.Fatal(err)
	}
	if custom.VerifyContains != "Linux" {
		t.Errorf("unknown base must get the generic verify, got %q", custom.VerifyContains)
	}
}

func TestRefBaseName(t *testing.T) {
	for ref, want := range map[string]string{
		"alpine":                   "alpine",
		"alpine:3.21":              "alpine",
		"ubuntu:24.04":             "ubuntu",
		"ghcr.io/org/img:tag":      "img",
		"docker.io/library/debian": "debian",
	} {
		if got := refBaseName(ref); got != want {
			t.Errorf("refBaseName(%q) = %q, want %q", ref, got, want)
		}
	}
}

// An arbitrary docker ref gets the universal plumbing template with its
// FROM line, and a tag/filename-safe slug for the cache.
func TestBaseRecipe(t *testing.T) {
	r, err := BaseRecipe("ghcr.io/org/img:24.04", "dev", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Dockerfile, "FROM ghcr.io/org/img:24.04") {
		t.Error("base Dockerfile must FROM the given ref")
	}
	for _, want := range []string{
		"/etc/wsl.conf", "touch /etc/fstab", "ARG WSL_USER", "login-shell",
		// The Windows bootstrap boot task execs /bin/s6-init on every
		// image, so the entry point must exist even without s6 installed.
		"/bin/s6-init", "s6-svscan",
	} {
		if !strings.Contains(r.Dockerfile, want) {
			t.Errorf("base Dockerfile missing %q", want)
		}
	}
	if r.Image != "ghcr.io-org-img-24.04" {
		t.Errorf("Image must be a slug, got %q", r.Image)
	}
	if r.DistroName != "winkit" {
		t.Errorf("empty distro name must default to winkit, got %q", r.DistroName)
	}
	a := cachePath("/c", r)
	other, _ := BaseRecipe("ubuntu:24.04", "dev", "")
	if b := cachePath("/c", other); a == b {
		t.Error("cache path must vary with the base ref")
	}
}

func TestDistroMaterializeURL(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write([]byte("fake rootfs tarball"))
	}))
	defer srv.Close()

	cacheDir := t.TempDir()
	d, err := DistroFor(srv.URL+"/rootfs.wsl", "dev", "winkit", "")
	if err != nil {
		t.Fatal(err)
	}
	path, err := d.Materialize(context.Background(), cacheDir, false, t.Logf)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "fake rootfs tarball" {
		t.Fatalf("downloaded content mismatch: %q %v", data, err)
	}

	// Second call must hit the cache, not the server.
	if _, err := d.Materialize(context.Background(), cacheDir, false, t.Logf); err != nil {
		t.Fatal(err)
	}
	if hits != 1 {
		t.Errorf("expected 1 fetch, got %d — cache not honored", hits)
	}

	// noCache forces a refetch.
	if _, err := d.Materialize(context.Background(), cacheDir, true, t.Logf); err != nil {
		t.Fatal(err)
	}
	if hits != 2 {
		t.Errorf("expected refetch under noCache, got %d hits", hits)
	}
}

func TestDistroMaterializeURLHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	d, _ := DistroFor(srv.URL+"/missing.wsl", "dev", "winkit", "")
	if _, err := d.Materialize(context.Background(), t.TempDir(), false, t.Logf); err == nil {
		t.Fatal("HTTP 404 must fail materialization, not cache an error page")
	}
}

func TestDistroMaterializeLocalPath(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "rootfs.wsl")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := DistroFor(p, "dev", "winkit", "")
	if err != nil {
		t.Fatal(err)
	}
	got, err := d.Materialize(context.Background(), t.TempDir(), false, t.Logf)
	if err != nil || got != p {
		t.Fatalf("local path must materialize to itself: got %q, %v", got, err)
	}

	d2, _ := DistroFor(filepath.Join(dir, "absent.wsl"), "dev", "winkit", "")
	if _, err := d2.Materialize(context.Background(), t.TempDir(), false, t.Logf); err == nil {
		t.Fatal("missing local tarball must error")
	}
}
