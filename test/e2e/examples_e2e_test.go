package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	winkit "github.com/devcell-sh/go-winkit"
	"github.com/devcell-sh/go-winkit/build"
	"github.com/devcell-sh/go-winkit/build/buildopts"
	"github.com/devcell-sh/go-winkit/cache"
	"github.com/devcell-sh/go-winkit/gosshd"
	"github.com/devcell-sh/go-winkit/internal/config"
	"github.com/devcell-sh/go-winkit/internal/testutil"
	"github.com/devcell-sh/go-winkit/vm/qemu"
)

// These tests build the examples/ configs for real, so an example that
// merely passes schema validation but breaks the build fails here, not
// on a user's machine. They read seeded media (task test:seed) and skip
// without it, matching the tier conventions of this directory.

// virtioISOPath mirrors windowsISOPath for the driver ISO.
func virtioISOPath(t *testing.T) string {
	t.Helper()
	p := cache.VirtIOISO()
	if _, err := os.Stat(p); err != nil {
		t.Skipf("virtio-win ISO not available at %s (run: task test:seed)", p)
	}
	return p
}

// loadExampleOpts loads and validates an example's winkit.yaml and
// returns the example dir plus converted BuildOpts. Relative paths in
// the config (wsl.services) are resolved against the example dir, the
// same way the CLI resolves them against the config's directory.
func loadExampleOpts(t *testing.T, name string) (string, *buildopts.BuildOpts) {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "examples", name))
	require.NoError(t, err)
	cfg, err := config.Load(filepath.Join(dir, "winkit.yaml"))
	require.NoError(t, err, "example config must load")
	require.NoError(t, cfg.Validate(), "example config must validate")
	opts, err := cfg.ToBuildOpts()
	require.NoError(t, err)
	if opts.WSL != nil && opts.WSL.ServicesDir != "" && !filepath.IsAbs(opts.WSL.ServicesDir) {
		opts.WSL.ServicesDir = filepath.Join(dir, opts.WSL.ServicesDir)
	}
	return dir, opts
}

// TestExamplePEMinimal_Build runs the pe-minimal example through the
// public winkit.Build entry point: gosshd cross-compile, PowerShell
// payload, WIM injection, FAT volume mastering. Minutes, no VM boot —
// so it lives in the default e2e tier, not behind the integration tag.
func TestExamplePEMinimal_Build(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a PE volume and may fetch the pwsh payload; skipped in -short")
	}
	winISO := windowsISOPath(t)
	virtioISO := virtioISOPath(t)

	_, opts := loadExampleOpts(t, "pe-minimal")
	require.True(t, opts.PE, "pe-minimal must select PE mode")

	resultDir := testutil.ResultDir(t)
	dest := filepath.Join(resultDir, "pe.qcow2")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	err := winkit.Build(ctx, build.Config{
		Dest:       dest,
		CacheDir:   cache.Dir(),
		WindowsISO: winISO,
		VirtIOISO:  virtioISO,
		Opts:       opts,
		Logger:     testLogger(t, resultDir),
	})
	require.NoError(t, err, "pe-minimal example must build")

	// The volume must carry the injected boot.wim and a bootloader —
	// the two files WinPE cannot boot without.
	wim, err := qemu.ReadFileFromFATQcow2(dest, "sources/boot.wim")
	require.NoError(t, err, "PE volume must contain sources/boot.wim")
	require.Greater(t, len(wim), 100<<20, "boot.wim should be a real WIM, got %d bytes", len(wim))

	var bootloader []byte
	for _, p := range []string{"EFI/BOOT/BOOTAA64.EFI", "EFI/Boot/bootaa64.efi"} {
		if data, err := qemu.ReadFileFromFATQcow2(dest, p); err == nil {
			bootloader = data
			break
		}
	}
	require.NotEmpty(t, bootloader, "PE volume must contain the ARM64 bootloader")

	if os.Getenv("WINKIT_E2E") != "1" {
		t.Log("PE volume built and inspected; set WINKIT_E2E=1 to also boot it and verify gosshd")
		return
	}

	// PE-level verification: boot the volume with winkit.Start, capture
	// periodic screenshots, and prove gosshd answers inside WinPE.
	// Ports offset from the defaults so this can run alongside a
	// wsl-alpine build/run on the same host.
	const peSSHPort = 21022
	const vmName = "pe-minimal-e2e"
	bootCtx, cancelBoot := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancelBoot()

	shotDir := filepath.Join(resultDir, "screenshots")
	require.NoError(t, os.MkdirAll(shotDir, 0o755))
	// winkit.Start places the run dir next to the image; the QMP socket
	// path derives from it, so the capturer can start before the VM.
	runOut := filepath.Join(resultDir, ".winkit", "run", vmName)
	qmpSock := qemu.QMPSocketPath(qemu.Spec{VMName: "winkit-" + vmName, QMPSocketDir: runOut})
	stopShots := make(chan struct{})
	shotsDone := make(chan struct{})
	go qemu.CaptureScreenshots(qmpSock, shotDir, stopShots, shotsDone, t.Logf)
	defer func() { close(stopShots); <-shotsDone }()

	machine, err := winkit.Start(bootCtx, winkit.StartOpts{
		Image:    dest,
		Name:     vmName,
		StateDir: filepath.Join(resultDir, "state"),
		SSHPort:  peSSHPort,
		RDPPort:  24389,
	})
	require.NoError(t, err, "PE volume must boot via winkit.Start")
	defer machine.Stop()

	var client *gosshd.Client
	deadline := time.Now().Add(15 * time.Minute)
	for {
		client, err = gosshd.Dial(bootCtx, fmt.Sprintf("127.0.0.1:%d", peSSHPort))
		if err == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Second)
	}
	require.NoError(t, err, "gosshd must come up inside WinPE")
	defer client.Close()

	stdout, stderr, code, err := client.Run(bootCtx, "cmd /c ver")
	require.NoError(t, err)
	require.Zero(t, code, "cmd /c ver exited %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	require.Contains(t, string(stdout), "Windows", "WinPE must identify as Windows")
}

