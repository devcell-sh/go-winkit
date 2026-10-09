package build

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/devcell-sh/go-winkit/vm"
	"github.com/devcell-sh/go-winkit/vm/qemu"
	"github.com/devcell-sh/go-winkit/winpe"
	"github.com/devcell-sh/go-winkit/wsl"
)

// PECapacity is the virtual size of the FAT boot volume a PE build
// produces: large enough for boot.wim plus injected payloads.
const PECapacity = 4 * 1024 * 1024 * 1024

// PEWSL1DataDiskSizeGB is the writable NTFS disk paired with a PE+WSL1
// boot volume. It holds the relocated runtime, 4 GB pagefile, user profile,
// and imported distro.
// PEWSL1DataDiskSizeGB sizes the writable NTFS disk paired with a PE+WSL1
// boot volume. The qcow2 is sparse, so this is a ceiling, not an
// allocation. The bootstrap places a 4 GB pagefile on it and `wsl --import`
// unpacks the rootfs next to it; a Nix rootfs with a desktop closure runs
// past the old 8 GB, which failed the import with "No space left on device".
const PEWSL1DataDiskSizeGB = 32

// PE assembles a WinPE boot volume at c.Dest: gosshd cross-compiled for
// the guest, PowerShell 7 payload, and the boot files from the
// Windows/virtio ISOs, mastered onto a FAT32 qcow2. No VM boots; the
// volume is ephemeral and provisioned at first boot over gosshd.
func PE(ctx context.Context, c Config) error {
	logger := c.logger()
	if c.Opts != nil {
		if err := c.Opts.Validate(); err != nil {
			return err
		}
	}
	wsl1 := c.Opts != nil && c.Opts.PE && c.Opts.WSL != nil
	// A rebuilt single-disk PE must not retain a stale multi-disk manifest.
	if err := os.Remove(ArtifactManifestPath(c.Dest)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing stale artifact manifest: %w", err)
	}

	gosshdExe := filepath.Join(c.WorkDir, "gosshd.exe")
	const arch = "arm64"
	logger.Info("cross-compiling gosshd", "target", "windows/"+arch)
	if err := winpe.CrossCompileGosshd(gosshdExe, arch); err != nil {
		return err
	}

	serviceExe := filepath.Join(c.WorkDir, "winkit-service.exe")
	logger.Info("cross-compiling winkit-service", "target", "windows/"+arch)
	if err := winpe.CrossCompileService(serviceExe, arch); err != nil {
		return err
	}

	dwmEnabled := c.Opts != nil && c.Opts.DWM

	var implorerExe string
	if dwmEnabled {
		if implorerPath := filepath.Join(c.WorkDir, "implorer.exe"); true {
			if err := winpe.CrossCompileImplorer(implorerPath, arch); err == nil {
				implorerExe = implorerPath
				logger.Info("implorer desktop shell available", "target", "windows/"+arch)
			}
		}
	}

	pwshFiles, err := winpe.FetchPwshFiles(c.CacheDir, func(f string, a ...any) {
		logger.Info(fmt.Sprintf(f, a...))
	})
	if err != nil {
		return err
	}

	logf := func(f string, a ...any) { logger.Info(fmt.Sprintf(f, a...)) }

	var webView2Files map[string][]byte
	var chromiumFiles map[string][]byte
	var contentShellFiles map[string][]byte
	if dwmEnabled {
		wv2, err := winpe.FetchWebView2Files(c.CacheDir, logf)
		if err != nil {
			return fmt.Errorf("fetching WebView2 runtime: %w", err)
		}
		webView2Files = wv2

		chromiumFiles, err = winpe.FetchChromiumFiles(c.CacheDir, logf)
		if err != nil {
			return fmt.Errorf("fetching Chromium: %w", err)
		}

		contentShellFiles, err = winpe.FetchContentShellFiles(c.CacheDir, logf)
		if err != nil {
			return fmt.Errorf("fetching content-shell: %w", err)
		}

		if n := winpe.MergeContentShellDeps(chromiumFiles, contentShellFiles); n > 0 {
			logger.Debug("merged Chrome deps into content-shell", "files", n)
		}
	}

	logger.Info("building PE boot volume")
	logger.Debug("image sources", "windows", c.WindowsISO, "virtio", c.VirtIOISO)
	baseCfg := winpe.BaseImageConfig{
		WindowsISO:        c.WindowsISO,
		VirtIOISO:         c.VirtIOISO,
		GosshdExe:         gosshdExe,
		GosshdAddr:        ":2222",
		ServiceExe:        serviceExe,
		ImplorerExe:       implorerExe,
		PwshFiles:         pwshFiles,
		WebView2Files:     webView2Files,
		ChromiumFiles:     chromiumFiles,
		ContentShellFiles: contentShellFiles,
		WorkDir:           c.WorkDir,
	}
	if c.Opts != nil {
		baseCfg.ChocolateyPackages = c.Opts.Packages.Chocolatey
	}
	if wsl1 {
		baseCfg.StartupCommand = winpe.WSL1PEStartupCommand
	}
	files, err := winpe.BuildBaseImageFiles(baseCfg)
	if err != nil {
		return err
	}

	// TODO: re-enable hostname patch after isolating crash.
	// hostname := winpe.GuestHostname("")
	// if c.Opts != nil && c.Opts.Hostname != "" {
	// 	hostname = c.Opts.Hostname
	// }
	bootWimPath := filepath.Join(c.WorkDir, "stage", "sources", "boot.wim")

	if !wsl1 {
		// TODO: re-enable hostname patch after isolating crash.
		// logger.Info("setting PE hostname", "hostname", hostname)
		// if err := winpe.PatchWIM(bootWimPath, winpe.HostnamePatchSet(2, hostname)); err != nil {
		// 	return fmt.Errorf("patching hostname: %w", err)
		// }
		// patchedBootWim, err := os.ReadFile(bootWimPath)
		// if err != nil {
		// 	return fmt.Errorf("reading patched boot.wim: %w", err)
		// }
		// files["/sources/boot.wim"] = patchedBootWim
		return qemu.CreateFATQcow2(c.Dest, files, PECapacity)
	}

	// The WSL1 runtime is assembled from three sources: the WSL1 optional
	// component carried in install.wim, the WSL engine MSI from GitHub,
	// and winkit's own PowerShell helpers. Each is its own step because
	// extracting install.wim and rewriting boot.wim are the slow parts.
	logger.Info("extracting install.wim from Windows ISO")
	installWimPath := filepath.Join(c.WorkDir, "install.wim")
	if err := winpe.Extract7zToFile(c.WindowsISO, "sources/install.wim", installWimPath); err != nil {
		return fmt.Errorf("extracting install.wim for WSL1: %w", err)
	}
	defer os.Remove(installWimPath)

	logger.Info("transferring WSL1 components into boot.wim")
	transferred, err := winpe.TransferWSL1Files(installWimPath, bootWimPath)
	if err != nil {
		return fmt.Errorf("transferring WSL1 components: %w", err)
	}
	logger.Debug("transferred WSL1 components", "files", len(transferred))

	if dwmEnabled {
		logger.Info("transferring DWM compositor into boot.wim")
		dwmTransferred, err := winpe.TransferDWMFiles(installWimPath, bootWimPath)
		if err != nil {
			return fmt.Errorf("transferring DWM components: %w", err)
		}
		logger.Debug("transferred DWM components", "files", len(dwmTransferred))
	}

	pkgs := c.Opts.Packages
	if pkgs.NeedsNetFx() {
		logger.Info("transferring .NET Framework into boot.wim")
		netfxTransferred, err := winpe.TransferNetFxFiles(installWimPath, bootWimPath)
		if err != nil {
			return fmt.Errorf("transferring .NET Framework: %w", err)
		}
		logger.Debug("transferred .NET Framework components", "files", len(netfxTransferred))
	}
	if pkgs.NeedsWoW64() {
		logger.Info("transferring WoW64 x64 emulation into boot.wim")
		wow64Transferred, err := winpe.TransferWoW64Files(installWimPath, bootWimPath)
		if err != nil {
			return fmt.Errorf("transferring WoW64 components: %w", err)
		}
		logger.Debug("transferred WoW64 components", "files", len(wow64Transferred))
	}

	logger.Info("patching boot.wim pagefile to use persistent data disk")
	if err := winpe.PatchWIM(bootWimPath, winpe.PagefilePatchSet()); err != nil {
		return fmt.Errorf("patching pagefile registry: %w", err)
	}

	wslDir, err := winpe.FetchWSL1Engine(ctx, c.CacheDir, c.NoCache, func(f string, a ...any) {
		logger.Info(fmt.Sprintf(f, a...))
	})
	if err != nil {
		return err
	}
	logger.Info("patching boot.wim with WSL1 engine and helpers")
	patch, err := winpe.WSL1PEPatchSet(c.WorkDir, wslDir)
	if err != nil {
		return err
	}
	if err := winpe.PatchWIM(bootWimPath, patch); err != nil {
		return fmt.Errorf("patching WSL1 boot.wim: %w", err)
	}

	if dwmEnabled {
		logger.Info("patching boot.wim with DWM service registry")
		if err := winpe.PatchWIM(bootWimPath, winpe.DWMServicePatchSet()); err != nil {
			return fmt.Errorf("patching DWM boot.wim: %w", err)
		}
	}
	// TODO: re-enable hostname patch after isolating crash.
	// logger.Info("setting PE hostname", "hostname", hostname)
	// if err := winpe.PatchWIM(bootWimPath, winpe.HostnamePatchSet(2, hostname)); err != nil {
	// 	return fmt.Errorf("patching hostname: %w", err)
	// }
	patchedBootWim, err := os.ReadFile(bootWimPath)
	if err != nil {
		return fmt.Errorf("reading patched WSL1 boot.wim: %w", err)
	}
	files["/sources/boot.wim"] = patchedBootWim

	image := c.WSLImage
	if image == "" {
		image = c.Opts.WSL.Image
	}
	distro, err := wsl.DistroFor(image, winpe.WSL1PEUserName, winpe.WSL1PEDistroName, c.NixHome, c.NixHomeOpts)
	if err != nil {
		return err
	}
	// Extra s6 services bake into the rootfs exactly as in the full image;
	// the PE bootstrap starts s6-svscan through winkit-service once the
	// distro is imported. PutService lets a user service override a
	// built-in of the same name instead of erroring.
	for _, svc := range c.WSLServices {
		if err := distro.PutService(svc); err != nil {
			return fmt.Errorf("adding s6 service %s: %w", svc.Name, err)
		}
	}
	distroPath, err := distro.Materialize(ctx, c.CacheDir, c.NoCache, func(f string, a ...any) {
		logger.Info(fmt.Sprintf(f, a...))
	})
	if err != nil {
		return fmt.Errorf("materializing PE WSL1 distro: %w", err)
	}
	distroData, err := os.ReadFile(distroPath)
	if err != nil {
		return fmt.Errorf("reading PE WSL1 distro: %w", err)
	}
	files["/distro.wsl"] = distroData

	logger.Info("creating PE+WSL1 boot volume", "dest", c.Dest)
	if err := qemu.CreateFATQcow2(c.Dest, files, PECapacity); err != nil {
		return err
	}
	dataDisk := PEDataDiskPath(c.Dest)
	if c.OfflineImport {
		logger.Info("offline import: creating pre-populated NTFS data disk", "dest", dataDisk)
		if err := OfflineImportWSL1(OfflineImportOpts{
			DiskPath:      dataDisk,
			SizeGB:        PEWSL1DataDiskSizeGB,
			DistroTarball: distroPath,
			DistroName:    winpe.WSL1PEDistroName,
			User:          winpe.WSL1PEUserName,
			BootWimPath:   bootWimPath,
		}); err != nil {
			return fmt.Errorf("offline import: %w", err)
		}
	} else {
		logger.Info("creating PE+WSL1 writable disk", "dest", dataDisk, "size_gb", PEWSL1DataDiskSizeGB)
		if err := qemu.CreateDisk(dataDisk, PEWSL1DataDiskSizeGB); err != nil {
			return err
		}
	}
	// Bootstrap phase: boot the PE image and run WSL1 bootstrap inside
	// the guest. The host monitors the structured log for completion.
	if c.Opts != nil && c.Opts.BootstrapOnBuild {
		logger.Info("bootstrap: booting PE to run WSL1 bootstrap")
		if err := BootstrapPE(ctx, c, c.Dest, dataDisk); err != nil {
			return fmt.Errorf("bootstrap phase: %w", err)
		}
		logger.Info("bootstrap: complete")
	}

	artifact := Artifact{
		Kind:       ArtifactKindPEWSL1,
		BootVolume: c.Dest,
		DataDisk:   dataDisk,
	}
	if c.Opts != nil {
		artifact.Forwards = append(artifact.Forwards, c.Opts.Ports.Forward...)
	}
	return WriteArtifact(c.Dest, artifact)
}

