//go:build darwin

package vz

import (
	"bytes"
	"context"
	"fmt"
	"image/png"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/devcell-sh/go-winkit/media/isokit"
	"github.com/devcell-sh/go-winkit/vm"
)

// TestVZBootProbe is the minimal-variable control experiment for the vz
// backend: boot ONE known-good disk image (no Windows artifacts, no Setup
// boot volume, no ISOs) through the exact StartRun path and report whether
// the guest executes. Proven 2026-09-02: Debian nocloud arm64 boots to a
// login prompt in <15s, so the vz wiring (EFI, NVMe, virtio-gpu, VNC) is
// sound.
//
// Run via `task debug:vz:bootprobe`. Knobs:
//
//	WINKIT_VZ_PROBE_MEDIA  - bootable raw arm64 disk image (required)
//	WINKIT_VZ_PROBE_SECS   - how long to probe (default 120)
//	WINKIT_VZ_PROBE_OUTDIR - artifact dir (default t.TempDir())
func TestVZBootProbe(t *testing.T) {
	media := os.Getenv("WINKIT_VZ_PROBE_MEDIA")
	if media == "" {
		t.Skip("set WINKIT_VZ_PROBE_MEDIA to a bootable raw arm64 disk image (e.g. Debian nocloud)")
	}
	outDir := probeOutDir(t)

	// Writable copy so the guest can mutate its disk.
	disk := filepath.Join(outDir, "probe.raw")
	if err := probeCopyFile(media, disk); err != nil {
		t.Fatalf("copying media: %v", err)
	}

	if !bootAndWatch(t, disk, outDir) {
		t.Errorf("VERDICT: guest never drew on the display with a known-good Linux image — the vz VM wiring itself is broken")
	} else {
		t.Logf("VERDICT: guest EXECUTES — vz wiring is sound")
	}
}

// TestVZBootProbeCrafted isolates OUR image pipeline from the payload: it
// extracts the known-good Debian ESP (grub + config) out of the probe media,
// repacks it into a GPT+ESP image built by isokit.CreateGPTFATImageSized —
// the exact function that builds the Windows Setup boot volume — and boots
// that. Outcomes:
//
//   - Screen shows grub (menu or rescue prompt) => our GPT/FAT construction
//     is EFI-bootable; the Windows bootmgfw payload is the isolated blocker.
//   - Screen stays black => Apple's EFI rejects OUR image format; the
//     Windows payload was never the variable.
func TestVZBootProbeCrafted(t *testing.T) {
	media := os.Getenv("WINKIT_VZ_PROBE_MEDIA")
	if media == "" {
		t.Skip("set WINKIT_VZ_PROBE_MEDIA to a bootable raw arm64 disk image (e.g. Debian nocloud)")
	}
	outDir := probeOutDir(t)

	files := loadESPDir(t)
	t.Logf("loaded %d files from the extracted Debian ESP", len(files))
	for p := range files {
		t.Logf("  esp file: %s (%d bytes)", p, len(files[p]))
	}

	crafted := filepath.Join(outDir, "crafted.img")
	if err := isokit.CreateGPTFATImageSized(crafted, files, 256*1024*1024); err != nil {
		t.Fatalf("building crafted GPT image: %v", err)
	}

	if !bootAndWatch(t, crafted, outDir) {
		t.Errorf("VERDICT: EFI does not boot an isokit-built GPT+ESP image even with a known-good grub payload — our image construction is the blocker")
	} else {
		t.Logf("VERDICT: isokit-built image BOOTS with a grub payload — image format is fine, the Windows bootmgfw payload is the isolated blocker")
	}
}

// bootAndWatch boots disk through the backend's StartRun path and watches
// the display via VNC. Returns true when the guest drew anything meaningful.
// Guest CPU is logged for reference but NOT used as a verdict signal:
// Virtualization.framework vCPU time does not show up in RUSAGE_SELF.
func bootAndWatch(t *testing.T, disk, outDir string) bool {
	secs := 120
	if s := os.Getenv("WINKIT_VZ_PROBE_SECS"); s != "" {
		fmt.Sscanf(s, "%d", &secs)
	}
	shotDir := filepath.Join(outDir, "screenshots")
	_ = os.MkdirAll(shotDir, 0o755)

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	b := &Backend{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	vm, err := b.StartRun(ctx, vm.VMRunConfig{
		DiskPath:  disk,
		OutputDir: outDir,
		CPUs:      2,
		MemoryGB:  2,
		BackendExtra: &RunOptions{
			VNCPort: DefaultVNCPort,
			Logger:  logger,
		},
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	defer vm.Stop()

	deadline := time.Now().Add(time.Duration(secs) * time.Second)
	for i := 1; time.Now().Before(deadline); i++ {
		time.Sleep(15 * time.Second)
		data, grabErr := VNCGrabFrame(fmt.Sprintf("127.0.0.1:%d", DefaultVNCPort), 5*time.Second)
		if grabErr != nil {
			t.Logf("probe %d: grab failed: %v", i, grabErr)
			continue
		}
		nonBlack, frameInfo := probeFrameStats(data)
		_ = os.WriteFile(filepath.Join(shotDir, fmt.Sprintf("probe-%02d.png", i)), data, 0o644)
		t.Logf("probe %d: frame=%s", i, frameInfo)
		if nonBlack > 500 {
			return true
		}
	}
	return false
}

func probeOutDir(t *testing.T) string {
	outDir := os.Getenv("WINKIT_VZ_PROBE_OUTDIR")
	if outDir == "" {
		outDir = t.TempDir()
	} else {
		// Separate subdir per test so two probes in one task run don't clobber
		// each other's frames.
		outDir = filepath.Join(outDir, t.Name())
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("mkdir outDir: %v", err)
	}
	return outDir
}

// loadESPDir reads a pre-extracted Debian ESP tree (shim/grub) into a
// path->content map suitable for isokit.CreateGPTFATImageSized. The Debian
// cloud image's ESP is FAT16, which go-diskfs cannot read, so the files are
// extracted once (7z/hdiutil) into WINKIT_VZ_PROBE_ESPDIR — default
// ~/.cache/winkit/debian-esp, populated by `task debug:vz:bootprobe:crafted`.
func loadESPDir(t *testing.T) map[string][]byte {
	dir := os.Getenv("WINKIT_VZ_PROBE_ESPDIR")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".cache", "winkit", "debian-esp")
	}
	files := make(map[string][]byte)
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		files["/"+filepath.ToSlash(rel)] = data
		return nil
	})
	if err != nil {
		t.Fatalf("walking ESP dir %s: %v", dir, err)
	}
	if len(files) == 0 {
		t.Fatalf("no files in ESP dir %s — extract the Debian ESP first (task debug:vz:bootprobe:crafted does this)", dir)
	}
	return files
}

func probeCopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func probeFrameStats(pngData []byte) (int, string) {
	img, err := png.Decode(bytes.NewReader(pngData))
	if err != nil {
		return 0, fmt.Sprintf("decode error: %v", err)
	}
	bo := img.Bounds()
	nonBlack := 0
	for y := bo.Min.Y; y < bo.Max.Y; y++ {
		for x := bo.Min.X; x < bo.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			if r|g|bl != 0 {
				nonBlack++
			}
		}
	}
	return nonBlack, fmt.Sprintf("%dx%d nonBlack=%d", bo.Dx(), bo.Dy(), nonBlack)
}
