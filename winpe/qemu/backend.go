package qemu

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/devcell-sh/go-winkit/winpe"
)

// InstallOptions holds QEMU-specific options for StartInstall that do not
// belong in the backend-agnostic winpe.VMInstallConfig. Pass as
// VMInstallConfig.BackendExtra.
type InstallOptions struct {
	BootVolume        string
	DevcellWimImg     string
	FirmwarePath      string
	VarsPath          string
	CDBus             string
	DiskCacheMode     string
	StructuredLogPath string
	Secure            bool
}

// RunOptions holds QEMU-specific options for StartRun. Pass as
// VMRunConfig.BackendExtra.
type RunOptions struct {
	FirmwarePath string
	VarsPath     string
	Secure       bool
}

// Backend implements winpe.VMBackend using QEMU.
type Backend struct{}

func (b *Backend) DefaultDiskFormat() winpe.DiskFormat {
	return winpe.DiskFormatQcow2
}

func (b *Backend) CreateDisk(path string, sizeGB int, format winpe.DiskFormat) error {
	if format == winpe.DiskFormatDefault {
		format = b.DefaultDiskFormat()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	switch format {
	case winpe.DiskFormatQcow2:
		return CreateDisk(path, sizeGB)
	case winpe.DiskFormatRaw:
		return createRawDisk(path, sizeGB)
	default:
		return fmt.Errorf("unsupported disk format: %v", format)
	}
}

func createRawDisk(path string, sizeGB int) error {
	cmd := exec.Command("qemu-img", "create", "-f", "raw", path, fmt.Sprintf("%dG", sizeGB))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("qemu-img create raw: %w\n%s", err, out)
	}
	return nil
}

func (b *Backend) CreateOverlay(path, base string) error {
	return CreateOverlay(path, base)
}

func (b *Backend) Flatten(src, dst string) error {
	return FlattenQcow2(src, dst)
}

func (b *Backend) StartInstall(ctx context.Context, cfg winpe.VMInstallConfig) (winpe.VM, error) {
	qcfg := InstallConfig{
		WindowsISO:   cfg.WindowsISO,
		VirtIOISO:    cfg.VirtIOISO,
		AnswerVolume: cfg.AnswerVolume,
		DiskPath:     cfg.DiskPath,
		DiskSizeGB:   cfg.DiskSizeGB,
		CPUs:         cfg.CPUs,
		MemoryGB:     cfg.MemoryGB,
		OutputDir:    cfg.OutputDir,
		SSHPort:      cfg.SSHPort,
		SSHGuestPort: cfg.SSHGuestPort,
		OpenSSHHostPort: cfg.OpenSSHHostPort,
		RDPPort:      cfg.RDPPort,
		Accel:        cfg.Accel,
	}
	if opts, ok := cfg.BackendExtra.(*InstallOptions); ok && opts != nil {
		qcfg.BootVolume = opts.BootVolume
		qcfg.DevcellWimImg = opts.DevcellWimImg
		qcfg.FirmwarePath = opts.FirmwarePath
		qcfg.VarsPath = opts.VarsPath
		qcfg.CDBus = opts.CDBus
		qcfg.DiskCacheMode = opts.DiskCacheMode
		qcfg.StructuredLogPath = opts.StructuredLogPath
		qcfg.Secure = opts.Secure
	}
	vm, err := StartInstall(ctx, qcfg)
	if err != nil {
		return nil, err
	}
	sshAddr := fmt.Sprintf("127.0.0.1:%d", cfg.SSHPort)
	return newVMHandle(vm, sshAddr), nil
}

func (b *Backend) StartRun(ctx context.Context, cfg winpe.VMRunConfig) (winpe.VM, error) {
	qcfg := RunConfig{
		DiskPath:        cfg.DiskPath,
		OutputDir:       cfg.OutputDir,
		VMName:          cfg.VMName,
		VirtIOISO:       cfg.VirtIOISO,
		CPUs:            cfg.CPUs,
		MemoryGB:        cfg.MemoryGB,
		SSHPort:         cfg.SSHPort,
		SSHGuestPort:    cfg.SSHGuestPort,
		OpenSSHHostPort: cfg.OpenSSHHostPort,
		RDPPort:         cfg.RDPPort,
		SSHHost:         cfg.SSHHost,
		Accel:           cfg.Accel,
		SMBIOSSerial:    cfg.SMBIOSSerial,
	}
	if opts, ok := cfg.BackendExtra.(*RunOptions); ok && opts != nil {
		qcfg.FirmwarePath = opts.FirmwarePath
		qcfg.VarsPath = opts.VarsPath
		qcfg.Secure = opts.Secure
	}
	vm, err := StartRun(ctx, qcfg)
	if err != nil {
		return nil, err
	}
	sshAddr := fmt.Sprintf("127.0.0.1:%d", cfg.SSHPort)
	if cfg.SSHHost != "" {
		sshAddr = fmt.Sprintf("%s:%d", cfg.SSHHost, cfg.SSHPort)
	}
	return newVMHandle(vm, sshAddr), nil
}

// vmHandle wraps InstallVM to satisfy winpe.VM, adding SSHAddr and Done.
type vmHandle struct {
	*InstallVM
	sshAddr string
	done    chan struct{}
}

func (h *vmHandle) SSHAddr() string   { return h.sshAddr }
func (h *vmHandle) OutputDir() string { return h.InstallVM.OutputDir() }
func (h *vmHandle) Done() <-chan struct{} { return h.done }

func newVMHandle(vm *InstallVM, sshAddr string) *vmHandle {
	h := &vmHandle{InstallVM: vm, sshAddr: sshAddr, done: make(chan struct{})}
	go func() {
		_ = vm.Wait()
		close(h.done)
	}()
	return h
}

// QMPSocketFromVM extracts the QMP socket path from a VM started by this
// backend. Returns "" if v was not created by the QEMU backend.
func QMPSocketFromVM(v winpe.VM) string {
	if h, ok := v.(*vmHandle); ok {
		return h.QMPSocket()
	}
	return ""
}
