package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/devcell-sh/go-winkit/webdavshare"
)

func startShare(t *testing.T) (root string, url string) {
	t.Helper()
	root = t.TempDir()
	srv, err := webdavshare.New(webdavshare.Config{Root: root, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	return root, srv.URL()
}

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for p, content := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func assertTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for p, want := range files {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		if err != nil {
			t.Errorf("%s: %v", p, err)
			continue
		}
		if string(data) != want {
			t.Errorf("%s = %q, want %q", p, data, want)
		}
	}
}

var tree = map[string]string{
	"top.txt":             "top",
	"src/main.go":         "package main",
	"src/nested/deep.txt": "deep",
	"assets/logo.bin":     "\x00\x01\x02binary",
}

func TestPullTree(t *testing.T) {
	serverRoot, url := startShare(t)
	writeTree(t, serverRoot, tree)

	dest := t.TempDir()
	if err := pullTree(url, dest); err != nil {
		t.Fatalf("pullTree: %v", err)
	}
	assertTree(t, dest, tree)
}

func TestPushTree(t *testing.T) {
	serverRoot, url := startShare(t)

	local := t.TempDir()
	writeTree(t, local, tree)
	if err := pushTree(url, local); err != nil {
		t.Fatalf("pushTree: %v", err)
	}
	assertTree(t, serverRoot, tree)
}

func TestPushThenPullRoundTrip(t *testing.T) {
	_, url := startShare(t)

	local := t.TempDir()
	writeTree(t, local, tree)
	if err := pushTree(url, local); err != nil {
		t.Fatalf("pushTree: %v", err)
	}

	back := t.TempDir()
	if err := pullTree(url, back); err != nil {
		t.Fatalf("pullTree: %v", err)
	}
	assertTree(t, back, tree)
}

func TestPullEmptyServer(t *testing.T) {
	_, url := startShare(t)
	if err := pullTree(url, t.TempDir()); err != nil {
		t.Fatalf("pullTree on empty share: %v", err)
	}
}
