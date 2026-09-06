package webdavshare

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// startTestServer serves root on an ephemeral port and returns the base URL.
func startTestServer(t *testing.T, root string) *Server {
	t.Helper()
	srv, err := New(Config{Root: root, Port: 0})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { srv.Close() })
	return srv
}

func TestServesExistingFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "hello.txt"), []byte("from host"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := startTestServer(t, root)

	resp, err := http.Get(srv.URL() + "/hello.txt")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "from host" {
		t.Fatalf("body = %q, want %q", body, "from host")
	}
}

func TestPutWritesBackToHost(t *testing.T) {
	root := t.TempDir()
	srv := startTestServer(t, root)

	req, _ := http.NewRequest(http.MethodPut, srv.URL()+"/guest.txt", strings.NewReader("from guest"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		t.Fatalf("PUT status = %d, want 201/204", resp.StatusCode)
	}
	data, err := os.ReadFile(filepath.Join(root, "guest.txt"))
	if err != nil {
		t.Fatalf("file not written to host: %v", err)
	}
	if string(data) != "from guest" {
		t.Fatalf("host file = %q, want %q", data, "from guest")
	}
}

func TestPropfindListsDirectory(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "sub"), 0o755)
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(root, "sub", "b.txt"), []byte("b"), 0o644)
	srv := startTestServer(t, root)

	req, _ := http.NewRequest("PROPFIND", srv.URL()+"/", nil)
	req.Header.Set("Depth", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PROPFIND: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMultiStatus {
		t.Fatalf("PROPFIND status = %d, want 207", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	for _, want := range []string{"a.txt", "sub"} {
		if !bytes.Contains(body, []byte(want)) {
			t.Errorf("PROPFIND response missing %q", want)
		}
	}
}

func TestMkcolCreatesDirectory(t *testing.T) {
	root := t.TempDir()
	srv := startTestServer(t, root)

	req, _ := http.NewRequest("MKCOL", srv.URL()+"/newdir", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("MKCOL: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("MKCOL status = %d, want 201", resp.StatusCode)
	}
	fi, err := os.Stat(filepath.Join(root, "newdir"))
	if err != nil || !fi.IsDir() {
		t.Fatalf("directory not created on host: %v", err)
	}
}

func TestPathEscapeRejected(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(filepath.Dir(root), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(outside)
	srv := startTestServer(t, root)

	// Raw traversal in the URL path must not reach files outside root.
	for _, path := range []string{"/../secret.txt", "/..%2Fsecret.txt", "/%2e%2e/secret.txt"} {
		req, _ := http.NewRequest(http.MethodGet, srv.URL()+path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK && bytes.Contains(body, []byte("secret")) {
			t.Errorf("path %q escaped the share root", path)
		}
	}
}

func TestEphemeralPortReported(t *testing.T) {
	srv := startTestServer(t, t.TempDir())
	if srv.Port() == 0 {
		t.Fatal("Port() = 0 after Start on ephemeral port")
	}
	if !strings.HasPrefix(srv.URL(), "http://127.0.0.1:") {
		t.Fatalf("URL() = %q, want loopback http URL", srv.URL())
	}
}

func TestFixedPort(t *testing.T) {
	// Find a free port first, then ask for it explicitly.
	probe := startTestServer(t, t.TempDir())
	port := probe.Port()
	probe.Close()

	srv, err := New(Config{Root: t.TempDir(), Port: port})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := srv.Start(); err != nil {
		t.Fatalf("Start on fixed port %d: %v", port, err)
	}
	defer srv.Close()
	if srv.Port() != port {
		t.Fatalf("Port() = %d, want %d", srv.Port(), port)
	}
}

func TestCloseStopsServing(t *testing.T) {
	srv := startTestServer(t, t.TempDir())
	url := srv.URL()
	if err := srv.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := http.Get(url + "/"); err == nil {
		t.Fatal("server still serving after Close")
	}
}

func TestNewRejectsMissingRoot(t *testing.T) {
	if _, err := New(Config{Root: filepath.Join(t.TempDir(), "nope")}); err == nil {
		t.Fatal("New with nonexistent root should fail")
	}
	if _, err := New(Config{Root: ""}); err == nil {
		t.Fatal("New with empty root should fail")
	}
}
