//go:build darwin

package vz

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/tmc/apple/dispatch"
	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/virtualization"
	"github.com/tmc/apple/x/vzkit/display"
	"github.com/tmc/apple/x/vzkit/vnc"

	"github.com/devcell-sh/go-winkit/winpe"
)

// Backend implements winpe.VMBackend using Apple's Virtualization.framework
// via tmc/apple. macOS 13+ required; USB mass storage needs macOS 14+.
type Backend struct{}

func (*Backend) DefaultDiskFormat() winpe.DiskFormat { return winpe.DiskFormatRaw }

func (*Backend) CreateDisk(path string, sizeGB int, format winpe.DiskFormat) error {
	if format == winpe.DiskFormatDefault {
		format = winpe.DiskFormatRaw
	}
	if format != winpe.DiskFormatRaw {
		return fmt.Errorf("vz backend only supports raw disk images (got %v)", format)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("creating raw disk: %w", err)
	}
	size := int64(sizeGB) * 1024 * 1024 * 1024
	if err := f.Truncate(size); err != nil {
		f.Close()
		return fmt.Errorf("truncating raw disk to %dG: %w", sizeGB, err)
	}
	return f.Close()
}

// CreateOverlay for raw disks: full copy of base (APFS sparse keeps it
// reasonable on macOS). The overlay IS the working copy; base stays pristine.
func (*Backend) CreateOverlay(path, base string) error {
	return copyFile(base, path)
}

