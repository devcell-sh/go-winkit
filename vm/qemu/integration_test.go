//go:build wimlib && integration

package qemu

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/devcell-sh/go-wimlib"
	"github.com/devcell-sh/go-winkit/cache"
	"github.com/devcell-sh/go-winkit/gosshd"
	"github.com/devcell-sh/go-winkit/internal/testutil"
	"github.com/devcell-sh/go-winkit/winpe"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWimBuilder drives the full WIM builder pipeline through winpe.Run():
// build WinPE artifacts, boot a QEMU VM, wait for DISM offline servicing,
// extract winkit.wim, and verify its contents.
//
//	go test -tags 'wimlib integration' -run TestWimBuilder/tcg -timeout 30m -v ./winpe/qemu/
//	go test -tags 'wimlib integration' -run TestWimBuilder/hvf -timeout 30m -v ./winpe/qemu/
func TestWimBuilder(t *testing.T) {
	if testing.Short() {
		t.Skip("long: boots WinPE in QEMU for DISM servicing")
	}

	qemuBin := requireQEMUBin(t)
	winISO := requireWindowsISO(t)
	virtioISO := requireVirtioISO(t)
	pwshFiles := requirePwshFiles(t)
	gosshdExe := requireGosshdExe(t)

	for _, accel := range []string{"tcg", "hvf"} {
		t.Run(accel, func(t *testing.T) {
			if accel == "hvf" && runtime.GOOS != "darwin" {
				t.Skip("hvf requires macOS")
			}

			// A durable result dir, not t.TempDir(): the serial log,
			// screenshots and build.jsonl are the only evidence of what a
			// guest did, and a failed boot is exactly when Go would have
			// deleted them.
			outDir := testutil.ResultDir(t)
			qemuAccel := accel
			if accel == "tcg" {
				qemuAccel = "tcg,thread=multi"
			}
			t.Logf("results: %s", outDir)

			runner := NewRunner(qemuBin, qemuAccel)

			// Driver-only profile — the proven "boot-wim" scope.
			// Inbox optional features cannot be DISM-enabled in a WinPE
			// image (CBS 0x800f080c: their packages parent
			// Microsoft-Windows-Foundation-Package while boot.wim's is
			// Microsoft-Windows-WinPE-Package). OpenSSH capabilities need
			// Windows Update and the builder VM has no route out.
			cfg := winpe.RunConfig{
				Build: winpe.BuildConfig{
					WindowsISO:   winISO,
					VirtIOISO:    virtioISO,
					PwshFiles:    pwshFiles,
					GosshdExe:    gosshdExe,
					OutputDir:    outDir,
					VirtIO:       true,
					ProgressPort: `\\.\Global\` + ProgressPortName,
				},
				PollInterval: 15 * time.Second,
				// The full DISM pass finishes at ~25.5 min under TCG in
				// this container; leave margin for the shutdown + flush.
				Timeout: 32 * time.Minute,
				// This container's cgroup caps memory at 4GB; the 5GB
				// default OOM-kills the test once DISM touches pages.
				// Winkit used 5GB on a bare host; in this container the 8GB
				// cgroup (.winkit.toml mem_limit) must also hold QEMU/TCG
				// overhead, the test process, and page cache — a 5GB guest
				// was OOM-killed at ~2.2GB internal use. 4GB leaves the
				// host side ~4GB and still exceeds observed guest demand.
				MemoryGB: 4,
			}

			ctx, cancel := context.WithTimeout(context.Background(), 38*time.Minute)
			defer cancel()

			t.Log("starting WIM builder (build + boot + DISM + extract)")
			result, err := winpe.Run(ctx, runner, cfg)
			require.NoError(t, err, "winpe.Run must complete successfully")
			require.NotEmpty(t, result.DevcellWim, "winkit.wim must not be empty")

			t.Logf("winkit.wim: %d bytes (%.1f MB)",
				len(result.DevcellWim), float64(len(result.DevcellWim))/(1024*1024))

			// Log guest output if available.
			for name, content := range result.GuestLogs {
				logPath := filepath.Join(outDir, "log-"+name)
				os.WriteFile(logPath, []byte(content), 0644)
				t.Logf("guest log %s: %d bytes", name, len(content))
			}

			agentOut := result.GuestLogs[winpe.AgentResultFile]

			// Builder script assertions.
			assert.Contains(t, agentOut, "WINKIT WIM BUILDER",
				"builder script header must appear in output")
			assert.Contains(t, agentOut, "Found virtio-win ISO",
				"builder must find the driver directories")
			assert.Contains(t, agentOut, "boot.wim committed successfully")
			assert.Contains(t, agentOut, "winkit.wim created")
			assert.NotContains(t, agentOut, "Mounting install.wim",
				"driver-only build must not mount install.wim")

			// gosshd must land on the shared volume — it's the base
			// stack's SSH server (Win32-OpenSSH cannot run in WinPE).
			gosshdData, err := ReadFileFromFATQcow2(
				filepath.Join(outDir, "shared.qcow2"), "/"+winpe.GosshdVolumeName)
			require.NoError(t, err, "gosshd.exe must be on the shared volume")
			require.True(t, len(gosshdData) > 1024*1024 &&
				gosshdData[0] == 'M' && gosshdData[1] == 'Z',
				"gosshd.exe must be a real PE binary (%d bytes)", len(gosshdData))

			// Verify WIM contents with wimlib.
			verifyDevcellWim(t, result.DevcellWim, agentOut, outDir)
		})
	}
}

// verifyDevcellWim writes winkit.wim to disk, opens it with wimlib, and
// asserts the expected features were applied by DISM.
func verifyDevcellWim(t *testing.T, wimData []byte, agentOut, outDir string) {
	t.Helper()

	wimPath := filepath.Join(outDir, "winkit.wim")
	require.NoError(t, os.WriteFile(wimPath, wimData, 0644))

	wim, err := wimlib.OpenWIM(wimPath)
	require.NoError(t, err, "opening winkit.wim with wimlib")
	defer wim.Close()

	count, err := wim.ImageCount()
	require.NoError(t, err)
	require.GreaterOrEqual(t, count, 2,
		"winkit.wim must retain at least 2 images from install.wim")

	extractDir := filepath.Join(outDir, "winkit-extracted")
	require.NoError(t, os.MkdirAll(extractDir, 0755))
	require.NoError(t, wim.ExtractImage(2, extractDir, nil))

	// Inbox optional features cannot be DISM-enabled in a WinPE image (CBS
	// parent package mismatch). OpenSSH capabilities need Windows Update
	// access the builder VM doesn't have. This test verifies the DISM
	// driver path.

	// VirtIO drivers in DriverStore.
	assert.Contains(t, agentOut, `OK: Add-Driver NetKVM\w11\ARM64`)
	assert.Contains(t, agentOut, `OK: Add-Driver vioserial\w11\ARM64`)
	assert.Contains(t, agentOut, `OK: Add-Driver vioscsi\w11\ARM64`)

	driverStoreDir := filepath.Join(extractDir,
		"Windows", "System32", "DriverStore", "FileRepository")
	for _, drv := range []struct {
		name string
		sys  string
	}{
		{"NetKVM", "netkvm.sys"},
		{"vioserial", "vioser.sys"},
		{"vioscsi", "vioscsi.sys"},
	} {
		matches, _ := filepath.Glob(filepath.Join(driverStoreDir, "*", drv.sys))
		if len(matches) > 0 {
			info, _ := os.Stat(matches[0])
			t.Logf("  VirtIO OK: %s -> %s (%d bytes)",
				drv.name, filepath.Base(filepath.Dir(matches[0])), info.Size())
		} else {
			t.Errorf("  VirtIO MISSING: %s (%s not found in DriverStore/FileRepository)",
				drv.name, drv.sys)
		}
	}
}

// TestVMPVerify is the two-pass VMP verification test:
//
//   - Pass 1: Build winkit.wim with VMP transplant
//
//   - Pass 2: Boot from that WIM and verify services are live
//
//     go test -tags 'wimlib integration' -run TestVMPVerify/tcg -timeout 90m -v ./winpe/qemu/
//
// TestQcowBuilder builds the base-stack qcow2 image (virtio drivers +
// gosshd shell — what `winkit build qemu-image` produces), boots it in
// QEMU, and proves the SSH path end to end: dial gosshd through the host
// port forward and run a command in the guest.
//
//	go test -tags 'wimlib integration' -run TestQcowBuilder/base -timeout 20m -v ./winpe/qemu/
//	go test -tags 'wimlib integration' -run TestQcowBuilder/prebuilt -timeout 20m -v ./winpe/qemu/
//	go test -tags 'wimlib integration' -run TestQcowBuilder/vmp -timeout 20m -v ./winpe/qemu/
//	go test -tags 'wimlib integration' -run TestQcowBuilder/interactive -timeout 60m -v ./winpe/qemu/
func TestQcowBuilder(t *testing.T) {
	if testing.Short() {
		t.Skip("long: boots the base WinPE image in QEMU")
	}

	qemuBin := requireQEMUBin(t)

	// prebuilt: boots a pre-built winpe-base.qcow2 from testdata, skipping
	// the build step. This verifies the SSH boot chain end-to-end without
	// requiring cached Windows/virtio ISOs.
	t.Run("prebuilt", func(t *testing.T) {
		img := filepath.Join("testdata", "winpe-base.qcow2")
		if _, err := os.Stat(img); err != nil {
			// Also check the repo-root testdata.
			img = filepath.Join("..", "..", "test", "testdata", "winpe-base.qcow2")
			if _, err := os.Stat(img); err != nil {
				t.Skipf("winpe-base.qcow2 not in testdata (copy one there to enable this test)")
			}
		}
		t.Logf("using pre-built image: %s", img)

		outDir := testutil.ResultDir(t)
		t.Logf("results: %s", outDir)

		diskPath := filepath.Join(outDir, "scratch.qcow2")
		require.NoError(t, CreateDisk(diskPath, 8))
		fwPath := FirmwarePath()
		require.NotEmpty(t, fwPath, "no UEFI firmware found")
		varsPath := filepath.Join(outDir, "vars.fd")
		require.NoError(t, PrepareVarsFile(fwPath, varsPath))

		sshPort := freeTCPPort(t)
		spec := Spec{
			VMName:                 "winkit-qcow-prebuilt-test",
			CPUs:                   2,
			MemoryGB:               2,
			DiskPath:               diskPath,
			FirmwarePath:           fwPath,
			VarsPath:               varsPath,
			QMPSocketDir:           outDir,
			DisplayType:            "none",
			Accel:                  "tcg,thread=multi",
			SerialLogPath:          filepath.Join(outDir, "serial.log"),
			GuestStructuredLogPath: filepath.Join(outDir, "build.jsonl"),
			NoReboot:               true,
			SSHPort:                sshPort,
		}
		spec.ApplyDefaults()
		require.NoError(t, spec.Validate())

		argv := BuildQcowBootArgv(spec, img)
		argv[0] = qemuBin
		require.NoError(t, EnsureScreenshotDir(outDir, ScreenSourceQMP))

		t.Logf("QEMU argv: %s", strings.Join(argv, " "))
		cmd := exec.Command(argv[0], argv[1:]...)
		require.NoError(t, cmd.Start(), "starting QEMU")
		qemuDone := make(chan struct{})
		go func() { cmd.Wait(); close(qemuDone) }()
		defer func() {
			QMPQuit(QMPSocketPath(spec))
			select {
			case <-qemuDone:
			case <-time.After(15 * time.Second):
				cmd.Process.Kill()
				<-qemuDone
			}
		}()

		addr := fmt.Sprintf("127.0.0.1:%d", sshPort)
		deadline := time.Now().Add(12 * time.Minute)
		var client *gosshd.Client
		var err error
		seq := 0
		for time.Now().Before(deadline) {
			select {
			case <-qemuDone:
				serialLog, _ := os.ReadFile(filepath.Join(outDir, "serial.log"))
				t.Fatalf("QEMU exited before gosshd answered\nserial.log:\n%s", string(serialLog))
			default:
			}
			seq++
			ppm := ScreenshotPath(outDir, ScreenSourceQMP, time.Now(), "none", seq, seq, "ppm")
			QMPScreendump(QMPSocketPath(spec), ppm)

			dialCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			client, err = gosshd.Dial(dialCtx, addr)
			cancel()
			if err == nil {
				break
			}
			t.Logf("poll %d: SSH not ready yet: %v", seq, err)
			time.Sleep(10 * time.Second)
		}
		require.NotNil(t, client, "gosshd never answered on %s: %v", addr, err)
		defer client.Close()
		t.Logf("gosshd answered on %s", addr)

		stdout, _, code, err := client.Run(context.Background(), "echo WINKIT_BASE_OK")
		require.NoError(t, err, "running command over SSH")
		assert.Equal(t, 0, code, "guest command exit code")
		assert.Contains(t, string(stdout), "WINKIT_BASE_OK",
			"guest must execute commands over SSH")

		stdout, _, code, err = client.Run(context.Background(), "driverquery")
		if err == nil && code == 0 {
			t.Logf("driverquery output:\n%s", string(stdout))
			assert.Contains(t, strings.ToLower(string(stdout)), "netkvm",
				"netkvm must be loaded")
		} else {
			t.Logf("driverquery unavailable (err=%v code=%d) — SSH itself already proves netkvm", err, code)
		}
	})

	// interactive: boot the prebuilt image and hold the SSH session open until
	// the test times out. Use from the LLM session to test changes via SSH:
	//
	//	go test -tags 'wimlib integration' -run TestQcowBuilder/interactive -timeout 60m -v ./winpe/qemu/
	t.Run("interactive", func(t *testing.T) {
		// WINKIT_INTERACTIVE_IMAGE overrides which prebuilt qcow2 to boot;
		// defaults to the base image.
		prebuiltImg := filepath.Join("..", "..", "test", "testdata", "winpe-base.qcow2")
		if p := os.Getenv("WINKIT_INTERACTIVE_IMAGE"); p != "" {
			prebuiltImg = p
		}
		if _, err := os.Stat(prebuiltImg); err != nil {
			t.Skipf("%s not found", prebuiltImg)
		}
		t.Logf("interactive image: %s", prebuiltImg)

		outDir := testutil.ResultDir(t)
		t.Logf("results: %s", outDir)

		diskPath := filepath.Join(outDir, "scratch.qcow2")
		require.NoError(t, CreateDisk(diskPath, 8))
		fwPath := FirmwarePath()
		require.NotEmpty(t, fwPath, "no UEFI firmware found")
		varsPath := filepath.Join(outDir, "vars.fd")
		require.NoError(t, PrepareVarsFile(fwPath, varsPath))

		sshPort := uint16(20022)
		// WINKIT_INTERACTIVE_MEM_GB overrides guest RAM (default 2). An empty
		// child VM needs headroom for the root + child partition, so raise it
		// (e.g. 6) when driving an HCS/VM-start probe. Note: >3G may spill into
		// the ARM virt highmem window the prebuilt edk2 can't map — watch for an
		// early UEFI crash (no gosshd) and fall back if so.
		memGB := 2
		if v := os.Getenv("WINKIT_INTERACTIVE_MEM_GB"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				memGB = n
			}
		}
		spec := Spec{
			VMName:                 "winkit-interactive",
			CPUs:                   2,
			MemoryGB:               uint64(memGB),
			DiskPath:               diskPath,
			FirmwarePath:           fwPath,
			VarsPath:               varsPath,
			QMPSocketDir:           outDir,
			DisplayType:            "none",
			Accel:                  "tcg,thread=multi",
			SerialLogPath:          filepath.Join(outDir, "serial.log"),
			GuestStructuredLogPath: filepath.Join(outDir, "build.jsonl"),
			NoReboot:               true,
			SSHPort:                sshPort,
		}
		spec.ApplyDefaults()
		require.NoError(t, spec.Validate())

		argv := BuildQcowBootArgv(spec, prebuiltImg)
		argv[0] = qemuBin
		require.NoError(t, EnsureScreenshotDir(outDir, ScreenSourceQMP))

		cmd := exec.Command(argv[0], argv[1:]...)
		require.NoError(t, cmd.Start(), "starting QEMU")
		qemuDone := make(chan struct{})
		go func() { cmd.Wait(); close(qemuDone) }()
		defer func() {
			QMPQuit(QMPSocketPath(spec))
			select {
			case <-qemuDone:
			case <-time.After(15 * time.Second):
				cmd.Process.Kill()
				<-qemuDone
			}
		}()

		// Wait for SSH to come up, screenshotting for evidence. An
		// overridden image may boot much slower under TCG, so give it a
		// longer window.
		sshWait := 12 * time.Minute
		if os.Getenv("WINKIT_INTERACTIVE_IMAGE") != "" {
			sshWait = 25 * time.Minute
		}
		addr := fmt.Sprintf("127.0.0.1:%d", sshPort)
		deadline := time.Now().Add(sshWait)
		var client *gosshd.Client
		var err error
		seq := 0
		for time.Now().Before(deadline) {
			select {
			case <-qemuDone:
				serialLog, _ := os.ReadFile(filepath.Join(outDir, "serial.log"))
				t.Fatalf("QEMU exited before gosshd answered\nserial.log:\n%s", string(serialLog))
			default:
			}
			seq++
			ppm := ScreenshotPath(outDir, ScreenSourceQMP, time.Now(), "none", seq, seq, "ppm")
			QMPScreendump(QMPSocketPath(spec), ppm)

			dialCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			client, err = gosshd.Dial(dialCtx, addr)
			cancel()
			if err == nil {
				break
			}
			t.Logf("poll %d: SSH not ready yet: %v", seq, err)
			time.Sleep(10 * time.Second)
		}
		require.NotNil(t, client, "gosshd never answered on %s: %v", addr, err)
		client.Close()

		t.Logf("======================================")
		t.Logf("  INTERACTIVE SESSION READY")
		t.Logf("  SSH: ssh -p %d -o StrictHostKeyChecking=no root@127.0.0.1", sshPort)
		t.Logf("  gosshd address: %s", addr)
		t.Logf("  QMP socket: %s", QMPSocketPath(spec))
		t.Logf("  results: %s", outDir)
		t.Logf("  Kill with: go test -timeout or Ctrl-C")
		t.Logf("======================================")

		// Keep evidence flowing for the whole session: a screenshot and a
		// session.jsonl heartbeat (with an SSH liveness check) every 15s,
		// until the test is canceled or QEMU exits.
		sessionLog, err := os.OpenFile(filepath.Join(outDir, "session.jsonl"),
			os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		require.NoError(t, err, "opening session.jsonl")
		defer sessionLog.Close()

		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-qemuDone:
				t.Log("QEMU exited")
				return
			case <-ticker.C:
				seq++
				now := time.Now()
				ppm := ScreenshotPath(outDir, ScreenSourceQMP, now, "none", seq, seq, "ppm")
				shot := ppm
				shotErr := QMPScreendump(QMPSocketPath(spec), ppm)
				if shotErr == nil {
					png := ppm[:len(ppm)-3] + "png"
					if ConvertPPMtoPNG(ppm, png) == nil {
						os.Remove(ppm)
						shot = png
					}
				}

				dialCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				hb, hbErr := gosshd.Dial(dialCtx, addr)
				sshOK := hbErr == nil
				if sshOK {
					_, _, code, runErr := hb.Run(dialCtx, "echo HEARTBEAT_OK")
					sshOK = runErr == nil && code == 0
					hb.Close()
				}
				cancel()

				entry := map[string]any{
					"ts":     now.UTC().Format(time.RFC3339),
					"seq":    seq,
					"ssh_ok": sshOK,
				}
				if shotErr == nil {
					entry["screenshot"] = filepath.Base(shot)
				} else {
					entry["screenshot_error"] = shotErr.Error()
				}
				if hbErr != nil {
					entry["ssh_error"] = hbErr.Error()
				}
				line, _ := json.Marshal(entry)
				sessionLog.Write(append(line, '\n'))
			}
		}
	})

	winISO := requireWindowsISO(t)
	virtioISO := requireVirtioISO(t)
	gosshdExe := requireGosshdExe(t)
	pwshFiles := requirePwshFiles(t)

	t.Run("base", func(t *testing.T) {
		outDir := testutil.ResultDir(t)
		t.Logf("results: %s", outDir)

		// 1. Build the base image files and pack the bootable qcow2.
		files, err := winpe.BuildBaseImageFiles(winpe.BaseImageConfig{
			WindowsISO: winISO,
			VirtIOISO:  virtioISO,
			GosshdExe:  gosshdExe,
			PwshFiles:  pwshFiles,
			WorkDir:    t.TempDir(),
		})
		require.NoError(t, err, "building base image files")
		for _, path := range []string{
			"/sources/boot.wim", "/EFI/BOOT/BOOTAA64.EFI",
			"/EFI/Microsoft/Boot/BCD", "/boot/boot.sdi",
		} {
			require.Contains(t, files, path, "base image must be a complete boot disk")
		}

		img := filepath.Join(outDir, "winpe-base.qcow2")
		require.NoError(t, CreateFATQcow2(img, files, 4*1024*1024*1024))
		files = nil

		// 2. Boot it.
		diskPath := filepath.Join(outDir, "scratch.qcow2")
		require.NoError(t, CreateDisk(diskPath, 8))
		fwPath := FirmwarePath()
		require.NotEmpty(t, fwPath, "no UEFI firmware found")
		varsPath := filepath.Join(outDir, "vars.fd")
		require.NoError(t, PrepareVarsFile(fwPath, varsPath))

		sshPort := freeTCPPort(t)
		spec := Spec{
			VMName:                 "winkit-qcow-base-test",
			CPUs:                   2,
			MemoryGB:               2,
			DiskPath:               diskPath,
			FirmwarePath:           fwPath,
			VarsPath:               varsPath,
			QMPSocketDir:           outDir,
			DisplayType:            "none",
			Accel:                  "tcg,thread=multi",
			SerialLogPath:          filepath.Join(outDir, "serial.log"),
			GuestStructuredLogPath: filepath.Join(outDir, "build.jsonl"),
			NoReboot:               true,
			SSHPort:                sshPort,
		}
		spec.ApplyDefaults()
		require.NoError(t, spec.Validate())

		argv := BuildQcowBootArgv(spec, img)
		argv[0] = qemuBin
		require.NoError(t, EnsureScreenshotDir(outDir, ScreenSourceQMP))

		cmd := exec.Command(argv[0], argv[1:]...)
		require.NoError(t, cmd.Start(), "starting QEMU")
		qemuDone := make(chan struct{})
		go func() { cmd.Wait(); close(qemuDone) }()
		defer func() {
			QMPQuit(QMPSocketPath(spec))
			select {
			case <-qemuDone:
			case <-time.After(15 * time.Second):
				cmd.Process.Kill()
				<-qemuDone
			}
		}()

		// 3. Poll SSH until gosshd answers, screenshotting for evidence.
		addr := fmt.Sprintf("127.0.0.1:%d", sshPort)
		deadline := time.Now().Add(12 * time.Minute)
		var client *gosshd.Client
		seq := 0
		for time.Now().Before(deadline) {
			select {
			case <-qemuDone:
				t.Fatal("QEMU exited before gosshd answered")
			default:
			}
			seq++
			ppm := ScreenshotPath(outDir, ScreenSourceQMP, time.Now(), "none", seq, seq, "ppm")
			QMPScreendump(QMPSocketPath(spec), ppm)

			dialCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			client, err = gosshd.Dial(dialCtx, addr)
			cancel()
			if err == nil {
				break
			}
			time.Sleep(10 * time.Second)
		}
		require.NotNil(t, client, "gosshd never answered on %s: %v", addr, err)
		defer client.Close()
		t.Logf("gosshd answered on %s", addr)

		// 4. Run a command in the guest over SSH.
		stdout, _, code, err := client.Run(context.Background(), "echo WINKIT_BASE_OK")
		require.NoError(t, err, "running command over SSH")
		assert.Equal(t, 0, code, "guest command exit code")
		assert.Contains(t, string(stdout), "WINKIT_BASE_OK",
			"guest must execute commands over SSH")

		// netkvm must actually be loaded — SSH arriving over virtio-net
		// already proves it, but the driver list makes it explicit. The
		// storage drivers are shipped and drvloaded yet bind no device in
		// this topology (NVMe disk, USB volume), so they are not asserted.
		stdout, _, code, err = client.Run(context.Background(), "driverquery")
		if err == nil && code == 0 {
			assert.Contains(t, strings.ToLower(string(stdout)), "netkvm",
				"netkvm must be loaded")
		} else {
			t.Logf("driverquery unavailable (err=%v code=%d) — SSH itself already proves netkvm", err, code)
		}
	})
}

func freeTCPPort(t *testing.T) uint16 {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()
	return uint16(l.Addr().(*net.TCPAddr).Port)
}

// Test helpers: resolve prerequisites, skip when missing.

func requireQEMUBin(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"qemu-system-aarch64", "qemu-system-x86_64"} {
		if p, err := LookPath(name); err == nil {
			return p
		}
	}
	t.Skip("no QEMU binary found")
	return ""
}

// The ISOs come from the cache `winkit fetch` seeds; see `task test:seed`.
// Set WINKIT_CACHE_DIR to run against a copy elsewhere.

func requireWindowsISO(t *testing.T) string {
	t.Helper()
	p := cache.WindowsISO()
	if _, err := os.Stat(p); err != nil {
		t.Skipf("Windows ISO not available at %s (run: task test:seed)", p)
	}
	return p
}

func requireVirtioISO(t *testing.T) string {
	t.Helper()
	p := cache.VirtIOISO()
	if _, err := os.Stat(p); err != nil {
		t.Skipf("virtio-win ISO not available at %s (run: task test:seed)", p)
	}
	return p
}

// requireGosshdExe cross-compiles gosshd for windows/arm64 once per test
// binary and returns its path.
func requireGosshdExe(t *testing.T) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "gosshd.exe")
	require.NoError(t, winpe.CrossCompileGosshd(dst, "arm64"),
		"cross-compiling gosshd for windows/arm64")
	return dst
}

func requirePwshFiles(t *testing.T) map[string][]byte {
	t.Helper()

	if p := os.Getenv("WINKIT_TEST_PWSH_ZIP"); p != "" {
		files, err := winpe.ExtractPwshFiles(p)
		require.NoError(t, err, "extracting pwsh files from %s", p)
		require.NotEmpty(t, files, "pwsh zip contained no files")
		return files
	}

	files, err := winpe.FetchPwshFiles(cache.Dir(), t.Logf)
	if err != nil {
		t.Skipf("could not obtain pwsh: %v", err)
	}
	require.NotEmpty(t, files, "pwsh zip contained no files")
	return files
}

// LookPath finds a QEMU binary, checking nix profile paths first.
func LookPath(name string) (string, error) {
	candidates := []string{
		filepath.Join(os.Getenv("HOME"), ".local/state/nix/profiles/profile/bin", name),
		filepath.Join("/opt/winkit/.local/state/nix/profiles/profile/bin", name),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	// Fall back to PATH.
	for _, dir := range strings.Split(os.Getenv("PATH"), string(os.PathListSeparator)) {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", os.ErrNotExist
}
