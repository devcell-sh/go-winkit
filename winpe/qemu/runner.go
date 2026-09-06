package qemu

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/devcell-sh/go-winkit/winpe"
)

// Runner implements winpe.Runner for QEMU.
type Runner struct {
	QEMUBin string
	Accel   string
	CDBus   string
}

// NewRunner creates a QEMU runner. qemuBin may be empty (auto-detected).
func NewRunner(qemuBin, accel string) *Runner {
	return &Runner{QEMUBin: qemuBin, Accel: accel}
}

// Boot starts a WinPE VM and returns a Guest handle.
func (r *Runner) Boot(ctx context.Context, bs winpe.BootSpec) (winpe.Guest, error) {
	qemuBin := r.QEMUBin
	if qemuBin == "" {
		var err error
		qemuBin, err = QEMUBinaryPath()
		if err != nil {
			return nil, err
		}
	}

	outDir := bs.OutputDir
	if outDir == "" {
		var err error
		outDir, err = os.MkdirTemp("", "winkit-qemu-*")
		if err != nil {
			return nil, fmt.Errorf("creating output dir: %w", err)
		}
	}

	fwPath := FirmwarePath()
	if fwPath == "" {
		return nil, fmt.Errorf("no UEFI firmware found")
	}

	varsPath := filepath.Join(outDir, "vars.fd")
	if err := PrepareVarsFile(fwPath, varsPath); err != nil {
		return nil, fmt.Errorf("preparing vars: %w", err)
	}

	diskPath := filepath.Join(outDir, "scratch.qcow2")
	if err := CreateDisk(diskPath, 8); err != nil {
		return nil, fmt.Errorf("creating scratch disk: %w", err)
	}

	sharedImg := filepath.Join(outDir, "shared.qcow2")
	if err := CreateFATQcow2(sharedImg, bs.SharedFiles, 20*1024*1024*1024); err != nil {
		return nil, fmt.Errorf("creating shared volume: %w", err)
	}
	// The shared files (two boot.wim copies + pwsh, ~2GB) are on disk now.
	// Drop the buffers before QEMU starts competing for the same memory.
	bs.SharedFiles = nil

	cpus := uint(bs.CPUs)
	if cpus == 0 {
		cpus = 2
	}
	mem := uint64(bs.MemoryGB)
	if mem == 0 {
		mem = 5
	}

	serialLog := filepath.Join(outDir, "serial.log")
	progressLog := filepath.Join(outDir, "guest-progress.log")
	structuredLog := filepath.Join(outDir, "build.jsonl")

	spec := Spec{
		VMName:                 "winkit-wim-builder",
		CPUs:                   cpus,
		MemoryGB:               mem,
		DiskPath:               diskPath,
		FirmwarePath:           fwPath,
		VarsPath:               varsPath,
		QMPSocketDir:           outDir,
		DisplayType:            "none",
		NoReboot:               true,
		SerialLogPath:          serialLog,
		GuestProgressLogPath:   progressLog,
		GuestStructuredLogPath: structuredLog,
		CDBus:                  r.CDBus,
	}
	if r.Accel != "" {
		spec.Accel = r.Accel
	}
	spec.ApplyDefaults()

	wbs := WimBuilderSpec{
		Spec:       spec,
		WinPEISO:   bs.WinPEISO,
		SharedImg:  sharedImg,
		WindowsISO: bs.WindowsISO,
		VirtIOISO:  bs.VirtIOISO,
	}
	argv := BuildWimBuilderArgv(wbs)
	argv[0] = qemuBin

	screenshotDir := filepath.Join(outDir, "screenshots")
	os.MkdirAll(screenshotDir, 0o755)

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting QEMU: %w", err)
	}

	qmpSock := QMPSocketPath(spec)
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(qmpSock); err == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	g := &guest{
		cmd:           cmd,
		sharedImg:     sharedImg,
		qmpSock:       qmpSock,
		screenshotDir: screenshotDir,
		outDir:        outDir,
		progressLog:   progressLog,
		doneCh:        make(chan struct{}),
		fatalCh:       make(chan string, 1),
	}
	go func() {
		cmd.Wait()
		close(g.doneCh)
	}()

	// Fatal-state serial watchers: a firmware synchronous exception ASSERTs
	// and hangs forever, and an EFI shell drop means nothing bootable was
	// found. Both would otherwise burn the whole run timeout.
	syncExCh := WatchSerialForSyncException(serialLog, g.doneCh)
	efiShellCh := WatchSerialForEFIShell(serialLog, g.doneCh)
	go func() {
		select {
		case reason := <-syncExCh:
			g.fatalCh <- "firmware synchronous exception: " + reason
		case reason := <-efiShellCh:
			g.fatalCh <- "firmware dropped to EFI shell: " + reason
		case <-g.doneCh:
		}
	}()

	return g, nil
}

type guest struct {
	cmd           *exec.Cmd
	sharedImg     string
	qmpSock       string
	screenshotDir string
	outDir        string
	doneCh        chan struct{}
	fatalCh       chan string
	progressLog   string
	screenshotSeq int
}

// FatalBoot reports unrecoverable boot states seen on the serial console.
func (g *guest) FatalBoot() <-chan string {
	return g.fatalCh
}

// StructuredLogPath is where QEMU writes the guest's build.jsonl stream.
func (g *guest) StructuredLogPath() string {
	return filepath.Join(g.outDir, "build.jsonl")
}

// ProgressContains reports whether the streamed guest progress log carries
// the token. The guest's FAT writes only reach the shared image after
// shutdown, so this stream is the only live completion signal.
func (g *guest) ProgressContains(token string) bool {
	data, err := os.ReadFile(g.progressLog)
	return err == nil && strings.Contains(string(data), token)
}

// Shutdown quits QEMU gracefully via QMP so writeback caches flush and the
// shared qcow2 is consistent, falling back to Kill if QMP is unreachable.
func (g *guest) Shutdown() error {
	if err := QMPQuit(g.qmpSock); err != nil && g.cmd.Process != nil {
		g.cmd.Process.Kill()
	}
	select {
	case <-g.doneCh:
	case <-time.After(30 * time.Second):
		if g.cmd.Process != nil {
			g.cmd.Process.Kill()
			<-g.doneCh
		}
	}
	return nil
}

func (g *guest) ReadSharedFile(path string) ([]byte, error) {
	path = "/" + strings.TrimPrefix(path, "/")
	data, err := ReadFileFromFATQcow2(g.sharedImg, path)
	if err != nil {
		return nil, &winpe.SharedFileNotFoundError{Path: path}
	}
	return data, nil
}

func (g *guest) Stop() error {
	if g.cmd.Process != nil {
		g.cmd.Process.Kill()
		<-g.doneCh
	}
	return nil
}

func (g *guest) ScreenshotDir() string {
	return g.screenshotDir
}

func (g *guest) Done() <-chan struct{} {
	return g.doneCh
}

func (g *guest) TakeScreenshot() (string, error) {
	g.screenshotSeq++
	ppmPath := ScreenshotPath(g.outDir, ScreenSourceQMP, time.Now(),
		"none", g.screenshotSeq, g.screenshotSeq, "ppm")
	if err := EnsureScreenshotDir(g.outDir, ScreenSourceQMP); err != nil {
		return "", err
	}
	if err := QMPScreendump(g.qmpSock, ppmPath); err != nil {
		return "", err
	}
	pngPath := ppmPath[:len(ppmPath)-3] + "png"
	if err := ConvertPPMtoPNG(ppmPath, pngPath); err != nil {
		return ppmPath, nil
	}
	return pngPath, nil
}
