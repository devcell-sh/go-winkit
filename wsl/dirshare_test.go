package wsl

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/devcell-sh/go-winkit/gosshd"
	"github.com/devcell-sh/go-winkit/sftpshare"
)

func TestMountScript(t *testing.T) {
	share := sftpshare.DirShare{
		HostPath: "/Users/dmitry/dev/devcell-sh/go-winkit",
		VMPath:   "/Users/dmitry/dev/devcell-sh/go-winkit",
		Drive:    "W",
	}

	script, err := MountScript(share)
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"mkdir -p",
		"/Users/dmitry/dev/devcell-sh",
		"ln -sf",
		"/mnt/w",
		share.VMPath,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script missing %q\nscript:\n%s", want, script)
		}
	}

	// Idempotent: removes existing symlink
	if !strings.Contains(script, "[ -L") {
		t.Errorf("script should check for existing symlink\nscript:\n%s", script)
	}
}

func TestMountScriptDriveLowercase(t *testing.T) {
	share := sftpshare.DirShare{
		VMPath: "/project",
		Drive:  "X",
	}
	script, err := MountScript(share)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(script, "/mnt/x") {
		t.Errorf("drive letter should be lowercased in mount path\nscript:\n%s", script)
	}
}

func TestMountScriptValidation(t *testing.T) {
	if _, err := MountScript(sftpshare.DirShare{Drive: "W"}); err == nil {
		t.Error("expected error for empty VMPath")
	}
	if _, err := MountScript(sftpshare.DirShare{VMPath: "/x"}); err == nil {
		t.Error("expected error for empty Drive")
	}
}

func TestMountScriptMulti(t *testing.T) {
	shares := []sftpshare.DirShare{
		{VMPath: "/Users/dmitry/dev/project-a", Drive: "W", Port: 9844},
		{VMPath: "/Users/dmitry/dev/project-b", Drive: "X", Port: 9845},
	}

	script, err := MountScriptMulti(shares)
	if err != nil {
		t.Fatal(err)
	}

	// Single shebang
	if strings.Count(script, "#!/bin/sh") != 1 {
		t.Errorf("multi script should have exactly one shebang\nscript:\n%s", script)
	}

	for _, s := range shares {
		if !strings.Contains(script, s.VMPath) {
			t.Errorf("multi script missing VMPath %q\nscript:\n%s", s.VMPath, script)
		}
	}

	if !strings.Contains(script, "/mnt/w") || !strings.Contains(script, "/mnt/x") {
		t.Errorf("multi script missing drive mounts\nscript:\n%s", script)
	}
}

