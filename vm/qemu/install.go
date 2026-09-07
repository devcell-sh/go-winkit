package qemu

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// InstallConfig configures a Windows install boot via QEMU.
type InstallConfig struct {
	WindowsISO    string
	VirtIOISO     string
	AnswerVolume  string
	DevcellWimImg string

	DiskPath     string
	DiskSizeGB   int
	FirmwarePath string
	VarsPath     string

	// BootVolume, when set, is a FAT qcow2 carrying the retail Setup boot chain
	// (winpe.BuildSetupBootVolumeFiles). StartInstall then boots it (bootindex=1)
	// via BuildSetupBootArgv instead of trying to boot the Windows ISO's El
	// Torito image, which ASSERTs on Linux-hosted EDK2. The Windows ISO stays
	// attached as a readable CD for \sources\install.wim.
	BootVolume string

	CPUs          uint
	MemoryGB      uint64
	Accel         string
	CDBus         string
	DiskCacheMode string
	OutputDir     string

	// SSHPort forwards host:SSHPort → guest:SSHGuestPort. In the wsl2 stack the
	// guest port is gosshd's provisioning port, so this is the channel the host
	// provisions over. Zero disables the forward.
	SSHPort uint16
	// SSHGuestPort is the guest port SSHPort maps to (0 = 22). The wsl2 stack
	// sets 2222 so the gosshd provisioning server coexists with the Windows
	// OpenSSH the image ships on :22.
	SSHGuestPort uint16
	// OpenSSHHostPort forwards host:OpenSSHHostPort → guest:22, used to verify
	// the delivered Windows OpenSSH separately from the gosshd channel. Zero
	// disables it.
	OpenSSHHostPort uint16
	// RDPPort forwards host:RDPPort → guest:3389 for Remote Desktop. Zero
	// disables it.
	RDPPort uint16
	// StructuredLogPath, when set, backs the winkit.structured.0 virtio-serial
	// port with a build.jsonl file (the guest streams structured events there
	// once vioserial is installed in specialize).
	StructuredLogPath string
	// Secure boots the VM on the EL3/secure-world machine (secure=on, GICv3/ITS,
	// neoverse-n1, -kernel firmware). Required for the Hyper-V hypervisor to
	// actually launch. Implies FirmwareKernel (no pflash NVRAM).
	Secure bool
	// DisplayType overrides the QEMU -display flag. Empty defaults to "none".
	// Set to "vnc=:N" to start a VNC server on port 5900+N.
	DisplayType string
}

// InstallVM is a handle to a running Windows install VM.
type InstallVM struct {
	cmd       *exec.Cmd
	qmpSock   string
	serialLog string
	outputDir string
}

// QMPSocket returns the path to the QMP unix socket.
func (vm *InstallVM) QMPSocket() string { return vm.qmpSock }

// SerialLog returns the path to the serial console log.
func (vm *InstallVM) SerialLog() string { return vm.serialLog }

// OutputDir returns the VM's output directory.
func (vm *InstallVM) OutputDir() string { return vm.outputDir }

// PID returns the QEMU process ID, or 0 if not started.
func (vm *InstallVM) PID() int {
	if vm.cmd.Process != nil {
		return vm.cmd.Process.Pid
	}
	return 0
}

// Stop kills the QEMU process.
func (vm *InstallVM) Stop() error {
	if vm.cmd.Process != nil {
		vm.cmd.Process.Kill()
		vm.cmd.Wait()
	}
	return nil
}

// Wait waits for the QEMU process to exit.
func (vm *InstallVM) Wait() error {
	return vm.cmd.Wait()
}

