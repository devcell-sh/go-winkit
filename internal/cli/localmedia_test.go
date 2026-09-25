package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveLocalMedia_ISO(t *testing.T) {
	dir := t.TempDir()
	iso := filepath.Join(dir, "win.iso")
	if err := os.WriteFile(iso, []byte("iso"), 0o644); err != nil {
		t.Fatal(err)
	}

	path, ok, err := resolveLocalMedia(iso)
	if err != nil {
		t.Fatalf("existing ISO should resolve: %v", err)
	}
	if !ok {
		t.Fatal("existing ISO should be recognized as local media")
	}
	if !filepath.IsAbs(path) {
		t.Fatalf("resolved path should be absolute, got %q", path)
	}
}

func TestResolveLocalMedia_MissingISO(t *testing.T) {
	_, _, err := resolveLocalMedia(filepath.Join(t.TempDir(), "gone.iso"))
	if err == nil {
		t.Fatal("missing ISO should error")
	}
}

func TestResolveLocalMedia_WIMNotSupported(t *testing.T) {
	dir := t.TempDir()
	wim := filepath.Join(dir, "install.wim")
	if err := os.WriteFile(wim, []byte("wim"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := resolveLocalMedia(wim)
	if err == nil {
		t.Fatal("WIM source should error until supported")
	}
	if !strings.Contains(err.Error(), "iso") && !strings.Contains(err.Error(), "ISO") {
		t.Fatalf("WIM error should point at ISO alternative, got: %v", err)
	}
}

func TestResolveLocalMedia_CatalogSpecPassesThrough(t *testing.T) {
	path, ok, err := resolveLocalMedia("windows/11-pro-arm64")
	if err != nil {
		t.Fatalf("catalog spec should not error: %v", err)
	}
	if ok || path != "" {
		t.Fatalf("catalog spec is not local media, got ok=%v path=%q", ok, path)
	}
}