// TestExampleFullWSL1Alpine_E2E runs the wsl-alpine example end to end:
// full unattended install, alpine distro import, the myagent s6
// service, and the htop wsl-phase hook — then boots the image with
// winkit.Start and verifies the guest over gosshd. Multi-hour on TCG;
// opt-in via WINKIT_E2E=1 like TestQcowBuilderWSL.
func TestExampleFullWSL1Alpine_E2E(t *testing.T) {
	if os.Getenv("WINKIT_E2E") != "1" {
		t.Skip("full Windows install; opt in with WINKIT_E2E=1 (needs seeded cache, docker, QEMU)")
	}
	winISO := windowsISOPath(t)
	virtioISO := virtioISOPath(t)

	_, opts := loadExampleOpts(t, "wsl-alpine")
	require.NotNil(t, opts.WSL)

	resultDir := testutil.ResultDir(t)
	dest := filepath.Join(resultDir, "wsl-alpine.qcow2")
	workDir := filepath.Join(resultDir, "work")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Hour)
	defer cancel()

	// Periodic QMP screenshots into the result dir, same pattern as the
	// CLI's --debug: the socket path is deterministic and the capturer
	// idles until QEMU creates it.
	shotDir := filepath.Join(resultDir, "screenshots")
	require.NoError(t, os.MkdirAll(shotDir, 0o755))
	qmpSock := qemu.QMPSocketPath(qemu.Spec{
		VMName: "winkit-install", QMPSocketDir: filepath.Join(workDir, "install")})
	stopShots := make(chan struct{})
	shotsDone := make(chan struct{})
	go qemu.CaptureScreenshots(qmpSock, shotDir, stopShots, shotsDone, t.Logf)
	defer func() { close(stopShots); <-shotsDone }()

	// Build failure includes hook failure: hookexec fails the build if
	// `apk add htop` exits non-zero, so a green build already proves
	// the wsl-phase hook ran. StructuredLogPath streams pe-agent events
	// directly into build.jsonl alongside host build events.
	buildJSONL := filepath.Join(resultDir, "build.jsonl")
	err := winkit.Build(ctx, build.Config{
		Dest:              dest,
		CacheDir:          cache.Dir(),
		WindowsISO:        winISO,
		VirtIOISO:         virtioISO,
		WorkDir:           workDir,
		Opts:              opts,
		Logger:            testLogger(t, resultDir),
		StructuredLogPath: buildJSONL,
	})
	require.NoError(t, err, "wsl-alpine example must build")

	// Verify pe-agent streamed events during the WinPE phase.
	t.Run("pe-agent", func(t *testing.T) {
		data, rerr := os.ReadFile(buildJSONL)
		require.NoError(t, rerr, "build.jsonl must exist")
		require.NotEmpty(t, data, "build.jsonl must not be empty")
		var peAgentLines int
		for _, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, `"event"`) &&
				(strings.Contains(line, `"setupact"`) ||
					strings.Contains(line, `"setuperr"`) ||
					strings.Contains(line, `"pe-agent-`) ||
					strings.Contains(line, `"setupapi"`)) {
				peAgentLines++
			}
		}
		t.Logf("build.jsonl: %d pe-agent event lines", peAgentLines)
		require.Greater(t, peAgentLines, 0,
			"pe-agent must stream at least one structured event to build.jsonl during WinPE boot")
	})

	// Boot the produced image and verify the consumer-visible state.
	machine, err := winkit.Start(ctx, winkit.StartOpts{
		Image:    dest,
		StateDir: filepath.Join(resultDir, "state"),
	})
	require.NoError(t, err, "built image must boot via winkit.Start")
	defer machine.Stop()

	var client *gosshd.Client
	deadline := time.Now().Add(20 * time.Minute)
	for {
		client, err = gosshd.Dial(ctx, "127.0.0.1:20022")
		if err == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(15 * time.Second)
	}
	require.NoError(t, err, "gosshd must come up after boot")
	defer client.Close()

	run := func(cmd string) string {
		stdout, stderr, code, err := client.Run(ctx, cmd)
		require.NoError(t, err, "running %q", cmd)
		require.Zero(t, code, "%q exited %d\nstdout: %s\nstderr: %s", cmd, code, stdout, stderr)
		return string(stdout)
	}

	// The three consumer surfaces the example promises:
	out := run(`wsl -d winkit -- s6-svstat /etc/s6/services/myagent`)
	require.Contains(t, out, "up", "myagent service must be supervised and up")
	run(`wsl -d winkit -- cat /tmp/myagent-heartbeat`)
	run(`wsl -d winkit -- htop --version`)

	// Verify pe-agent ran during the second-pass install phase via
	// SetupExecute. Two verification paths:
	// 1. Boot FAT volume: pe-agent discovers it by marker and writes there
	// 2. SSH fallback: read C:\winkit-pe-agent.log from the OS drive
	t.Run("pe-agent-second-pass", func(t *testing.T) {
		bootVolPath := filepath.Join(workDir, "wsl-boot.qcow2")
		bootLog, bootErr := qemu.ReadFileFromFATQcow2(bootVolPath, "winkit-pe-agent.log")
		if bootErr == nil && len(bootLog) > 0 {
			t.Logf("pe-agent second-pass log from boot volume (%d bytes):\n%s", len(bootLog), string(bootLog))
		} else {
			t.Logf("boot volume log not available: %v", bootErr)
		}

		stdout, _, code, err := client.Run(ctx,
			`cmd /c "if exist C:\winkit-pe-agent.log (type C:\winkit-pe-agent.log) else (echo NOT_FOUND)"`)
		require.NoError(t, err)
		sshFound := code == 0 && !strings.Contains(string(stdout), "NOT_FOUND")
		if sshFound {
			t.Logf("pe-agent second-pass log from SSH (%d bytes):\n%s", len(stdout), string(stdout))
		}

		if len(bootLog) == 0 && !sshFound {
			t.Log("pe-agent second-pass log not found on either boot volume or OS drive")
			t.Log("This is expected if install.wim was not patched (CGO_ENABLED=0)")
		}

		// Read serial probe results from the dedicated probe log on C:.
		// The probe writes here directly at pe-agent startup, before the
		// boot volume is discovered, so it captures COM port enumeration
		// even when --log-file is dropped by detachPEAgent.
		probeOut, _, probeCode, probeErr := client.Run(ctx,
			`cmd /c "if exist C:\winkit-serial-probe.log (type C:\winkit-serial-probe.log) else (echo PROBE_NOT_FOUND)"`)
		if probeErr == nil && probeCode == 0 && !strings.Contains(string(probeOut), "PROBE_NOT_FOUND") {
			t.Logf("serial probe log (%d bytes):\n%s", len(probeOut), string(probeOut))
		} else {
			t.Logf("serial probe log not found on C: (code=%d err=%v)", probeCode, probeErr)
		}
	})
}


