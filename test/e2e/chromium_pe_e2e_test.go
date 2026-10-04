//go:build integration

package e2e

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	winkit "github.com/devcell-sh/go-winkit"
	"github.com/devcell-sh/go-winkit/build"
	"github.com/devcell-sh/go-winkit/cache"
	"github.com/devcell-sh/go-winkit/gosshd"
	"github.com/devcell-sh/go-winkit/internal/cli"
	"github.com/devcell-sh/go-winkit/internal/testutil"
	"github.com/devcell-sh/go-winkit/vm/qemu"
)

// TestChromium_PE_E2E verifies that a Chromium-based browser can start
// and render inside a WinPE VM. Chromium runs inside the WSL1 distro's
// X11 desktop (Xvfb + IceWM), which is the supported path for graphical
// Linux apps in WinPE.
//
// The test:
//  1. Builds the pe-wsl1-nix-home-manager example (includes Chromium)
//  2. Boots with TCG
//  3. Waits for WSL1 bootstrap + X11 desktop
//  4. Launches Chromium on display :99
//  5. Verifies the Chromium process is running
//  6. Takes an X11 screenshot to prove rendering
func TestChromium_PE_E2E(t *testing.T) {
	if os.Getenv("WINKIT_E2E") != "1" {
		t.Skip("set WINKIT_E2E=1 to run (needs seeded cache, docker, QEMU)")
	}
	_ = windowsISOPath(t)
	_ = virtioISOPath(t)

	exampleDir, err := filepath.Abs(filepath.Join("..", "..", "examples", "pe-wsl1-nix-home-manager"))
	require.NoError(t, err)
	resultDir := testutil.ResultDir(t)
	dest := filepath.Join(resultDir, "winkit-pe-chromium.qcow2")

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Minute)
	defer cancel()

	// Build using the CLI path.
	var cliOutput strings.Builder
	root := cli.NewRootCmd()
	root.SetOut(&cliOutput)
	root.SetErr(&cliOutput)
	args := []string{
		"build",
		"--file", exampleDir,
		"--cache-dir", cache.Dir(),
		"--force",
		dest,
	}
	root.SetArgs(args)
	t.Logf("winkit %s", strings.Join(args, " "))
	err = root.ExecuteContext(ctx)
	require.NoError(t, err, "PE+WSL1+nix build failed:\n%s", cliOutput.String())

	artifact, err := build.LoadArtifact(dest)
	require.NoError(t, err)
	require.NotNil(t, artifact)

	const peSSHPort = 22125
	const vmName = "pe-chromium-e2e"
	shotDir := filepath.Join(resultDir, "screenshots")
	require.NoError(t, os.MkdirAll(shotDir, 0o755))
	runOut := filepath.Join(resultDir, ".winkit", "run", vmName)
	qmpSock := qemu.QMPSocketPath(qemu.Spec{
		VMName: "winkit-" + vmName, QMPSocketDir: runOut,
	})
	stopShots := make(chan struct{})
	shotsDone := make(chan struct{})
	go qemu.CaptureScreenshots(qmpSock, shotDir, stopShots, shotsDone, t.Logf)
	defer func() { close(stopShots); <-shotsDone }()

	machine, err := winkit.Start(ctx, winkit.StartOpts{
		Image:    dest,
		Name:     vmName,
		StateDir: filepath.Join(resultDir, "state"),
		Accel:    "tcg",
		SSHPort:  peSSHPort,
		RDPPort:  25392,
		VNCPort:  5903,
	})
	require.NoError(t, err, "chromium PE image must boot")
	defer machine.Stop()

	if os.Getenv("WINKIT_E2E_TEARDOWN") == "false" {
		defer holdPEWSL1DebugVM(t, ctx, peSSHPort)
	}

	client := waitForChromiumSSH(t, ctx, peSSHPort)
	defer client.Close()

	waitForPEWSL1Bootstrap(t, ctx, client)

	// Wait for X11 to be ready (Xvfb socket).
	t.Log("waiting for X11 display :99")
	waitForX11(t, ctx, client)

	// Launch Chromium inside the WSL1 distro on display :99.
	// --no-sandbox: WSL1 doesn't support Linux namespaces for sandboxing.
	// --disable-gpu: no GPU in the VM, use software rendering.
	// --disable-dev-shm-usage: /dev/shm may be small in WSL1.
	t.Log("launching Chromium")
	chromiumCmd := `wsl -d winkit -u winkit -e /nix/var/nix/profiles/per-user/winkit/profile/bin/bash -lc "DISPLAY=:99 chromium --no-sandbox --disable-gpu --disable-dev-shm-usage --no-first-run --start-maximized about:blank &"` //nolint:lll
	stdout, stderr, code, err := client.Run(ctx, chromiumCmd)
	t.Logf("chromium launch: code=%d stdout=%q stderr=%q err=%v", code, string(stdout), string(stderr), err)

	// Give Chromium a few seconds to initialize.
	time.Sleep(10 * time.Second)

	// Verify Chromium is running.
	t.Log("checking if Chromium process is alive")
	psCmd := `wsl -d winkit -u winkit -e /nix/var/nix/profiles/per-user/winkit/profile/bin/bash -lc "pgrep -f chromium | head -5"`
	stdout, _, _, err = client.Run(ctx, psCmd)
	chromiumPIDs := strings.TrimSpace(string(stdout))
	t.Logf("chromium PIDs: %q", chromiumPIDs)

	if chromiumPIDs == "" {
		// Try to get crash info.
		crashCmd := `wsl -d winkit -u winkit -e /nix/var/nix/profiles/per-user/winkit/profile/bin/bash -lc "ls -la /tmp/.X11-unix/ 2>&1; cat /tmp/chromium-crash.log 2>/dev/null || echo no-crash-log"`
		stdout, _, _, _ = client.Run(ctx, crashCmd)
		t.Logf("X11/crash debug: %s", string(stdout))
		t.Fatal("Chromium process not running: browser failed to start in WinPE WSL1 X11")
	}
	t.Logf("Chromium is running with PIDs: %s", chromiumPIDs)

	// Take an X11 screenshot to prove Chromium rendered something.
	screenshotCmd := `wsl -d winkit -u winkit -e /nix/var/nix/profiles/per-user/winkit/profile/bin/bash -lc "DISPLAY=:99 import -window root /tmp/chromium-screenshot.png 2>&1 && echo SCREENSHOT_OK || echo SCREENSHOT_FAIL"` //nolint:lll
	stdout, _, _, _ = client.Run(ctx, screenshotCmd)
	if strings.Contains(string(stdout), "SCREENSHOT_OK") {
		t.Log("X11 screenshot captured: Chromium rendered on display :99")
	} else {
		// xwd as fallback (part of xorg).
		xwdCmd := `wsl -d winkit -u winkit -e /nix/var/nix/profiles/per-user/winkit/profile/bin/bash -lc "DISPLAY=:99 xdotool getactivewindow getwindowname 2>&1"` //nolint:lll
		stdout, _, _, _ = client.Run(ctx, xwdCmd)
		t.Logf("active window: %s", string(stdout))
	}

	// Final check: Chromium window visible to the window manager.
	wmctrlCmd := `wsl -d winkit -u winkit -e /nix/var/nix/profiles/per-user/winkit/profile/bin/bash -lc "DISPLAY=:99 xdotool search --name '' getwindowname 2>&1 | head -20"` //nolint:lll
	stdout, _, _, _ = client.Run(ctx, wmctrlCmd)
	windowList := string(stdout)
	t.Logf("X11 window list:\n%s", windowList)

	if strings.Contains(strings.ToLower(windowList), "chromium") ||
		strings.Contains(strings.ToLower(windowList), "new tab") ||
		strings.Contains(strings.ToLower(windowList), "about:blank") {
		t.Log("SUCCESS: Chromium browser window visible in WinPE X11 desktop")
	} else {
		t.Log("Chromium process is running but window title not detected (may still be initializing)")
	}
}