// BootstrapPE boots the PE image during the build, waits for the WSL1
// bootstrap to complete (monitoring the structured log for the terminal
// event), then shuts down QEMU. This moves the heavy provisioning work
// out of `winkit start` into the build.
func BootstrapPE(ctx context.Context, c Config, bootVolume, dataDisk string) error {
	logger := c.logger()
	bootstrapDir := filepath.Join(c.WorkDir, "bootstrap")
	os.MkdirAll(bootstrapDir, 0o755)

	guestJSONL := filepath.Join(bootstrapDir, "guest.jsonl")

	fwPath := qemu.FirmwarePath()
	if fwPath == "" {
		return fmt.Errorf("no UEFI firmware found")
	}
	varsPath := filepath.Join(bootstrapDir, "vars.fd")
	if err := qemu.PrepareVarsFile(fwPath, varsPath); err != nil {
		return fmt.Errorf("preparing bootstrap vars: %w", err)
	}

	accel := c.Accel
	if accel == "" {
		accel = qemu.DefaultAccel()
	}

	runCfg := vm.VMRunConfig{
		DiskPath:     dataDisk,
		OutputDir:    bootstrapDir,
		VMName:       "winkit-bootstrap",
		CPUs:         4,
		MemoryGB:     4,
		Accel:        accel,
		SSHPort:      20022,
		SSHGuestPort: 2222,
		BackendExtra: &qemu.RunOptions{
			BootVolume:        bootVolume,
			VarsPath:          varsPath,
			Secure:            false,
			DisplayType:       "none",
			StructuredLogPath: guestJSONL,
		},
	}

	backend := &qemu.Backend{}
	machine, err := backend.StartRun(ctx, runCfg)
	if err != nil {
		return fmt.Errorf("starting bootstrap VM: %w", err)
	}
	defer machine.Stop()

	stopTail := make(chan struct{})
	go tailStream(filepath.Join(machine.OutputDir(), "serial.log"), "serial", logger, stopTail)
	go tailStream(filepath.Join(machine.OutputDir(), "qemu.log"), "qemu", logger, stopTail)
	go tailStream(guestJSONL, "bootstrap", logger, stopTail)
	qmpSock := qemu.QMPSocketFromVM(machine)

	logger.Info("bootstrap: waiting for WSL1 bootstrap to complete", "guest_log", guestJSONL)
	err = waitForBootstrapEvent(ctx, guestJSONL, machine.Done(), 45*time.Minute)

	if err == nil {
		logger.Info("bootstrap: event received, waiting for guest filesystem flush")
		time.Sleep(10 * time.Second)
	}

	close(stopTail)

	if qmpSock != "" {
		_ = qemu.QMPQuit(qmpSock)
		time.Sleep(3 * time.Second)
	}

	return err
}