type tlogHandler struct {
	t     *testing.T
	file  *os.File
	attrs []slog.Attr
}

func (h *tlogHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *tlogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &tlogHandler{t: h.t, file: h.file, attrs: append(h.attrs[:len(h.attrs):len(h.attrs)], attrs...)}
}
func (h *tlogHandler) WithGroup(string) slog.Handler { return h }
func (h *tlogHandler) Handle(_ context.Context, r slog.Record) error {
	msg := r.Message
	attrs := make(map[string]string)
	r.Attrs(func(a slog.Attr) bool { attrs[a.Key] = a.Value.String(); return true })
	for _, a := range h.attrs {
		attrs[a.Key] = a.Value.String()
	}
	// Console output
	line := msg
	for k, v := range attrs {
		line += " " + k + "=" + v
	}
	h.t.Log(line)
	// JSONL file: ts first, then event, msg, then extra attrs.
	if h.file != nil {
		var buf []byte
		buf = append(buf, '{')
		appendKV := func(k, v string) {
			if len(buf) > 1 {
				buf = append(buf, ',')
			}
			kj, _ := json.Marshal(k)
			vj, _ := json.Marshal(v)
			buf = append(buf, kj...)
			buf = append(buf, ':')
			buf = append(buf, vj...)
		}
		appendKV("ts", r.Time.UTC().Format(time.RFC3339Nano))
		appendKV("event", "build")
		appendKV("msg", msg)
		for k, v := range attrs {
			appendKV(k, v)
		}
		buf = append(buf, '}', '\n')
		h.file.Write(buf)
	}
	return nil
}

func testLogger(t *testing.T, resultDir ...string) *slog.Logger {
	var f *os.File
	if len(resultDir) > 0 {
		var err error
		f, err = os.OpenFile(filepath.Join(resultDir[0], "build.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err == nil {
			t.Cleanup(func() { f.Close() })
		}
	}
	return slog.New(&tlogHandler{t: t, file: f})
}
