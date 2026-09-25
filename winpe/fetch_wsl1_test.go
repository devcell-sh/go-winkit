package winpe

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFetchWSL1EngineUsesCompleteCache(t *testing.T) {
	cacheDir := t.TempDir()
	engineDir := filepath.Join(cacheDir, "tools", "wsl-"+WSL1PackageVersion, "extracted", "PFiles64", "WSL")
	for _, name := range WSL1EngineFiles() {
		path := filepath.Join(engineDir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("payload"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := FetchWSL1Engine(context.Background(), cacheDir, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != engineDir {
		t.Fatalf("engine dir = %q, want %q", got, engineDir)
	}
}
