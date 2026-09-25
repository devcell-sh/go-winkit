//go:build integration

package e2e

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	winkit "github.com/devcell-sh/go-winkit"
	"github.com/devcell-sh/go-winkit/build"
	"github.com/devcell-sh/go-winkit/cache"
	"github.com/devcell-sh/go-winkit/internal/cli"
	"github.com/devcell-sh/go-winkit/internal/testutil"
	"github.com/devcell-sh/go-winkit/vm/qemu"
)

// TestExamplePEWSL1Nix_E2E exercises the examples/pe-wsl1-nix contract.
// The nix rootfs is built via Docker (nixos/nix base + home-manager), so this
// test requires Docker in addition to the seeded cache and QEMU.
func TestExamplePEWSL1Nix_E2E(t *testing.T) {
	if os.Getenv("WINKIT_E2E") != "1" {
		t.Skip("set WINKIT_E2E=1 to run (needs seeded cache, docker, QEMU)")
	}
	_ = windowsISOPath(t)
	_ = virtioISOPath(t)

	exampleDir, err := filepath.Abs(filepath.Join("..", "..", "examples", "pe-wsl1-nix"))
	require.NoError(t, err)
	resultDir := testutil.ResultDir(t)
	dest := filepath.Join(resultDir, "winkit-core.qcow2")

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Minute)
	defer cancel()

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
	require.NoError(t, err, "winkit build failed:\n%s", cliOutput.String())
	t.Logf("winkit build output:\n%s", cliOutput.String())

	artifact, err := build.LoadArtifact(dest)
	require.NoError(t, err, "CLI output must have a valid artifact manifest")
	require.NotNil(t, artifact, "PE+WSL1 must be a declared multi-disk artifact")
	require.Equal(t, build.ArtifactKindPEWSL1, artifact.Kind)
	require.FileExists(t, artifact.BootVolume)
	require.FileExists(t, artifact.DataDisk)

	const peSSHPort = 22123
	const vmName = "pe-wsl1-nix-e2e"
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
		RDPPort:  25390,
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

	waitForPEWSL1Bootstrap(t, ctx, client)
	bootstrap := run(`pwsh -NoLogo -NoProfile -NonInteractive -File X:\winkit\status-wsl1.ps1`)
	require.Contains(t, bootstrap, "WSL1_BOOTSTRAP_OK")

	// Verify probe outputs: the probe runs WSL commands as the winkit user
	// and writes results to E:\winkit\*.out.
	list := strings.ReplaceAll(run(`type E:\winkit\list.out`), "\x00", "")
	require.Regexp(t, `(?mi)winkit\s+(stopped|running)\s+1\s*$`, list,
		"the imported distro must be WSL1: %s", list)

	arch := strings.ToLower(strings.ReplaceAll(run(`type E:\winkit\arch.out`), "\x00", ""))
	require.Contains(t, arch, "aarch64")

	// The probe runs nix --version via the absolute store path (no login
	// shell needed). This is the real proof that the nix rootfs works
	// inside WSL1.
	nixVer := strings.TrimSpace(strings.ReplaceAll(
		run(`type E:\winkit\nix-version.out`), "\x00", ""))
	require.Contains(t, nixVer, "nix (Nix)",
		"nix --version must succeed inside the WSL1 distro: %s", nixVer)
	t.Logf("nix version: %s", nixVer)
}

// waitForPEWSL1NixSSH and holdPEWSL1NixDebugVM are reused from the Alpine
// test file (same package): waitForPEWSL1SSH, holdPEWSL1DebugVM,
// waitForPEWSL1Bootstrap, windowsISOPath, virtioISOPath.