// StartInstall boots a Windows installer via QEMU and returns a handle.
// The caller is responsible for monitoring progress (SSH polling, stall
// detection, screenshots) and calling Stop() when done.
// RunConfig boots an already-installed disk (continue/finalize mode) via
// BuildRunCommand, rather than driving Setup. The disk and its NVRAM vars store
// must already exist — the vars carry the "Windows Boot Manager" UEFI entry, so
// a fresh vars store would not boot the installed OS.
type RunConfig struct {
	DiskPath     string
	FirmwarePath string
	VarsPath     string
	OutputDir    string
	VMName       string
	VirtIOISO    string

	CPUs     uint
	MemoryGB uint64
	Accel    string

	SSHPort         uint16
	SSHGuestPort    uint16
	OpenSSHHostPort uint16
	RDPPort         uint16
	// SSHHost is the host address the forwards bind to. "" → 127.0.0.1
	// (loopback only). Set 0.0.0.0 so another host (e.g. a container reaching
	// the Mac via host.docker.internal) can connect to the forwarded ports.
	SSHHost string
	// Secure boots the VM on the EL3/secure-world machine. See InstallConfig.Secure.
	Secure bool
	// SMBIOSSerial passes -smbios type=1,serial=<value> so a guest startup
	// script can read it as the dynamic hostname.
	SMBIOSSerial string
	// DisplayType overrides the QEMU -display flag. Empty defaults to "none".
	DisplayType string
	// Detach runs QEMU in its own process group so it survives the parent's
	// exit. The caller must track the PID and stop it via QMP or signals.
	Detach bool
	// StructuredLogPath, when set, backs the winkit.structured.0 virtio-serial
	// port with a host-side file so gosshd guest events are captured.
	StructuredLogPath string
}