func TestMountScriptE2E(t *testing.T) {
	if os.Getenv("WINKIT_E2E") != "1" {
		t.Skip("set WINKIT_E2E=1 (requires running VM with NixDev + SFTP share)")
	}

	addr := os.Getenv("WINKIT_E2E_SSH")
	if addr == "" {
		addr = "127.0.0.1:20122"
	}
	user := os.Getenv("WINKIT_E2E_USER")
	if user == "" {
		user = "dmitry"
	}
	pass := os.Getenv("WINKIT_E2E_PASS")
	if pass == "" {
		pass = "rdp"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	client, err := gosshd.DialWith(ctx, addr, user, pass)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	defer client.Close()

	share := sftpshare.DirShare{
		HostPath: "/Users/dmitry/dev/devcell-sh/go-winkit",
		VMPath:   "/Users/dmitry/dev/devcell-sh/go-winkit",
		Drive:    "W",
	}

	script, err := MountScript(share)
	if err != nil {
		t.Fatal(err)
	}

	b64 := base64.StdEncoding.EncodeToString([]byte(script))
	cmd := fmt.Sprintf(`wsl -d NixDev -u root -e /bin/sh -c "echo %s | base64 -d | /bin/sh"`, b64)
	stdout, stderr, exit, err := client.Run(ctx, cmd)
	if err != nil || exit != 0 {
		t.Fatalf("mount script failed: exit=%d stdout=%s stderr=%s err=%v", exit, stdout, stderr, err)
	}

	// Verify: readlink + ls via base64-encoded script (avoids PowerShell quoting)
	verifyScript := fmt.Sprintf("#!/bin/sh\nreadlink %s\nls %s | head -3\n", share.VMPath, share.VMPath)
	b64v := base64.StdEncoding.EncodeToString([]byte(verifyScript))
	verify := fmt.Sprintf(`wsl -d NixDev -e /bin/sh -c "echo %s | base64 -d | /bin/sh"`, b64v)
	stdout, stderr, exit, err = client.Run(ctx, verify)
	if err != nil || exit != 0 {
		t.Fatalf("verify failed: exit=%d stdout=%s stderr=%s err=%v", exit, stdout, stderr, err)
	}

	out := string(stdout)
	if !strings.Contains(out, "/mnt/w") {
		t.Errorf("symlink target should be /mnt/w, got: %s", out)
	}
	t.Logf("symlink verified:\n%s", out)

	// Data sharing: write a file from WSL1 side, verify it lands host-side
	// (through the SFTP share), then read a host-written file from WSL1.
	marker := ".dirshare-e2e-marker"
	writeScript := fmt.Sprintf("#!/bin/sh\necho dirshare-ok > %s/%s\n", share.VMPath, marker)
	b64w := base64.StdEncoding.EncodeToString([]byte(writeScript))
	writeCmd := fmt.Sprintf(`wsl -d NixDev -e /bin/sh -c "echo %s | base64 -d | /bin/sh"`, b64w)
	if _, stderr, exit, err = client.Run(ctx, writeCmd); err != nil || exit != 0 {
		t.Fatalf("guest write failed: exit=%d stderr=%s err=%v", exit, stderr, err)
	}

	// The SFTP share root maps to the host's shared dir. Check if the
	// marker file appeared there (via the SFTP vfs-cache writeback).
	sharedDir := os.Getenv("WINKIT_E2E_SHARED_DIR")
	if sharedDir == "" {
		sharedDir = os.Getenv("WINKIT_SHARED_DIR")
	}
	if sharedDir != "" {
		markerPath := fmt.Sprintf("%s/%s", sharedDir, marker)
		deadline := time.Now().Add(15 * time.Second)
		for {
			if data, err := os.ReadFile(markerPath); err == nil {
				if !strings.Contains(string(data), "dirshare-ok") {
					t.Errorf("marker content unexpected: %s", data)
				}
				t.Logf("host saw guest write: %s", strings.TrimSpace(string(data)))
				os.Remove(markerPath)
				break
			}
			if time.Now().After(deadline) {
				t.Error("guest write did not land host-side within 15s")
				break
			}
			time.Sleep(2 * time.Second)
		}
	} else {
		t.Log("WINKIT_E2E_SHARED_DIR not set, skipping host-side write verification")
	}

	// Read: verify guest can read files through the symlinked path
	readScript := fmt.Sprintf("#!/bin/sh\ncat %s/probe.txt 2>/dev/null || echo NO_PROBE\n", share.VMPath)
	b64r := base64.StdEncoding.EncodeToString([]byte(readScript))
	readCmd := fmt.Sprintf(`wsl -d NixDev -e /bin/sh -c "echo %s | base64 -d | /bin/sh"`, b64r)
	stdout, _, _, _ = client.Run(ctx, readCmd)
	t.Logf("guest read through symlink: %s", strings.TrimSpace(string(stdout)))
}

func TestDirShareServerConfig(t *testing.T) {
	share := sftpshare.DirShare{
		HostPath: "/Users/dmitry/dev/project",
		VMPath:   "/Users/dmitry/dev/project",
		Drive:    "W",
		Port:     9844,
	}
	cfg := share.ServerConfig(nil)
	if cfg.Root != share.HostPath {
		t.Errorf("ServerConfig.Root = %q, want %q", cfg.Root, share.HostPath)
	}
	if cfg.Port != 9844 {
		t.Errorf("ServerConfig.Port = %d, want 9844", cfg.Port)
	}

	// Zero port falls back to DefaultPort
	share.Port = 0
	cfg = share.ServerConfig(nil)
	if cfg.Port != sftpshare.DefaultPort {
		t.Errorf("ServerConfig.Port with zero = %d, want %d", cfg.Port, sftpshare.DefaultPort)
	}
}

func TestParentDir(t *testing.T) {
	for path, want := range map[string]string{
		"/Users/dmitry/dev/project": "/Users/dmitry/dev",
		"/project":                  "/",
		"/a/b/c/d":                  "/a/b/c",
	} {
		if got := parentDir(path); got != want {
			t.Errorf("parentDir(%q) = %q, want %q", path, got, want)
		}
	}
}
