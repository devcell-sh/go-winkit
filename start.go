package winkit

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/devcell-sh/go-winkit/build"
	"github.com/devcell-sh/go-winkit/unattend"
	"github.com/devcell-sh/go-winkit/vm"
	"github.com/devcell-sh/go-winkit/vm/qemu"
	"github.com/devcell-sh/go-winkit/vm/vmstate"
	"github.com/devcell-sh/go-winkit/winpe"
)

// StartOpts configures Start. Only Image is required; zero values get
// the CLI's defaults (4 vCPUs, 6 GB, SSH 20022, RDP 23389, backend
// auto-detected).
type StartOpts struct {
	// Image is the built disk image to boot (qcow2 for QEMU).
	Image string
	// Name identifies the VM in state/status; defaults to the image
	// filename without extension.
	Name string
	// Hostname is applied as the guest computer name via the SMBIOS
	// serial on next boot; defaults to the unattend default.
	Hostname string
	// Accel selects the accelerator (kvm, hvf, tcg); default: best for
	// the host.
	Accel string
	// StateDir holds running-VM state for Status/stop; default
	// ~/.winkit/run.
	StateDir string
	CPUs     uint
	MemoryGB uint64
	// SSHPort forwards to the guest provisioning SSH (gosshd, :2222).
	// The delivered Windows OpenSSH lands on SSHPort+100.
	SSHPort uint16
	RDPPort uint16
	// Foreground keeps the VM attached to the calling process; the
	// default detaches it so the handle outlives the caller.
	Foreground bool
	// VNC starts a VNC server on display :0 (port 5900).
	VNC bool
	// Logger receives host-side events; nil logs to run.jsonl in the
	// VM's output directory.
	Logger *slog.Logger
}