// waitForBootstrapEvent monitors the guest structured log file for the
// terminal bootstrap event. Returns nil on success.
func waitForBootstrapEvent(ctx context.Context, logPath string, vmDone <-chan struct{}, timeout time.Duration) error {
	deadline := time.After(timeout)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	var offset int64
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return fmt.Errorf("bootstrap timed out after %v", timeout)
		case <-vmDone:
			if ev, ok := scanForEvent(logPath, &offset); ok {
				return ev
			}
			return fmt.Errorf("VM exited before bootstrap completed")
		case <-ticker.C:
			if ev, ok := scanForEvent(logPath, &offset); ok {
				return ev
			}
		}
	}
}

func scanForEvent(logPath string, offset *int64) (error, bool) {
	f, err := os.Open(logPath)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	if _, err := f.Seek(*offset, 0); err != nil {
		return nil, false
	}

	buf, err := io.ReadAll(f)
	if err != nil || len(buf) == 0 {
		return nil, false
	}
	*offset += int64(len(buf))

	for _, line := range strings.Split(string(buf), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev struct {
			Event string `json:"event"`
			Msg   string `json:"msg"`
		}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		switch ev.Event {
		case "init-bootstrap-complete":
			return nil, true
		case "init-bootstrap-failed", "wsl1-bootstrap-failed", "bootstrap-wsl1-error":
			return fmt.Errorf("bootstrap failed: %s", ev.Msg), true
		}
		if strings.Contains(ev.Msg, "WSL1_BOOTSTRAP_FAILED") {
			return fmt.Errorf("bootstrap failed: %s", ev.Msg), true
		}
	}
	return nil, false
}
