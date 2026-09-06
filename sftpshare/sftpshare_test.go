package sftpshare

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// startTestServer serves root on an ephemeral port and returns the server.
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

// dialTestClient connects an sftp client with the server's default creds.
func dialTestClient(t *testing.T, srv *Server) *sftp.Client {
	t.Helper()
	conn, err := ssh.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", srv.Port()), &ssh.ClientConfig{
		User:            DefaultUser,
		Auth:            []ssh.AuthMethod{ssh.Password(DefaultPassword)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	})
	if err != nil {
		t.Fatalf("ssh dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	client, err := sftp.NewClient(conn)
	if err != nil {
		t.Fatalf("sftp client: %v", err)
	}
	t.Cleanup(func() { client.Close() })
	return client
}

func TestReadsExistingFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "hello.txt"), []byte("from host"), 0o644); err != nil {
		t.Fatal(err)
	}
	client := dialTestClient(t, startTestServer(t, root))

	f, err := client.Open("/hello.txt")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "from host" {
		t.Fatalf("read = %q, want %q", data, "from host")
	}
}

func TestWriteLandsOnHost(t *testing.T) {
	root := t.TempDir()
	client := dialTestClient(t, startTestServer(t, root))

	f, err := client.Create("/guest.txt")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := f.Write([]byte("from guest")); err != nil {
		t.Fatalf("write: %v", err)
	}
	f.Close()

	data, err := os.ReadFile(filepath.Join(root, "guest.txt"))
	if err != nil {
		t.Fatalf("file not written to host: %v", err)
	}
	if string(data) != "from guest" {
		t.Fatalf("host file = %q, want %q", data, "from guest")
	}
}

func TestListStatMkdirRenameRemove(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("a"), 0o644)
	client := dialTestClient(t, startTestServer(t, root))

	// List root.
	entries, err := client.ReadDir("/")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "a.txt" {
		t.Fatalf("ReadDir = %v, want [a.txt]", entries)
	}

	// Stat.
	fi, err := client.Stat("/a.txt")
	if err != nil || fi.Size() != 1 {
		t.Fatalf("Stat: fi=%v err=%v", fi, err)
	}

	// Mkdir + list inside.
	if err := client.Mkdir("/sub"); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if fi, err := os.Stat(filepath.Join(root, "sub")); err != nil || !fi.IsDir() {
		t.Fatalf("host dir not created: %v", err)
	}

	// Rename (rclone uploads to a temp name, then renames).
	if err := client.Rename("/a.txt", "/sub/b.txt"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "sub", "b.txt")); err != nil {
		t.Fatalf("renamed file missing on host: %v", err)
	}

	// Remove file, then dir.
	if err := client.Remove("/sub/b.txt"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := client.RemoveDirectory("/sub"); err != nil {
		t.Fatalf("RemoveDirectory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "sub")); !os.IsNotExist(err) {
		t.Fatalf("host dir still present: %v", err)
	}
}

func TestSetModTime(t *testing.T) {
	// rclone sets the mtime after every upload; without Setstat support the
	// backend logs errors and falls back to size-only change detection.
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "t.txt"), []byte("x"), 0o644)
	client := dialTestClient(t, startTestServer(t, root))

	fi, err := client.Stat("/t.txt")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	want := fi.ModTime().Add(-3600e9).Truncate(1e9)
	if err := client.Chtimes("/t.txt", want, want); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	hostFi, _ := os.Stat(filepath.Join(root, "t.txt"))
	if !hostFi.ModTime().Truncate(1e9).Equal(want) {
		t.Fatalf("mtime = %v, want %v", hostFi.ModTime(), want)
	}
}

func TestPathEscapeRejected(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(filepath.Dir(root), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(outside)
	client := dialTestClient(t, startTestServer(t, root))

	for _, path := range []string{"/../secret.txt", "../secret.txt", "/sub/../../secret.txt"} {
		f, err := client.Open(path)
		if err != nil {
			continue // rejected: good
		}
		data, _ := io.ReadAll(f)
		f.Close()
		if strings.Contains(string(data), "secret") {
			t.Errorf("path %q escaped the share root", path)
		}
	}
}

func TestSymlinkEscapeRejected(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(filepath.Dir(root), "sym-secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(outside)
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	client := dialTestClient(t, startTestServer(t, root))

	f, err := client.Open("/link")
	if err != nil {
		return // rejected: good
	}
	data, _ := io.ReadAll(f)
	f.Close()
	if strings.Contains(string(data), "secret") {
		t.Error("symlink escaped the share root")
	}
}

func TestBadPasswordRejected(t *testing.T) {
	srv := startTestServer(t, t.TempDir())
	_, err := ssh.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", srv.Port()), &ssh.ClientConfig{
		User:            DefaultUser,
		Auth:            []ssh.AuthMethod{ssh.Password("wrong")},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	})
	if err == nil {
		t.Fatal("dial with wrong password should fail")
	}
}

func TestEphemeralPortReported(t *testing.T) {
	srv := startTestServer(t, t.TempDir())
	if srv.Port() == 0 {
		t.Fatal("Port() = 0 after Start on ephemeral port")
	}
}

func TestFixedPort(t *testing.T) {
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
	addr := fmt.Sprintf("127.0.0.1:%d", srv.Port())
	if err := srv.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := ssh.Dial("tcp", addr, &ssh.ClientConfig{
		User:            DefaultUser,
		Auth:            []ssh.AuthMethod{ssh.Password(DefaultPassword)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}); err == nil {
		t.Fatal("server still accepting after Close")
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