// Start boots a previously built image and registers it in the state
// dir so Status and the winkit CLI can manage it. The returned handle
// exposes Stop, Wait, Done, SSHAddr, PID. This is the library form of
// `winkit start`; devcell's engine adapter wraps it.
func Start(ctx context.Context, opts StartOpts) (vm.VM, error) {
	if opts.Image == "" {
		return nil, fmt.Errorf("StartOpts.Image is required")
	}
	absImage, err := filepath.Abs(opts.Image)
	if err != nil {
		return nil, fmt.Errorf("resolving image path: %w", err)
	}
	if _, err := os.Stat(absImage); err != nil {
		return nil, fmt.Errorf("image not found: %w", err)
	}
	artifact, err := build.LoadArtifact(absImage)
	if err != nil {
		return nil, err
	}

	name := opts.Name
	if name == "" {
		base := filepath.Base(absImage)
		name = strings.TrimSuffix(base, filepath.Ext(base))
	}
	stateDir := opts.StateDir
	if stateDir == "" {
		stateDir = vmstate.DefaultDir()
	}
	if existing, err := vmstate.FindByImage(stateDir, absImage); err == nil &&
		existing != nil && vmstate.IsAlive(existing.PID) {
		return nil, fmt.Errorf("VM %q is already running (PID %d) from image %s",
			existing.Name, existing.PID, absImage)
	}

	accel := opts.Accel
	if accel == "" {
		accel = qemu.DefaultAccel()
	}
	cpus := opts.CPUs
	if cpus == 0 {
		cpus = 4
	}
	memoryGB := opts.MemoryGB
	if memoryGB == 0 {
		memoryGB = 6
	}
	sshPort := opts.SSHPort
	if sshPort == 0 {
		sshPort = 20022
	}
	rdpPort := opts.RDPPort
	if rdpPort == 0 {
		rdpPort = 23389
	}
	hostname := opts.Hostname
	if hostname == "" {
		hostname = unattend.DefaultConfig().Hostname
	}

	backend, backendName, err := build.ResolveBackend()
	if err != nil {
		return nil, err
	}

	outDir := filepath.Join(filepath.Dir(absImage), ".winkit", "run", name)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating output dir: %w", err)
	}

	logger := opts.Logger
	if logger == nil {
		// run.jsonl is the run's structured log; the guest's own event
		// stream (guest.jsonl) is appended on teardown by winkit stop.
		hostLogFile, err := os.Create(filepath.Join(outDir, "run.jsonl"))
		if err != nil {
			return nil, fmt.Errorf("creating host log: %w", err)
		}
		defer hostLogFile.Close()
		logger = slog.New(winpe.NewGuestEventHandler(hostLogFile))
	}

	secure := strings.HasPrefix(accel, "tcg")
	var varsPath string
	if artifact != nil {
		// WSL1 does not need an EL3/secure machine. A fresh writable vars file
		// gives the standalone PE FAT volume the same proven boot path as the
		// integration harness.
		secure = false
		fwPath := qemu.FirmwarePath()
		if fwPath == "" {
			return nil, fmt.Errorf("no UEFI firmware found")
		}
		varsPath = filepath.Join(outDir, "vars.fd")
		if err := qemu.PrepareVarsFile(fwPath, varsPath); err != nil {
			return nil, fmt.Errorf("preparing PE artifact vars: %w", err)
		}
	} else if !secure {
		if found := build.FindSiblingVars(absImage); found != "" {
			dst := filepath.Join(outDir, "vars.fd")
			data, err := os.ReadFile(found)
			if err != nil {
				return nil, fmt.Errorf("copying vars: %w", err)
			}
			if err := os.WriteFile(dst, data, 0o644); err != nil {
				return nil, fmt.Errorf("copying vars: %w", err)
			}
			varsPath = dst
		}
	}

	diskPath := absImage
	bootVolume := ""
	if artifact != nil {
		diskPath = artifact.DataDisk
		bootVolume = artifact.BootVolume
	}
	runCfg := vm.VMRunConfig{
		DiskPath:        diskPath,
		OutputDir:       outDir,
		VMName:          "winkit-" + name,
		CPUs:            cpus,
		MemoryGB:        memoryGB,
		Accel:           accel,
		SSHPort:         sshPort,
		SSHGuestPort:    2222,
		OpenSSHHostPort: sshPort + 100,
		RDPPort:         rdpPort,
		SMBIOSSerial:    hostname,
	}
	displayType := "none"
	var vncPort uint16
	if opts.VNC {
		displayType = "vnc=:0"
		vncPort = 5900
	}
	if backendName == "qemu" {
		runCfg.BackendExtra = &qemu.RunOptions{
			BootVolume:        bootVolume,
			VarsPath:          varsPath,
			Secure:            secure,
			DisplayType:       displayType,
			Detach:            !opts.Foreground,
			StructuredLogPath: filepath.Join(outDir, "guest.jsonl"),
		}
	}

	logger.Info("starting VM",
		"image", absImage, "disk", diskPath, "boot_volume", bootVolume,
		"accel", accel, "backend", backendName,
		"cpus", cpus, "memory_gb", memoryGB)

	machine, err := backend.StartRun(ctx, runCfg)
	if err != nil {
		return nil, fmt.Errorf("starting VM: %w", err)
	}

	st := &vmstate.State{
		Name:      name,
		ImagePath: absImage,
		PID:       machine.PID(),
		Backend:   backendName,
		StartedAt: time.Now(),
		SSHPort:   sshPort,
		RDPPort:   rdpPort,
		VNCPort:   vncPort,
		Accel:     accel,
		OutputDir: outDir,
	}
	if err := vmstate.Save(stateDir, st); err != nil {
		machine.Stop()
		return nil, fmt.Errorf("saving state: %w", err)
	}
	logger.Info("VM started", "pid", machine.PID(), "name", name)
	return machine, nil
}

// Status lists the VMs registered in stateDir (default ~/.winkit/run)
// as saved by Start / `winkit start`. Dead entries are included;
// vmstate.IsAlive distinguishes them.
func Status(stateDir string) ([]*vmstate.State, error) {
	if stateDir == "" {
		stateDir = vmstate.DefaultDir()
	}
	return vmstate.List(stateDir)
}
