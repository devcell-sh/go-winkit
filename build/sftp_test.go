package build

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/devcell-sh/go-winkit/sftpshare"
)

func TestWSLSharedDirEnv(t *testing.T) {
	// unset defaults to the current directory
	wd0, _ := os.Getwd()
	t.Setenv("WINKIT_SHARED_DIR", "")
	t.Setenv("WINKIT_E2E_SHARED_DIR", "")
	if dir, err := wslSharedDir(); err != nil || dir != wd0 {
		t.Fatalf("unset env: got (%q, %v), want cwd %q", dir, err, wd0)
	}

	// "none" disables sharing
	t.Setenv("WINKIT_SHARED_DIR", "none")
	if dir, err := wslSharedDir(); err != nil || dir != "" {
		t.Fatalf("none: got (%q, %v), want empty", dir, err)
	}
	t.Setenv("WINKIT_SHARED_DIR", "")

	abs := t.TempDir()
	t.Setenv("WINKIT_SHARED_DIR", abs)
	if dir, err := wslSharedDir(); err != nil || dir != abs {
		t.Fatalf("WINKIT_SHARED_DIR: got (%q, %v), want %q", dir, err, abs)
	}

	// e2e alias honored when the primary is unset
	t.Setenv("WINKIT_SHARED_DIR", "")
	t.Setenv("WINKIT_E2E_SHARED_DIR", abs)
	if dir, err := wslSharedDir(); err != nil || dir != abs {
		t.Fatalf("WINKIT_E2E_SHARED_DIR: got (%q, %v), want %q", dir, err, abs)
	}

	// relative resolves against cwd
	wd, _ := os.Getwd()
	t.Setenv("WINKIT_SHARED_DIR", ".")
	if dir, err := wslSharedDir(); err != nil || dir != wd {
		t.Fatalf("relative dir: got (%q, %v), want %q", dir, err, wd)
	}
}

func TestSFTPPortEnv(t *testing.T) {
	t.Setenv("WINKIT_SFTP_PORT", "")
	if p, err := sftpPort(); err != nil || p != sftpshare.DefaultPort {
		t.Fatalf("default: got (%d, %v), want %d", p, err, sftpshare.DefaultPort)
	}
	t.Setenv("WINKIT_SFTP_PORT", "12345")
	if p, err := sftpPort(); err != nil || p != 12345 {
		t.Fatalf("override: got (%d, %v), want 12345", p, err)
	}
	t.Setenv("WINKIT_SFTP_PORT", "nope")
	if _, err := sftpPort(); err == nil {
		t.Fatal("invalid port should error")
	}
}

func TestStartSFTPShareServes(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "probe.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WINKIT_SFTP_PORT", "0") // ephemeral for the test

	srv, err := startSFTPShare(root, slog.Default())
	if err != nil {
		t.Fatalf("startSFTPShare: %v", err)
	}
	defer srv.Close()

	conn, err := ssh.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", srv.Port()), &ssh.ClientConfig{
		User:            sftpshare.DefaultUser,
		Auth:            []ssh.AuthMethod{ssh.Password(sftpshare.DefaultPassword)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	})
	if err != nil {
		t.Fatalf("ssh dial: %v", err)
	}
	defer conn.Close()
	client, err := sftp.NewClient(conn)
	if err != nil {
		t.Fatalf("sftp client: %v", err)
	}
	defer client.Close()
	fi, err := client.Stat("/probe.txt")
	if err != nil || fi.Size() != 2 {
		t.Fatalf("Stat: fi=%v err=%v", fi, err)
	}
}

func TestStartSFTPShareDefaultPort(t *testing.T) {
	t.Setenv("WINKIT_SFTP_PORT", "")
	srv, err := startSFTPShare(t.TempDir(), slog.Default())
	if err != nil {
		t.Skipf("default port %d busy: %v", sftpshare.DefaultPort, err)
	}
	defer srv.Close()
	if srv.Port() != sftpshare.DefaultPort {
		t.Fatalf("Port() = %d, want default %d", srv.Port(), sftpshare.DefaultPort)
	}
}
