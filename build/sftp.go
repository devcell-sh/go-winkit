package build

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/devcell-sh/go-winkit/sftpshare"
	"github.com/devcell-sh/go-winkit/unattend"
)

// wslSharedDir resolves the host directory to share with the guest over
// SFTP. WINKIT_SHARED_DIR wins; WINKIT_E2E_SHARED_DIR is the e2e alias.
// Unset defaults to the current directory (WINKIT_SHARED_DIR=none disables
// sharing).
func wslSharedDir() (string, error) {
	dir := os.Getenv("WINKIT_SHARED_DIR")
	if dir == "" {
		dir = os.Getenv("WINKIT_E2E_SHARED_DIR")
	}
	if dir == "none" {
		return "", nil
	}
	if dir == "" {
		dir = "."
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolving shared dir %q: %w", dir, err)
	}
	return abs, nil
}

// resolveNixHome picks the home-manager configuration source for the NixDev
// rootfs: the --nixhome flag wins, then WINKIT_NIXHOME; empty means the
// embedded default flake (wsl.DefaultFlakeNix). Values are either a local
// directory or a remote flake ref — wsl.NixRecipe tells them apart.
func ResolveNixHome(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	return os.Getenv("WINKIT_NIXHOME")
}

// sftpPort resolves the port for the SFTP share. WINKIT_SFTP_PORT overrides
// the default (9844). 0 selects an ephemeral port. The port is fixed by
// default because it is rendered into the guest's boot-time mount task:
// an ephemeral port would strand every mount after the first build.
func sftpPort() (int, error) {
	port := sftpshare.DefaultPort
	if s := os.Getenv("WINKIT_SFTP_PORT"); s != "" {
		p, err := strconv.Atoi(s)
		if err != nil || p < 0 || p > 65535 {
			return 0, fmt.Errorf("invalid WINKIT_SFTP_PORT %q", s)
		}
		port = p
	}
	return port, nil
}

// startSFTPShare serves dir on the host loopback for the guest's rclone
// mount. Port defaults to 9844 (WINKIT_SFTP_PORT overrides).
func startSFTPShare(dir string, logger *slog.Logger) (*sftpshare.Server, error) {
	port, err := sftpPort()
	if err != nil {
		return nil, err
	}
	srv, err := sftpshare.New(sftpshare.Config{Root: dir, Port: port, Logger: logger})
	if err != nil {
		return nil, err
	}
	if err := srv.Start(); err != nil {
		return nil, err
	}
	return srv, nil
}

// wslVerifySFTP verifies the guest's fixed-disk mount end to end against an
// already-running SFTP share (owned and closed by the caller, so the server
// outlives the verify and spans the whole VM session): the guest re-runs the
// rclone mount task (registered at first logon by the bootstrap), reads a
// host-written probe through W:, and writes a marker back that must land
// host-side.
func wslVerifySFTP(ctx context.Context, addr, user, pass string, srv *sftpshare.Server, sharedDir string, logger *slog.Logger) error {
	// Host-local sanity: the listener accepts.
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", srv.Port()), 5*time.Second)
	if err != nil {
		return fmt.Errorf("sftp share not accepting on port %d: %w", srv.Port(), err)
	}
	conn.Close()
	logger.Info("sftp verify: host listener OK", "port", srv.Port())

	probePath := filepath.Join(sharedDir, "probe.txt")
	if err := os.WriteFile(probePath, []byte("sftp-probe-ok"), 0o644); err != nil {
		return fmt.Errorf("writing probe file: %w", err)
	}
	defer os.Remove(probePath)

	// Re-run the mount task: idempotent (a second rclone on a busy drive
	// letter exits; the running one keeps serving), and it revives the mount
	// when the host server restarted since boot.
	if _, stderr, exit, err := sshRun(ctx, addr, user, pass,
		"schtasks /run /tn "+unattend.RcloneMountTaskName); err != nil || exit != 0 {
		logger.Warn("sftp verify: mount task re-run failed (base built before SFTP sharing?)",
			"exit", exit, "stderr", strings.TrimSpace(string(stderr)), "err", err)
	}

	// The guest reads the probe through the mounted drive. The mount comes
	// up asynchronously after the task starts; retry briefly.
	const drive = "W" // matches the unattend bootstrap default
	var lastOut, lastErr string
	deadline := time.Now().Add(90 * time.Second)
	for {
		stdout, stderr, exit, err := sshRun(ctx, addr, user, pass,
			fmt.Sprintf(`Get-Content %s:\probe.txt`, drive))
		if err == nil && exit == 0 && strings.Contains(string(stdout), "sftp-probe-ok") {
			break
		}
		lastOut, lastErr = strings.TrimSpace(string(stdout)), strings.TrimSpace(string(stderr))
		if time.Now().After(deadline) {
			return fmt.Errorf("guest cannot read probe through %s: (last: out=%q err=%q)", drive, lastOut, lastErr)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
	logger.Info("sftp verify: guest read through mounted drive OK", "drive", drive+":")

	// The editor-save path: open an EXISTING host-created file for write.
	// A SYSTEM-run WinFsp mount denies GENERIC_WRITE to every other user
	// unless the volume ships the FileSecurity descriptor (run
	// 20260903T181714) — and creating new files does NOT exercise that, so
	// this append is the only regression check for it. Run this verify as a
	// non-SYSTEM user for it to mean anything.
	if _, stderr, exit, err := sshRun(ctx, addr, user, pass,
		fmt.Sprintf(`$ErrorActionPreference='Stop'; Add-Content -Path %s:\probe.txt -Value 'guest-edit-ok'`, drive)); err != nil || exit != 0 {
		return fmt.Errorf("editing an existing file through %s: failed (FileSecurity regression?): exit=%d stderr=%s err=%v", drive, exit, strings.TrimSpace(string(stderr)), err)
	}
	deadline = time.Now().Add(30 * time.Second)
	for {
		if data, err := os.ReadFile(probePath); err == nil && strings.Contains(string(data), "guest-edit-ok") {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("guest edit of an existing file did not land host-side within 30s (%s)", probePath)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	logger.Info("sftp verify: guest edit of an existing file landed host-side (editor-save path OK)")

	// Round trip: guest writes, host must see it (vfs write-back is ~5s).
	marker := ".winkit-sftp-verify"
	if _, stderr, exit, err := sshRun(ctx, addr, user, pass,
		fmt.Sprintf(`Set-Content -Path %s:\%s -Value 'guest-write-ok'`, drive, marker)); err != nil || exit != 0 {
		return fmt.Errorf("guest write through %s: failed: exit=%d stderr=%s err=%w", drive, exit, stderr, err)
	}
	markerPath := filepath.Join(sharedDir, marker)
	defer os.Remove(markerPath)
	deadline = time.Now().Add(30 * time.Second)
	for {
		if _, err := os.Stat(markerPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("guest write did not land host-side within 30s (%s)", markerPath)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	logger.Info("sftp verify: guest write landed host-side", "marker", markerPath)
	return nil
}
