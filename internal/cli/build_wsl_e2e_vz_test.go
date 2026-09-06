//go:build integration && darwin_vz

package cli

// vz-backend screenshot capture for the e2e test. Behind the darwin_vz tag
// with the rest of the vz support: it drives the vz VNC server, which only
// exists when the vz backend is compiled in.

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/devcell-sh/go-winkit/winpe/vz"
)

func vncPort() uint16 {
	if s := os.Getenv("WINKIT_E2E_VNC_PORT"); s != "" {
		if p, err := strconv.Atoi(s); err == nil && p > 0 && p < 65536 {
			return uint16(p)
		}
	}
	return vz.DefaultVNCPort
}

// captureVZScreenshots polls the vz VNC server and writes a PNG every 30s
// (first attempt after 10s). Every RFB protocol step is traced to
// <outDir>/vnc-debug.log AND stderr with immediate, unbuffered writes: the
// builtin Virtualization.framework VNC backend has SIGTRAP'd the whole test
// process on client connect, and t.Logf output does not survive that. The
// last trace line before a crash localizes the killing message.
func captureVZScreenshots(shotDir string, stop <-chan struct{}, done chan<- struct{}, t *testing.T) {
	defer close(done)

	logPath := filepath.Join(filepath.Dir(shotDir), "vnc-debug.log")
	var sink io.Writer = os.Stderr
	if logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		defer logFile.Close()
		sink = io.MultiWriter(logFile, os.Stderr)
	} else {
		t.Logf("vnc-debug.log unavailable (%v), tracing to stderr only", err)
	}
	logf := func(format string, args ...any) {
		fmt.Fprintf(sink, "%s vnc: %s\n", time.Now().Format("15:04:05.000"), fmt.Sprintf(format, args...))
	}

	addr := fmt.Sprintf("127.0.0.1:%d", vncPort())
	logf("screenshot loop starting: addr=%s first=10s interval=30s trace=%s", addr, logPath)
	logf("eyeball live view: open vnc://%s (macOS Screen Sharing)", addr)

	// Guest-progress probes, one line each per cycle. They decide between the
	// competing black-screen explanations:
	//  - disk allocated blocks growing => guest IS running (Setup partitions
	//    and writes within minutes), display path is the problem.
	//  - disk flat + process CPU near-idle => guest never boots (EFI is not
	//    booting the USB boot volume).
	//  - nvram.vars hash changing => EFI is at least writing boot variables.
	outDir := filepath.Dir(shotDir)
	nvramPath := filepath.Join(outDir, "work", "install", "nvram.vars")
	probe := func() {
		disks, _ := filepath.Glob(filepath.Join(outDir, "wsl.*"))
		for _, d := range disks {
			logf("probe: disk %s size=%dKB allocated=%dKB", filepath.Base(d), fileSizeKB(d), fileAllocatedKB(d))
		}
		if data, err := os.ReadFile(nvramPath); err == nil {
			sum := sha256.Sum256(data)
			logf("probe: nvram.vars hash=%x", sum[:6])
		} else {
			logf("probe: nvram.vars unreadable: %v", err)
		}
		var ru syscall.Rusage
		if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err == nil {
			// vz vCPU threads run inside this process: sustained CPU growth
			// far above wall-clock idle means the guest is executing code.
			logf("probe: process cpu user=%.1fs sys=%.1fs", time.Duration(ru.Utime.Nano()).Seconds(), time.Duration(ru.Stime.Nano()).Seconds())
		}
	}

	seq := 0
	shoot := func() {
		seq++
		start := time.Now()
		probe()
		logf("shot %d: connecting to %s", seq, addr)
		data, err := vz.VNCGrabFrameTrace(addr, 5*time.Second, func(format string, args ...any) {
			logf("shot %d: rfb: %s", seq, fmt.Sprintf(format, args...))
		})
		if err != nil {
			logf("shot %d: FAILED after %s: %v", seq, time.Since(start).Round(time.Millisecond), err)
			return
		}
		// Frame fingerprint: identical hashes across shots => the scanout
		// never refreshes; growing nonBlack => guest is actually drawing.
		logf("shot %d: frame %s", seq, frameStats(data))
		path := filepath.Join(shotDir, fmt.Sprintf("shot-%04d.png", seq))
		if err := os.WriteFile(path, data, 0o644); err != nil {
			logf("shot %d: write failed: %v", seq, err)
			return
		}
		logf("shot %d: OK, %d bytes -> %s (%s)", seq, len(data), path, time.Since(start).Round(time.Millisecond))
	}

	first := time.NewTimer(10 * time.Second)
	defer first.Stop()
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			shoot()
			logf("screenshot loop stopped after %d attempts", seq)
			return
		case <-first.C:
			shoot()
		case <-tick.C:
			shoot()
		}
	}
}
