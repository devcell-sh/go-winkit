package build

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

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

	var implorerExe string
	if implorerPath := filepath.Join(c.WorkDir, "implorer.exe"); true {
		if err := winpe.CrossCompileImplorer(implorerPath, arch); err == nil {
			implorerExe = implorerPath
			logger.Info("implorer desktop shell available", "target", "windows/"+arch)
		}
	}

	pwshFiles, err := winpe.FetchPwshFiles(c.CacheDir, func(f string, a ...any) {
		logger.Info(fmt.Sprintf(f, a...))
	})
	if err != nil {
		return err
	}

	var webView2Files map[string][]byte
	if implorerExe != "" {
		wv2, err := winpe.FetchWebView2Files(c.CacheDir, func(f string, a ...any) {
			logger.Info(fmt.Sprintf(f, a...))
		})
		if err != nil {
			return fmt.Errorf("fetching WebView2 runtime: %w", err)
		}
		webView2Files = wv2
	}

	logf := func(f string, a ...any) { logger.Info(fmt.Sprintf(f, a...)) }

	chromiumFiles, err := winpe.FetchChromiumFiles(c.CacheDir, logf)
	if err != nil {
		return fmt.Errorf("fetching Chromium: %w", err)
	}

	contentShellFiles, err := winpe.FetchContentShellFiles(c.CacheDir, logf)
	if err != nil {
		return fmt.Errorf("fetching content-shell: %w", err)
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

	logger.Info("transferring DWM compositor into boot.wim")
	dwmTransferred, err := winpe.TransferDWMFiles(installWimPath, bootWimPath)
	if err != nil {
		return fmt.Errorf("transferring DWM components: %w", err)
	}
	logger.Debug("transferred DWM components", "files", len(dwmTransferred))

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

	logger.Info("patching boot.wim with DWM service registry")
	if err := winpe.PatchWIM(bootWimPath, winpe.DWMServicePatchSet()); err != nil {
		return fmt.Errorf("patching DWM boot.wim: %w", err)
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
	distro, err := wsl.DistroFor(image, winpe.WSL1PEUserName, winpe.WSL1PEDistroName, c.NixHome)
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