// Flatten for raw disks: copy src to dst. Raw images are already standalone.
func (*Backend) Flatten(src, dst string) error {
	return copyFile(src, dst)
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

func (b *Backend) StartInstall(ctx context.Context, cfg winpe.VMInstallConfig) (winpe.VM, error) {
	outDir := cfg.OutputDir
	if outDir == "" {
		return nil, fmt.Errorf("OutputDir is required")
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating output dir: %w", err)
	}

	var logger *slog.Logger
	var vsockSSHPort uint32
	var vncPort uint16
	var bootVolume string
	varsPath := ""
	if opts, ok := cfg.BackendExtra.(*InstallOptions); ok && opts != nil {
		varsPath = opts.EFIVarsPath
		vsockSSHPort = opts.VsockSSHPort
		vncPort = opts.VNCPort
		logger = opts.Logger
		bootVolume = opts.BootVolume
	}
	if logger == nil {
		logger = slog.Default()
	}

	if cfg.DiskPath == "" {
		cfg.DiskPath = filepath.Join(outDir, "disk.raw")
		sz := cfg.DiskSizeGB
		if sz == 0 {
			sz = 64
		}
		if err := b.CreateDisk(cfg.DiskPath, sz, winpe.DiskFormatRaw); err != nil {
			return nil, fmt.Errorf("creating disk: %w", err)
		}
	}

	if varsPath == "" {
		varsPath = filepath.Join(outDir, "nvram.vars")
	}
	efiStore, err := virtualization.NewEFIVariableStoreCreatingVariableStoreAtURLOptionsError(
		foundation.NewURLFileURLWithPath(varsPath),
		virtualization.VZEFIVariableStoreInitializationOptionAllowOverwrite,
	)
	if err != nil {
		return nil, fmt.Errorf("creating EFI variable store: %w", err)
	}

	bootloader := virtualization.NewVZEFIBootLoader()
	bootloader.SetVariableStore(efiStore)

	cpus, memBytes := computeDefaults(cfg.CPUs, cfg.MemoryGB)
	vmCfg := virtualization.NewVZVirtualMachineConfiguration()
	vmCfg.SetBootLoader(bootloader)
	vmCfg.SetCPUCount(cpus)
	vmCfg.SetMemorySize(memBytes)

	logger.Info("vz: configuring VM",
		"cpus", cpus, "memoryGB", memBytes/(1024*1024*1024),
		"disk", cfg.DiskPath, "nvram", varsPath,
		"vsockSSHPort", vsockSSHPort)

	var storage []virtualization.VZStorageDeviceConfiguration

	// Attach-bus experiment knobs (see attachStorage): WINKIT_VZ_MAIN_ATTACH
	// for the install target disk, WINKIT_VZ_BOOT_ATTACH for the Setup boot
	// volume. Defaults preserve the original behavior (nvme + usb).
	mainKind := envAttachKind("WINKIT_VZ_MAIN_ATTACH", "nvme")
	bootKind := envAttachKind("WINKIT_VZ_BOOT_ATTACH", "usb")

	disk, err := attachStorage(mainKind, cfg.DiskPath, false)
	if err != nil {
		return nil, fmt.Errorf("attaching main disk (%s): %w", mainKind, err)
	}
	storage = append(storage, disk)
	logger.Info("vz: attached main disk", "kind", mainKind, "path", cfg.DiskPath)

	for _, ud := range []struct{ path, label, kind string }{
		{bootVolume, "boot volume", bootKind},
		{cfg.WindowsISO, "Windows ISO", "usb"},
		{cfg.VirtIOISO, "VirtIO ISO", "usb"},
		{cfg.AnswerVolume, "answer volume", "usb"},
	} {
		if ud.path == "" {
			continue
		}
		dev, err := attachStorage(ud.kind, ud.path, true)
		if err != nil {
			return nil, fmt.Errorf("attaching %s (%s): %w", ud.label, ud.kind, err)
		}
		storage = append(storage, dev)
		logger.Info("vz: attached storage", "label", ud.label, "kind", ud.kind, "path", ud.path)
	}
	vmCfg.SetStorageDevices(storage)

	if err := setNetwork(vmCfg); err != nil {
		return nil, err
	}
	logger.Info("vz: NAT network configured")

	if vsockSSHPort > 0 {
		if err := setVsock(vmCfg); err != nil {
			return nil, err
		}
		logger.Info("vz: vsock device added", "guestPort", vsockSSHPort)
	}

	serialLog := filepath.Join(outDir, "serial.log")
	if err := setSerial(vmCfg, serialLog); err != nil {
		return nil, err
	}

	if vncPort > 0 {
		if err := setGraphics(vmCfg); err != nil {
			return nil, err
		}
		logger.Info("vz: virtio graphics device configured for VNC")
	}

	os.MkdirAll(filepath.Join(outDir, "screenshots"), 0o755)

	ok, valErr := vmCfg.ValidateWithError()
	if !ok || valErr != nil {
		return nil, fmt.Errorf("VM configuration invalid: %w", valErr)
	}
	logger.Info("vz: VM configuration validated")

	logger.Info("vz: creating and starting virtual machine")
	queue := dispatch.QueueCreate("sh.devcell.winkit.vz")
	vm := virtualization.NewVirtualMachineWithConfigurationQueue(vmCfg, queue)

	h := &vmHandle{vm: vm, queue: queue, outDir: outDir, sshPort: cfg.SSHPort, sshHost: "127.0.0.1", logger: logger, done: make(chan struct{}), vncPort: vncPort}

	if err := h.startOnQueue(ctx); err != nil {
		return nil, fmt.Errorf("starting virtual machine: %w", err)
	}
	logger.Info("vz: VM started", "state", vzStateName(vm))

	if vncPort > 0 {
		if err := h.startVNC(vm); err != nil {
			logger.Warn("vz: VNC server failed to start (screenshots unavailable)", "err", err)
		}
	}

	if vsockSSHPort > 0 {
		if err := h.startVsockProxy(cfg.SSHPort, vsockSSHPort); err != nil {
			h.stopOnQueue()
			return nil, fmt.Errorf("starting vsock proxy: %w", err)
		}
		logger.Info("vz: vsock SSH proxy listening", "hostAddr", fmt.Sprintf("127.0.0.1:%d", cfg.SSHPort), "guestVsockPort", vsockSSHPort)
	}
	return h, nil
}

func (b *Backend) StartRun(ctx context.Context, cfg winpe.VMRunConfig) (winpe.VM, error) {
	outDir := cfg.OutputDir
	if outDir == "" {
		return nil, fmt.Errorf("OutputDir is required")
	}
	if cfg.DiskPath == "" {
		return nil, fmt.Errorf("DiskPath is required")
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating output dir: %w", err)
	}

	var logger *slog.Logger
	var vsockSSHPort uint32
	var vncPort uint16
	varsPath := ""
	if opts, ok := cfg.BackendExtra.(*RunOptions); ok && opts != nil {
		varsPath = opts.EFIVarsPath
		vsockSSHPort = opts.VsockSSHPort
		vncPort = opts.VNCPort
		logger = opts.Logger
	}
	if logger == nil {
		logger = slog.Default()
	}
	if varsPath == "" {
		varsPath = filepath.Join(outDir, "nvram.vars")
	}

	// Reuse an existing variable store (continue mode boots with the boot
	// entries the install wrote), but create a fresh one when the file does
	// not exist yet — pointing the boot loader at a missing vars file fails
	// VM start with "The boot loader is invalid."
	var efiStore virtualization.VZEFIVariableStore
	if _, statErr := os.Stat(varsPath); statErr == nil {
		efiStore = virtualization.NewEFIVariableStoreWithURL(
			foundation.NewURLFileURLWithPath(varsPath),
		)
	} else {
		created, err := virtualization.NewEFIVariableStoreCreatingVariableStoreAtURLOptionsError(
			foundation.NewURLFileURLWithPath(varsPath),
			virtualization.VZEFIVariableStoreInitializationOptionAllowOverwrite,
		)
		if err != nil {
			return nil, fmt.Errorf("creating EFI variable store: %w", err)
		}
		efiStore = created
		logger.Info("vz: created fresh EFI variable store", "path", varsPath)
	}

	bootloader := virtualization.NewVZEFIBootLoader()
	bootloader.SetVariableStore(efiStore)

	cpus, memBytes := computeDefaults(cfg.CPUs, cfg.MemoryGB)
	vmCfg := virtualization.NewVZVirtualMachineConfiguration()
	vmCfg.SetBootLoader(bootloader)
	vmCfg.SetCPUCount(cpus)
	vmCfg.SetMemorySize(memBytes)

	logger.Info("vz: configuring VM",
		"cpus", cpus, "memoryGB", memBytes/(1024*1024*1024),
		"disk", cfg.DiskPath, "nvram", varsPath,
		"vsockSSHPort", vsockSSHPort,
		"sharedDir", cfg.SharedDir)

	var storage []virtualization.VZStorageDeviceConfiguration
	mainKind := envAttachKind("WINKIT_VZ_MAIN_ATTACH", "nvme")
	disk, err := attachStorage(mainKind, cfg.DiskPath, false)
	if err != nil {
		return nil, fmt.Errorf("attaching disk (%s): %w", mainKind, err)
	}
	storage = append(storage, disk)
	logger.Info("vz: attached main disk", "kind", mainKind, "path", cfg.DiskPath)
	if cfg.VirtIOISO != "" {
		usb, err := attachUSB(cfg.VirtIOISO, true)
		if err != nil {
			return nil, fmt.Errorf("attaching VirtIO ISO: %w", err)
		}
		storage = append(storage, usb.VZStorageDeviceConfiguration)
		logger.Info("vz: attached VirtIO ISO", "path", cfg.VirtIOISO)
	}
	vmCfg.SetStorageDevices(storage)

	if err := setNetwork(vmCfg); err != nil {
		return nil, err
	}

	if cfg.SharedDir != "" {
		if err := setSharedDir(vmCfg, cfg.SharedDir); err != nil {
			return nil, err
		}
		logger.Info("vz: virtio-fs shared directory configured", "path", cfg.SharedDir, "tag", SharedDirTag)
	}

	if vsockSSHPort > 0 {
		if err := setVsock(vmCfg); err != nil {
			return nil, err
		}
		logger.Info("vz: vsock device added", "guestPort", vsockSSHPort)
	}

	serialLog := filepath.Join(outDir, "serial.log")
	if err := setSerial(vmCfg, serialLog); err != nil {
		return nil, err
	}

	if vncPort > 0 {
		if err := setGraphics(vmCfg); err != nil {
			return nil, err
		}
		logger.Info("vz: virtio graphics device configured for VNC")
	}

	os.MkdirAll(filepath.Join(outDir, "screenshots"), 0o755)

	ok, valErr := vmCfg.ValidateWithError()
	if !ok || valErr != nil {
		return nil, fmt.Errorf("VM configuration invalid: %w", valErr)
	}
	logger.Info("vz: VM configuration validated")

	logger.Info("vz: creating and starting virtual machine")
	queue := dispatch.QueueCreate("sh.devcell.winkit.vz")
	vm := virtualization.NewVirtualMachineWithConfigurationQueue(vmCfg, queue)

	sshHost := cfg.SSHHost
	if sshHost == "" {
		sshHost = "127.0.0.1"
	}
	h := &vmHandle{vm: vm, queue: queue, outDir: outDir, sshPort: cfg.SSHPort, sshHost: sshHost, logger: logger, done: make(chan struct{}), vncPort: vncPort}

	if err := h.startOnQueue(ctx); err != nil {
		return nil, fmt.Errorf("starting virtual machine: %w", err)
	}
	logger.Info("vz: VM started", "state", vzStateName(vm))

	if vncPort > 0 {
		if err := h.startVNC(vm); err != nil {
			logger.Warn("vz: VNC server failed to start (screenshots unavailable)", "err", err)
		}
	}

	if vsockSSHPort > 0 {
		if err := h.startVsockProxy(cfg.SSHPort, vsockSSHPort); err != nil {
			h.stopOnQueue()
			return nil, fmt.Errorf("starting vsock proxy: %w", err)
		}
		logger.Info("vz: vsock SSH proxy listening", "hostAddr", fmt.Sprintf("%s:%d", sshHost, cfg.SSHPort), "guestVsockPort", vsockSSHPort)
	}
	return h, nil
}

func computeDefaults(cpus uint, memGB uint64) (uint, uint64) {
	if cpus == 0 {
		cpus = 4
	}
	if memGB == 0 {
		memGB = 4
	}
	return cpus, memGB * 1024 * 1024 * 1024
}

func attachNVMe(path string, readOnly bool) (virtualization.VZNVMExpressControllerDeviceConfiguration, error) {
	att, err := virtualization.NewDiskImageStorageDeviceAttachmentWithURLReadOnlyError(
		foundation.NewURLFileURLWithPath(path), readOnly,
	)
	if err != nil {
		return virtualization.VZNVMExpressControllerDeviceConfiguration{}, err
	}
	return virtualization.NewNVMExpressControllerDeviceConfigurationWithAttachment(att), nil
}

func attachVirtio(path string, readOnly bool) (virtualization.VZVirtioBlockDeviceConfiguration, error) {
	att, err := virtualization.NewDiskImageStorageDeviceAttachmentWithURLReadOnlyError(
		foundation.NewURLFileURLWithPath(path), readOnly,
	)
	if err != nil {
		return virtualization.VZVirtioBlockDeviceConfiguration{}, err
	}
	return virtualization.NewVirtioBlockDeviceConfigurationWithAttachment(att), nil
}

// attachStorage attaches path as the given device kind: "usb", "virtio", or
// "nvme". Boot-order debugging knob: Apple's EFI behavior differs per bus
// (Tart boots Linux from virtio-blk; UTM restricts NVMe to Linux guests and
// uses USB mass storage only for El Torito ISOs), so which bus the Setup boot
// volume sits on may decide whether the firmware boots it at all.
func attachStorage(kind, path string, readOnly bool) (virtualization.VZStorageDeviceConfiguration, error) {
	switch kind {
	case "virtio":
		d, err := attachVirtio(path, readOnly)
		return d.VZStorageDeviceConfiguration, err
	case "nvme":
		d, err := attachNVMe(path, readOnly)
		return d.VZStorageDeviceConfiguration, err
	case "usb", "":
		d, err := attachUSB(path, readOnly)
		return d.VZStorageDeviceConfiguration, err
	default:
		return virtualization.VZStorageDeviceConfiguration{}, fmt.Errorf("unknown storage attach kind %q (want usb|virtio|nvme)", kind)
	}
}

// envAttachKind reads an attach-kind override from the environment, falling
// back to def when unset.
func envAttachKind(envVar, def string) string {
	if v := os.Getenv(envVar); v != "" {
		return v
	}
	return def
}

func attachUSB(path string, readOnly bool) (virtualization.VZUSBMassStorageDeviceConfiguration, error) {
	att, err := virtualization.NewDiskImageStorageDeviceAttachmentWithURLReadOnlyError(
		foundation.NewURLFileURLWithPath(path), readOnly,
	)
	if err != nil {
		return virtualization.VZUSBMassStorageDeviceConfiguration{}, err
	}
	return virtualization.NewUSBMassStorageDeviceConfigurationWithAttachment(att), nil
}

func setSharedDir(vmCfg virtualization.VZVirtualMachineConfiguration, dirPath string) error {
	share := virtualization.NewSharedDirectoryWithURLReadOnly(
		foundation.NewURLFileURLWithPath(dirPath), false,
	)
	single := virtualization.NewSingleDirectoryShareWithDirectory(share)
	fsCfg := virtualization.NewVirtioFileSystemDeviceConfigurationWithTag(SharedDirTag)
	fsCfg.SetShare(single)
	vmCfg.SetDirectorySharingDevices([]virtualization.VZDirectorySharingDeviceConfiguration{fsCfg.VZDirectorySharingDeviceConfiguration})
	return nil
}

func setVsock(vmCfg virtualization.VZVirtualMachineConfiguration) error {
	sockCfg := virtualization.NewVZVirtioSocketDeviceConfiguration()
	vmCfg.SetSocketDevices([]virtualization.VZSocketDeviceConfiguration{sockCfg.VZSocketDeviceConfiguration})
	return nil
}

func setNetwork(vmCfg virtualization.VZVirtualMachineConfiguration) error {
	nat := virtualization.NewVZNATNetworkDeviceAttachment()
	netCfg := virtualization.NewVZVirtioNetworkDeviceConfiguration()
	netCfg.SetAttachment(nat)
	vmCfg.SetNetworkDevices([]virtualization.VZNetworkDeviceConfiguration{netCfg.VZNetworkDeviceConfiguration})
	return nil
}

func setSerial(vmCfg virtualization.VZVirtualMachineConfiguration, logPath string) error {
	att, err := virtualization.NewFileSerialPortAttachmentWithURLAppendError(
		foundation.NewURLFileURLWithPath(logPath), false,
	)
	if err != nil {
		return fmt.Errorf("creating serial attachment: %w", err)
	}
	port := virtualization.NewVZVirtioConsoleDeviceSerialPortConfiguration()
	port.SetAttachment(att)
	vmCfg.SetSerialPorts([]virtualization.VZSerialPortConfiguration{port.VZSerialPortConfiguration})
	return nil
}

func setGraphics(vmCfg virtualization.VZVirtualMachineConfiguration) error {
	gfx, err := display.CreateVirtioGraphicsConfig([]display.Config{display.DefaultLinux()})
	if err != nil {
		return fmt.Errorf("creating virtio graphics device: %w", err)
	}
	vmCfg.SetGraphicsDevices([]virtualization.VZGraphicsDeviceConfiguration{gfx.VZGraphicsDeviceConfiguration})
	return nil
}

// vmHandle wraps a virtualization.VZVirtualMachine to satisfy winpe.VM.
type vmHandle struct {
	vm      virtualization.VZVirtualMachine
	queue   dispatch.Queue
	outDir  string
	sshPort uint16
	sshHost string
	vncPort uint16
	logger  *slog.Logger

	vncServer     *vnc.Server
	proxyListener net.Listener
	proxyWg       *sync.WaitGroup
	done          chan struct{}
	doneOnce      sync.Once
}

func vzStateName(vm virtualization.VZVirtualMachine) string {
	switch vm.State() {
	case virtualization.VZVirtualMachineStateStopped:
		return "stopped"
	case virtualization.VZVirtualMachineStateRunning:
		return "running"
	case virtualization.VZVirtualMachineStatePaused:
		return "paused"
	case virtualization.VZVirtualMachineStateError:
		return "error"
	case virtualization.VZVirtualMachineStateStarting:
		return "starting"
	case virtualization.VZVirtualMachineStatePausing:
		return "pausing"
	case virtualization.VZVirtualMachineStateResuming:
		return "resuming"
	case virtualization.VZVirtualMachineStateStopping:
		return "stopping"
	default:
		return fmt.Sprintf("unknown(%d)", vm.State())
	}
}

// installDelegateOnQueue sets up the VZVirtualMachineDelegate. Must be called
// on the VM's dispatch queue.
func (h *vmHandle) installDelegateOnQueue(vm virtualization.VZVirtualMachine) {
	delegate := virtualization.NewVZVirtualMachineDelegate(virtualization.VZVirtualMachineDelegateConfig{
		GuestDidStopVirtualMachine: func(_ virtualization.VZVirtualMachine) {
			h.logger.Info("vz: VM state changed", "state", "stopped")
			h.doneOnce.Do(func() { close(h.done) })
		},
		VirtualMachineDidStopWithError: func(_ virtualization.VZVirtualMachine, err foundation.NSError) {
			h.logger.Info("vz: VM state changed", "state", "error", "error", err.LocalizedDescription())
			h.doneOnce.Do(func() { close(h.done) })
		},
	})
	vm.SetDelegate(delegate)
}

func (h *vmHandle) startOnQueue(ctx context.Context) error {
	result, ctxErr := dispatchCall(h.queue, ctx, func(done func(error)) {
		h.installDelegateOnQueue(h.vm)
		h.vm.StartWithCompletionHandler(done)
	})
	if ctxErr != nil {
		return ctxErr
	}
	return result
}

func (h *vmHandle) Done() <-chan struct{} { return h.done }

func (h *vmHandle) startVNC(vm virtualization.VZVirtualMachine) error {
	port := h.vncPort
	if port == 0 {
		port = DefaultVNCPort
	}
	h.logger.Info("vz: starting VNC server", "requestedPort", port)
	srv, err := vnc.New(vnc.Config{Port: port, Mode: vnc.SecurityNone})
	if err != nil {
		return fmt.Errorf("creating VNC server: %w", err)
	}
	result, displayErr := srv.StartVirtualMachine(vm)
	h.vncServer = srv
	// The builtin server binds asynchronously: Port() can still report 0
	// right after Start(). Keep the requested port as the connect target and
	// poll briefly for the bound one instead of clobbering it with 0.
	h.vncPort = port
	boundPort := result.Port
	for i := 0; boundPort == 0 && i < 20; i++ {
		time.Sleep(100 * time.Millisecond)
		boundPort = srv.Port()
	}
	if boundPort != 0 {
		h.vncPort = boundPort
	}
	h.logger.Info("vz: VNC server started",
		"requestedPort", port,
		"portAtStart", result.Port,
		"boundPort", boundPort,
		"displayAttached", result.DisplayAttached,
		"stateAtStart", result.State,
		"state", srv.State(),
		"description", srv.Description())
	if displayErr != nil {
		h.logger.Warn("vz: VNC display attach warning", "err", displayErr)
	}
	return nil
}

// Screenshot captures the current VM display via VNC and returns PNG bytes.
func (h *vmHandle) Screenshot() ([]byte, error) {
	if h.vncServer == nil {
		return nil, fmt.Errorf("VNC server not running")
	}
	port := h.vncPort
	if port == 0 {
		port = DefaultVNCPort
	}
	return VNCGrabFrame(fmt.Sprintf("127.0.0.1:%d", port), 10*time.Second)
}

func (h *vmHandle) Stop() error {
	if h.vncServer != nil {
		h.vncServer.Stop()
	}
	if h.proxyListener != nil {
		h.proxyListener.Close()
	}
	if h.proxyWg != nil {
		h.proxyWg.Wait()
	}
	return h.stopOnQueue()
}

func (h *vmHandle) stopOnQueue() error {
	result, _ := dispatchCall(h.queue, context.Background(), func(done func(error)) {
		if h.vm.CanRequestStop() {
			if _, err := h.vm.RequestStopWithError(); err == nil {
				select {
				case <-h.done:
					done(nil)
					return
				case <-time.After(30 * time.Second):
				}
			}
		}
		if h.vm.CanStop() {
			h.vm.StopWithCompletionHandler(done)
			return
		}
		done(nil)
	})
	return result
}

func (h *vmHandle) Wait() error {
	<-h.done
	return nil
}

func (h *vmHandle) SSHAddr() string {
	return fmt.Sprintf("%s:%d", h.sshHost, h.sshPort)
}

func (h *vmHandle) OutputDir() string { return h.outDir }

// VNCPort returns the port the VNC server is listening on, or 0 if not running.
func (h *vmHandle) VNCPort() uint16 {
	if h.vncServer == nil {
		return 0
	}
	return h.vncPort
}

// VNCPortFromVM extracts the VNC port from a VM started by this backend.
// Returns 0 if v was not created by the vz backend or VNC is not running.
func VNCPortFromVM(v winpe.VM) uint16 {
	if h, ok := v.(*vmHandle); ok {
		return h.VNCPort()
	}
	return 0
}

// ScreenshotFromVM captures the current display of a VM started by this backend.
// Returns an error if v was not created by the vz backend or VNC is not running.
func ScreenshotFromVM(v winpe.VM) ([]byte, error) {
	if h, ok := v.(*vmHandle); ok {
		return h.Screenshot()
	}
	return nil, fmt.Errorf("not a vz backend VM")
}
