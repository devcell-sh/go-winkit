//go:build integration

package build

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"image/png"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/devcell-sh/go-winkit/cache"
	"github.com/devcell-sh/go-winkit/internal/testutil"
	"github.com/devcell-sh/go-winkit/vm/qemu"
	"github.com/devcell-sh/go-winkit/winpe"
)

// TestQcowBuilderWSL drives the full `winkit build --stage=wsl` path as a
// test: the same call chain as build.go's RunE, but with assertions
// at each phase. It is opt-in and long-running, behind the `integration`
// build tag AND WINKIT_E2E=1, and requires the Windows + virtio ISOs already
// cached (it never downloads multi-GB media inside the test).
//
// Accelerator: the build phase defaults to the best available accelerator
// (HVF on Mac, KVM on Linux, TCG fallback). Override with WINKIT_E2E_ACCEL.
// Continue mode (WINKIT_E2E_DISK) defaults to TCG (secure/EL3).
//
// Inspectable output: all artifacts persist under test/results/<ts>-<TestName>
// (testutil.ResultDir, the house convention; override with WINKIT_E2E_OUTDIR):
//   - build.jsonl       — unified structured log, namespaced by "source":
//     host slog events, guest structured events (merged from
//     work/guest.jsonl after the build), and live-tailed progress + serial
//     console lines
//   - work/install/serial.log, work/install/guest-progress.log
//   - screenshots/*.png — periodic QMP screendumps (converted from PPM)
//   - wsl.qcow2          — the produced disk
//
// Scope: install → SSH → SSH/RDP verify → clean shutdown, exactly what
// buildWSLImage does.
func TestQcowBuilderWSL(t *testing.T) {
	if os.Getenv("WINKIT_E2E") != "1" {
		t.Skip("set WINKIT_E2E=1 to run the multi-hour wsl install (needs cached ISOs)")
	}
	if !wimlibAvailable() {
		t.Skip("wimlib not compiled in; rebuild with -tags wimlib")
	}

	// --- ISO preflight ---
	// Continue mode (WINKIT_E2E_DISK) boots an already-installed disk, so it
	// needs no media; a full install requires both ISOs already cached.
	cacheDir := cache.Dir()
	cfg := cache.Config{Dir: cacheDir}
	continueMode := os.Getenv("WINKIT_E2E_DISK") != ""
	var winISO, virtioISO string
	if continueMode {
		virtioISO = cfg.VirtIOISO() // attached if present; drivers already installed
		t.Logf("continue mode: booting WINKIT_E2E_DISK=%s (no install media required)", os.Getenv("WINKIT_E2E_DISK"))
	} else {
		virtioISO = cfg.VirtIOISO()
		if _, err := os.Stat(virtioISO); err != nil {
			t.Skipf("virtio-win ISO not cached at %s — run a `winkit fetch` first", virtioISO)
		}
		winISOs, _ := filepath.Glob(filepath.Join(cacheDir, "windows-*.iso"))
		if len(winISOs) == 0 {
			t.Skipf("no Windows ISO cached in %s — run a `winkit fetch` first", cacheDir)
		}
		winISO = winISOs[0]
		t.Logf("using cached ISOs: windows=%s virtio=%s", winISO, virtioISO)
	}

	// --- persistent, inspectable output tree ---
	// Default to the house convention (test/results/<ts>-<TestName>, via
	// testutil.ResultDir) like every other test; WINKIT_E2E_OUTDIR overrides.
	outDir := os.Getenv("WINKIT_E2E_OUTDIR")
	if outDir == "" {
		outDir = testutil.ResultDir(t)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("mkdir outDir: %v", err)
	}
	workDir := filepath.Join(outDir, "work")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("mkdir workDir: %v", err)
	}
	// Backend + disk naming must mirror buildWSLImage's own resolution
	// (qemu by default everywhere — the vz-on-darwin platform default does
	// not apply to Windows installs). The disk FORMAT comes from the
	// backend's DiskFormatDefault; this only names the file to match.
	_, backendName, err := ResolveBackend()
	if err != nil {
		t.Fatalf("resolving backend: %v", err)
	}
	diskExt := ".qcow2"
	if backendName == "vz" {
		diskExt = ".raw"
	}
	dest := filepath.Join(outDir, "wsl"+diskExt)
	t.Logf("artifacts (persist after test): %s", outDir)

	// --- logger → unified build.jsonl + human-readable stderr under -v ---
	// Host events stream in live; the guest's raw event stream (QEMU
	// chardev, work/guest.jsonl) is appended after the build (MergeGuestLog).
	logFile, err := os.Create(filepath.Join(outDir, "build.jsonl"))
	if err != nil {
		t.Fatalf("create log: %v", err)
	}
	defer logFile.Close()
	fileHandler := winpe.NewGuestEventHandler(logFile)
	var handler slog.Handler = fileHandler
	if testing.Verbose() {
		handler = winpe.MultiHandler(
			fileHandler,
			slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}),
		)
	}
	logger := slog.New(handler)

	// --- periodic screenshots via QMP (QEMU) or VNC (vz) ---
	shotDir := filepath.Join(outDir, "screenshots")
	_ = os.MkdirAll(shotDir, 0o755)
	stopShots := make(chan struct{})
	shotsDone := make(chan struct{})
	if backendName == "qemu" {
		installOut := filepath.Join(workDir, "install")
		qmpSock := qemu.QMPSocketPath(qemu.Spec{VMName: "winkit-install", QMPSocketDir: installOut})
		go qemu.CaptureScreenshots(qmpSock, shotDir, stopShots, shotsDone, t.Logf)
	} else if backendName == "vz" {
		go captureVZScreenshots(shotDir, stopShots, shotsDone, t)
	} else {
		t.Logf("screenshots: not available on %s backend", backendName)
		close(shotsDone)
	}

	// --- shared-directory round trip (qemu backend) ---
	// Share a dir carrying a host sentinel; the guest mounts it as a fixed
	// disk (WinFsp + rclone over SFTP) and writes a marker back through the
	// share. The SFTP server itself is started inside buildWSLImage /
	// continueWSLImage before the VM boots and closed by defer only when the
	// run (including the interactive wait) is done, so the guest's boot-time
	// mount always has a host to talk to.
	// Share the project root with the guest so the WSL1 DirShare symlink
	// resolves to the actual source tree, not a scratch subdir.
	sharedDir := ""
	if backendName == "qemu" &&
		os.Getenv("WINKIT_SHARED_DIR") == "" && os.Getenv("WINKIT_E2E_SHARED_DIR") == "" {
		// Project root is one level up from build/
		projectRoot, err := filepath.Abs("..")
		if err != nil {
			t.Fatalf("resolving project root: %v", err)
		}
		sharedDir = projectRoot
		t.Setenv("WINKIT_SHARED_DIR", sharedDir)
		t.Logf("sharing project root %s with the guest over SFTP", sharedDir)
	}

	// Generous ceiling: a TCG install + feature enable can run for hours. The
	// buildWSLImage internal SSH wait (wslInstallWait) is the real budget;
	// this is a hard backstop so a wedged VM cannot hang the suite forever.
	ctx, cancel := context.WithTimeout(context.Background(), wslInstallWait+30*time.Minute)
	defer cancel()

	// --- the call under test: identical to build.go's wsl branch ---
	// buildWSLImage internally asserts every phase and returns an error if any
	// fails: waitForWindowsSSH (whoami), wslVerify (SSH + RDP port reachable).
	// A nil return therefore means all of those passed.
	buildErr := wslImage(ctx, dest, cacheDir, winISO, virtioISO, workDir, logger, false, "", os.Getenv("WINKIT_E2E_WSL_IMAGE"), ResolveNixHome(""), nil, "")
	close(stopShots)
	<-shotsDone

	if err := MergeGuestLog(logFile, workDir); err != nil {
		t.Logf("merging guest.jsonl into build.jsonl: %v", err)
	}

	shots, _ := filepath.Glob(filepath.Join(shotDir, "*.png"))
	t.Logf("captured %d screenshots in %s", len(shots), shotDir)
	if buildErr != nil {
		t.Fatalf("buildWSLImage: %v (inspect %s: build.jsonl, work/install/serial.log, screenshots/)", buildErr, outDir)
	}

	// --- post-conditions on the produced artifact ---
	// Full install: dest is the produced disk. Continue mode: dest is a small
	// COW overlay; the deliverable is the flattened wsl-baked.qcow2.
	artifact := dest
	if continueMode {
		artifact = filepath.Join(outDir, "wsl-baked"+diskExt)
	}
	fi, err := os.Stat(artifact)
	if err != nil {
		t.Fatalf("produced artifact missing: %v", err)
	}
	// A real install/baked image writes many GB; anything near the empty-qcow2
	// floor means the OS never landed even though the call returned.
	const minInstalledBytes = 4 * 1024 * 1024 * 1024
	if fi.Size() < minInstalledBytes {
		t.Fatalf("produced artifact too small (%d bytes) — install/bake likely did not complete", fi.Size())
	}
	if len(shots) == 0 && (backendName == "qemu" || backendName == "vz") {
		t.Errorf("no screenshots captured — %s screendump path is not working", backendName)
	}
	// The bootstrap writes this marker at first logon, so it only exists in
	// the share of a full install; continue mode verifies the mount live via
	// wslVerifySFTP instead.
	if sharedDir != "" && !continueMode {
		marker := filepath.Join(sharedDir, ".winkit", "sftp-mounted")
		if data, err := os.ReadFile(marker); err != nil {
			t.Errorf("guest never wrote the SFTP marker (%v) — the 'mount the host SFTP share as a fixed disk' bootstrap step failed; check %s", err, filepath.Join(outDir, "build.jsonl"))
		} else {
			t.Logf("SFTP fixed-disk round trip verified: %s", strings.TrimSpace(string(data)))
		}
	}
	t.Logf("artifact built: %.1f GB at %s", float64(fi.Size())/(1<<30), artifact)
}

// fileSizeKB returns the file's apparent size in KB, or -1.
func fileSizeKB(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return -1
	}
	return fi.Size() / 1024
}

// fileAllocatedKB returns the file's actually-allocated size in KB (sparse
// files report far less than their apparent size), or -1.
func fileAllocatedKB(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return -1
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return st.Blocks * 512 / 1024
	}
	return fi.Size() / 1024
}

// frameStats fingerprints a captured PNG frame: how many pixels are non-black,
// the brightest channel value seen, and a content hash for frame-to-frame
// comparison.
func frameStats(pngData []byte) string {
	img, err := png.Decode(bytes.NewReader(pngData))
	if err != nil {
		return fmt.Sprintf("decode error: %v", err)
	}
	b := img.Bounds()
	nonBlack := 0
	var maxChan uint32
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			v := r | g | bl
			if v != 0 {
				nonBlack++
			}
			if v > maxChan {
				maxChan = v
			}
		}
	}
	sum := sha256.Sum256(pngData)
	return fmt.Sprintf("%dx%d nonBlack=%d maxChan=%d hash=%x", b.Dx(), b.Dy(), nonBlack, maxChan>>8, sum[:6])
}