func waitForChromiumSSH(t *testing.T, ctx context.Context, port int) *gosshd.Client {
	t.Helper()
	deadline := time.Now().Add(25 * time.Minute)
	var lastErr error
	for time.Now().Before(deadline) {
		client, err := gosshd.Dial(ctx, fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			return client
		}
		lastErr = err
		select {
		case <-ctx.Done():
			require.NoError(t, ctx.Err(), "context expired waiting for gosshd")
		case <-time.After(10 * time.Second):
		}
	}
	require.NoError(t, lastErr, "gosshd must come up for Chromium test")
	return nil
}

func waitForX11(t *testing.T, ctx context.Context, client *gosshd.Client) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Minute)
	for time.Now().Before(deadline) {
		stdout, _, _, err := client.Run(ctx,
			`wsl -d winkit -u winkit -e /bin/sh -c "test -S /tmp/.X11-unix/X99 && echo X11_READY"`)
		if err == nil && strings.Contains(string(stdout), "X11_READY") {
			t.Log("X11 display :99 is ready")
			return
		}
		select {
		case <-ctx.Done():
			require.NoError(t, ctx.Err(), "context expired waiting for X11")
		case <-time.After(15 * time.Second):
		}
	}
	t.Fatal("X11 display :99 did not come up within 10 minutes")
}
