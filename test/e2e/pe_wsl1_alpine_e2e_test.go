//go:build integration

package e2e

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
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

// TestExamplePEWSL1Alpine_E2E is the production acceptance test for the
// examples/pe-wsl1-alpine contract. It deliberately contains no WIM patching,
// registry setup, disk preparation, or distro import logic: the example is
// built through the real winkit CLI, booted through the public Start API, and
// queried through the same SSH-facing `wsl` command a user runs.
func TestExamplePEWSL1Alpine_E2E(t *testing.T) {
	if os.Getenv("WINKIT_E2E") != "1" {
		t.Skip("set WINKIT_E2E=1 to run (needs seeded cache, docker, QEMU)")
	}
	// Fail/skip before the CLI starts if the expensive seeded inputs are absent.
	_ = windowsISOPath(t)
	_ = virtioISOPath(t)

	exampleDir, err := filepath.Abs(filepath.Join("..", "..", "examples", "pe-wsl1-alpine"))
	require.NoError(t, err)
	resultDir := testutil.ResultDir(t)
	dest := filepath.Join(resultDir, "winkit-core.qcow2")

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Minute)
	defer cancel()

	// L2: exercise the thin CLI and directory-form --file exactly as a user
	// does. All assembly and stage dispatch must happen below the CLI boundary.
	var cliOutput bytes.Buffer
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
	require.NoError(t, err, "standard winkit build failed:\n%s", cliOutput.String())
	t.Logf("winkit build output:\n%s", cliOutput.String())

	artifact, err := build.LoadArtifact(dest)
	require.NoError(t, err, "CLI output must have a valid artifact manifest")
	require.NotNil(t, artifact, "PE+WSL1 must be a declared multi-disk artifact")
	require.Equal(t, build.ArtifactKindPEWSL1, artifact.Kind)
	require.FileExists(t, artifact.BootVolume)
	require.FileExists(t, artifact.DataDisk)

	// L3: boot only the CLI-produced artifact. Start reads the manifest and
	// attaches the FAT volume as boot media plus the writable NVMe data disk.
	const peSSHPort = 22122
	const vmName = "pe-wsl1-alpine-e2e"
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
		Accel:    os.Getenv("WINKIT_E2E_ACCEL"),
		SSHPort:  peSSHPort,
		RDPPort:  25389,
	})
	require.NoError(t, err, "CLI artifact must boot via winkit.Start")
	defer machine.Stop()

	if os.Getenv("WINKIT_E2E_TEARDOWN") == "false" {
		defer holdPEWSL1DebugVM(t, ctx, peSSHPort)
	}

	client := waitForPEWSL1SSH(t, ctx, peSSHPort)
	defer client.Close()
	run := func(command string) string {
		t.Helper()
		stdout, stderr, code, runErr := client.Run(ctx, command)
		require.NoError(t, runErr, "running %q", command)
		require.Zero(t, code, "%q exited %d\nstdout: %s\nstderr: %s", command, code, stdout, stderr)
		return string(stdout) + string(stderr)
	}

	// gosshd owns the WinPE shell lifetime while bootstrap runs alongside it.
	// Wait for the explicit production marker before making WSL assertions.
	waitForPEWSL1Bootstrap(t, ctx, client)
	bootstrap := run(`pwsh -NoLogo -NoProfile -NonInteractive -File X:\winkit\status-wsl1.ps1`)
	require.Contains(t, bootstrap, "WSL1_BOOTSTRAP_OK")

	// The bootstrap probe already ran WSL commands as the winkit user and
	// wrote results to E:\winkit\*.out. Verify those files directly: this
	// matches the production contract (probe-wsl1.ps1 is the authority)
	// and avoids CreateProcessAsUser path-with-spaces issues through SSH.
	list := strings.ReplaceAll(run(`type E:\winkit\list.out`), "\x00", "")
	require.Regexp(t, `(?mi)winkit\s+(stopped|running)\s+1\s*$`, list,
		"the imported distro must be WSL1: %s", list)

	version := strings.TrimSpace(strings.ReplaceAll(
		run(`type E:\winkit\alpine-release.out`), "\x00", ""))
	require.NotEmpty(t, version, "Alpine version must be returned by the WSL1 probe")
	t.Logf("Alpine version: %s", version)

	osRelease := strings.ReplaceAll(run(`type E:\winkit\os-release.out`), "\x00", "")
	require.Contains(t, osRelease, "Alpine Linux")
	arch := strings.ToLower(strings.ReplaceAll(run(`type E:\winkit\arch.out`), "\x00", ""))
	require.Contains(t, arch, "aarch64")
}

func waitForPEWSL1Bootstrap(t *testing.T, ctx context.Context, client *gosshd.Client) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Minute)
	for time.Now().Before(deadline) {
		stdout, stderr, code, err := client.Run(ctx,
			`pwsh -NoLogo -NoProfile -NonInteractive -File X:\winkit\status-wsl1.ps1`)
		if err != nil {
			t.Fatalf("SSH connection failed while waiting for WSL1 bootstrap: %v", err)
		}
		out := string(stdout) + string(stderr)
		if code == 0 && strings.Contains(out, "WSL1_BOOTSTRAP_OK") {
			return
		}
		if strings.Contains(out, "WSL1_BOOTSTRAP_FAILED") {
			t.Fatalf("WSL1 bootstrap failed:\n%s", out)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("context expired waiting for WSL1 bootstrap: %v", ctx.Err())
		case <-time.After(5 * time.Second):
		}
	}
	t.Fatal("timed out waiting for WSL1 bootstrap marker")
}

func waitForPEWSL1SSH(t *testing.T, ctx context.Context, port int) *gosshd.Client {
	t.Helper()
	deadline := time.Now().Add(20 * time.Minute)
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
	require.NoError(t, lastErr, "gosshd must come up after WSL1 bootstrap")
	return nil
}

func holdPEWSL1DebugVM(t *testing.T, ctx context.Context, peSSHPort int) {
	t.Helper()
	t.Logf("debug VM retained; SSH: ssh -p %d -o StrictHostKeyChecking=no admin@127.0.0.1 (password: admin)", peSSHPort)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	select {
	case <-sigCh:
		t.Log("interrupt received; shutting down")
	case <-ctx.Done():
		t.Log("context timeout; shutting down")
	}
}
