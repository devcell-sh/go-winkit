package qemu

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// CaptureScreenshots polls the VM's QMP socket and writes a PNG every
// 30s until stop is closed, so a run can be inspected visually after the
// fact. Failures are logged, never fatal: a missing frame must not fail
// the build.
func CaptureScreenshots(qmpSock, shotDir string, stop <-chan struct{}, done chan<- struct{}, logf func(format string, args ...any)) {
	defer close(done)
	seq := 0
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	shoot := func() {
		if _, err := os.Stat(qmpSock); err != nil {
			return // VM/socket not up yet
		}
		seq++
		ppm := filepath.Join(shotDir, fmt.Sprintf("shot-%04d.ppm", seq))
		if err := QMPScreendump(qmpSock, ppm); err != nil {
			logf("screendump %d: %v", seq, err)
			return
		}
		png := ppm[:len(ppm)-3] + "png"
		if err := ConvertPPMtoPNG(ppm, png); err == nil {
			_ = os.Remove(ppm) // keep only the PNG
		}
	}
	for {
		select {
		case <-stop:
			shoot() // final frame
			return
		case <-tick.C:
			shoot()
		}
	}
}