// StartRun boots an existing installed disk with SSH/RDP forwards and a QMP
// socket, for continue/interactive sessions. It does not create a disk or vars.
func StartRun(ctx context.Context, cfg RunConfig) (*InstallVM, error) {
	if cfg.DiskPath == "" {
		return nil, fmt.Errorf("DiskPath is required")
	}
	outDir := cfg.OutputDir
	if outDir == "" {
		return nil, fmt.Errorf("OutputDir is required")
	}
	os.MkdirAll(outDir, 0o755)

	fwPath := cfg.FirmwarePath
	if fwPath == "" {
		fwPath = FirmwarePath()
		if fwPath == "" {
			return nil, fmt.Errorf("no UEFI firmware found")
		}
	}

	cpus := cfg.CPUs
	if cpus == 0 {
		cpus = 4
	}
	mem := cfg.MemoryGB
	if mem == 0 {
		mem = 4
	}
	serialLog := filepath.Join(outDir, "serial.log")
	vmName := cfg.VMName
	if vmName == "" {
		vmName = "winkit-install"
	}

	runDisplayType := cfg.DisplayType
	if runDisplayType == "" {
		runDisplayType = "none"
	}
	spec := Spec{
		VMName:                 vmName,
		CPUs:                   cpus,
		MemoryGB:               mem,
		DiskPath:               cfg.DiskPath,
		FirmwarePath:           fwPath,
		VarsPath:               cfg.VarsPath,
		QMPSocketDir:           outDir,
		DisplayType:            runDisplayType,
		SerialLogPath:          serialLog,
		GuestStructuredLogPath: cfg.StructuredLogPath,
		VirtIOISO:              cfg.VirtIOISO,
		SSHPort:                cfg.SSHPort,
		SSHGuestPort:           cfg.SSHGuestPort,
		OpenSSHHostPort:        cfg.OpenSSHHostPort,
		RDPPort:                cfg.RDPPort,
		SSHHost:                cfg.SSHHost,
		SMBIOSSerial:           cfg.SMBIOSSerial,
	}
	if cfg.Accel != "" {
		spec.Accel = cfg.Accel
	}
	if cfg.Secure {
		kfw := KernelFirmwarePath()
		if kfw == "" {
			return nil, fmt.Errorf("secure machine requires QEMU_EFI.kernel.fd (EL3 firmware); not found in any search path")
		}
		spec.FirmwarePath = kfw
		spec.FirmwareKernel = true
		spec.VarsPath = ""
		spec.MachineType = "virt,virtualization=on,gic-version=3,its=on,secure=on"
		spec.CPU = "neoverse-n1"
	} else if cfg.VarsPath == "" {
		// No vars.fd: use -kernel firmware which auto-discovers the ESP
		// bootloader. Works with any accel (hvf, kvm, tcg).
		kfw := KernelFirmwarePath()
		if kfw == "" {
			return nil, fmt.Errorf("no vars.fd and no kernel firmware found; cannot boot")
		}
		spec.FirmwarePath = kfw
		spec.FirmwareKernel = true
		spec.VarsPath = ""
	}
	spec.ApplyDefaults()

	argv := BuildRunCommand(spec)
	qemuBin, err := QEMUBinaryPath()
	if err != nil {
		return nil, err
	}
	argv[0] = qemuBin
	os.MkdirAll(filepath.Join(outDir, "screenshots"), 0o755)

	var cmd *exec.Cmd
	if cfg.Detach {
		cmd = exec.Command(argv[0], argv[1:]...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	} else {
		cmd = exec.CommandContext(ctx, argv[0], argv[1:]...)
	}
	// Capture QEMU's own stderr/stdout so a launch failure (bad drive path,
	// unsupported machine/accel) is diagnosable from the output dir instead of
	// vanishing to the parent's terminal. Without this a dead VM looks identical
	// to a slow-booting one: the caller's port wait just spins forever.
	qemuLog := filepath.Join(outDir, "qemu.log")
	logF, err := os.Create(qemuLog)
	if err != nil {
		return nil, fmt.Errorf("creating qemu log: %w", err)
	}
	// Header written BEFORE launch, so even an instant death (bad path, missing
	// firmware) leaves behind the exact resolved command and which input files
	// were present — the two things that differ silently host-vs-container.
	fmt.Fprintf(logF, "=== winkit QEMU launch (%s) ===\n", runtime.GOOS)
	fmt.Fprintf(logF, "binary:   %s\n", qemuBin)
	fmt.Fprintf(logF, "accel:    %s\n", spec.effectiveAccel())
	for _, f := range []struct{ label, path string }{
		{"firmware", spec.FirmwarePath}, {"vars", spec.VarsPath},
		{"disk", spec.DiskPath}, {"virtio", spec.VirtIOISO},
	} {
		if f.path == "" {
			continue
		}
		state := "OK"
		if fi, statErr := os.Stat(f.path); statErr != nil {
			state = "MISSING: " + statErr.Error()
		} else {
			state = fmt.Sprintf("%d bytes", fi.Size())
		}
		fmt.Fprintf(logF, "%-9s %s  [%s]\n", f.label+":", f.path, state)
	}
	fmt.Fprintf(logF, "argv:     %s\n=== QEMU output follows ===\n", strings.Join(argv, " "))
	// Fail early with a clear message if a required input file is absent, rather
	// than letting QEMU abort with a terse open() error buried in its output.
	for _, req := range []struct{ label, path string }{
		{"firmware", spec.FirmwarePath}, {"vars", spec.VarsPath}, {"disk", spec.DiskPath},
	} {
		if req.path == "" {
			continue
		}
		if _, statErr := os.Stat(req.path); statErr != nil {
			logF.Close()
			return nil, fmt.Errorf("QEMU %s not found: %s (%v); see %s", req.label, req.path, statErr, qemuLog)
		}
	}
	cmd.Stdout = logF
	cmd.Stderr = logF
	if err := cmd.Start(); err != nil {
		logF.Close()
		return nil, fmt.Errorf("starting QEMU: %w", err)
	}
	qmpSock := QMPSocketPath(spec)
	// QEMU creates the QMP socket during init, before the guest boots, so it
	// appears within seconds even under slow TCG. A live VM answers a QMP dial;
	// a QEMU that aborted at launch (e.g. the hostfwd bind error that bit us when
	// port 20022 was already in use) leaves either no socket or a stale one that
	// refuses. Requiring an actual connection — not just the socket file's
	// presence — is what stops a died-at-launch QEMU masquerading as a slow boot.
	deadline := time.Now().Add(30 * time.Second)
	for {
		if c, err := net.Dial("unix", qmpSock); err == nil {
			c.Close()
			return &InstallVM{cmd: cmd, qmpSock: qmpSock, serialLog: serialLog, outputDir: outDir}, nil
		}
		if time.Now().After(deadline) {
			logF.Close()
			_ = cmd.Process.Kill()
			tail, _ := os.ReadFile(qemuLog)
			return nil, fmt.Errorf("QEMU did not accept a QMP connection within 30s (likely died at launch); see %s:\n%s", qemuLog, lastLines(string(tail), 20))
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// lastLines returns the final n lines of s, for surfacing the tail of a log in
// an error message without dumping the whole file.
func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func StartInstall(ctx context.Context, cfg InstallConfig) (*InstallVM, error) {
	if cfg.WindowsISO == "" {
		return nil, fmt.Errorf("WindowsISO is required")
	}
	if cfg.DiskPath == "" && cfg.OutputDir == "" {
		return nil, fmt.Errorf("DiskPath or OutputDir is required")
	}

	outDir := cfg.OutputDir
	if outDir == "" {
		var err error
		outDir, err = os.MkdirTemp("", "winkit-install-*")
		if err != nil {
			return nil, fmt.Errorf("creating output dir: %w", err)
		}
	}
	os.MkdirAll(outDir, 0o755)

	fwPath := cfg.FirmwarePath
	if fwPath == "" {
		fwPath = FirmwarePath()
		if fwPath == "" {
			return nil, fmt.Errorf("no UEFI firmware found")
		}
	}

	varsPath := cfg.VarsPath
	if varsPath == "" && !cfg.Secure {
		varsPath = filepath.Join(outDir, "vars.fd")
		if err := PrepareVarsFile(fwPath, varsPath); err != nil {
			return nil, fmt.Errorf("preparing vars: %w", err)
		}
	}

	diskPath := cfg.DiskPath
	if diskPath == "" {
		diskPath = filepath.Join(outDir, "disk.qcow2")
		diskSize := cfg.DiskSizeGB
		if diskSize == 0 {
			diskSize = 64
		}
		if err := CreateDisk(diskPath, diskSize); err != nil {
			return nil, fmt.Errorf("creating disk: %w", err)
		}
	}

	serialLog := filepath.Join(outDir, "serial.log")
	progressLog := filepath.Join(outDir, "guest-progress.log")

	cpus := cfg.CPUs
	if cpus == 0 {
		cpus = 4
	}
	mem := cfg.MemoryGB
	if mem == 0 {
		mem = 4
	}

	displayType := cfg.DisplayType
	if displayType == "" {
		displayType = "none"
	}
	spec := Spec{
		VMName:                 "winkit-install",
		CPUs:                   cpus,
		MemoryGB:               mem,
		DiskPath:               diskPath,
		FirmwarePath:           fwPath,
		VarsPath:               varsPath,
		QMPSocketDir:           outDir,
		DisplayType:            displayType,
		SerialLogPath:          serialLog,
		GuestProgressLogPath:   progressLog,
		GuestStructuredLogPath: cfg.StructuredLogPath,
		VirtIOISO:              cfg.VirtIOISO,
		DiskCacheMode:          cfg.DiskCacheMode,
		CDBus:                  cfg.CDBus,
		SSHPort:                cfg.SSHPort,
		SSHGuestPort:           cfg.SSHGuestPort,
		OpenSSHHostPort:        cfg.OpenSSHHostPort,
		RDPPort:                cfg.RDPPort,
	}
	if cfg.Accel != "" {
		spec.Accel = cfg.Accel
	}
	if cfg.Secure {
		kfw := KernelFirmwarePath()
		if kfw == "" {
			return nil, fmt.Errorf("secure machine requires QEMU_EFI.kernel.fd (EL3 firmware); not found in any search path")
		}
		spec.FirmwarePath = kfw
		spec.FirmwareKernel = true
		spec.VarsPath = ""
		spec.MachineType = "virt,virtualization=on,gic-version=3,its=on,secure=on"
		spec.CPU = "neoverse-n1"
	}
	spec.ApplyDefaults()

	var argv []string
	if cfg.BootVolume != "" {
		// FAT-boot the retail Setup boot chain; the ISO is only a file source.
		argv = BuildSetupBootArgv(spec, cfg.BootVolume, cfg.WindowsISO, cfg.AnswerVolume)
	} else {
		argv = BuildInstallCommand(spec, cfg.WindowsISO, cfg.AnswerVolume, cfg.DevcellWimImg)
	}

	qemuBin, err := QEMUBinaryPath()
	if err != nil {
		return nil, err
	}
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

	return &InstallVM{
		cmd:       cmd,
		qmpSock:   qmpSock,
		serialLog: serialLog,
		outputDir: outDir,
	}, nil
}
