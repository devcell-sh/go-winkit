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
const PEWSL1DataDiskSizeGB = 8

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

	pwshFiles, err := winpe.FetchPwshFiles(c.CacheDir, func(f string, a ...any) {
		logger.Info(fmt.Sprintf(f, a...))
	})
	if err != nil {
		return err
	}

	logger.Info("building PE boot volume")
	logger.Debug("image sources", "windows", c.WindowsISO, "virtio", c.VirtIOISO)
	baseCfg := winpe.BaseImageConfig{
		WindowsISO: c.WindowsISO,
		VirtIOISO:  c.VirtIOISO,
		GosshdExe:  gosshdExe,
		GosshdAddr: ":2222",
		ServiceExe: serviceExe,
		PwshFiles:  pwshFiles,
		WorkDir:    c.WorkDir,
	}
	if wsl1 {
		baseCfg.StartupCommand = winpe.WSL1PEStartupCommand
	}
	files, err := winpe.BuildBaseImageFiles(baseCfg)
	if err != nil {
		return err
	}

	if !wsl1 {
		return qemu.CreateFATQcow2(c.Dest, files, PECapacity)
	}

	logger.Info("assembling WSL1 WinPE runtime")
	bootWimPath := filepath.Join(c.WorkDir, "stage", "sources", "boot.wim")
	installWimPath := filepath.Join(c.WorkDir, "install.wim")
	if err := winpe.Extract7zToFile(c.WindowsISO, "sources/install.wim", installWimPath); err != nil {
		return fmt.Errorf("extracting install.wim for WSL1: %w", err)
	}
	defer os.Remove(installWimPath)
	transferred, err := winpe.TransferWSL1Files(installWimPath, bootWimPath)
	if err != nil {
		return fmt.Errorf("transferring WSL1 components: %w", err)
	}
	logger.Info("transferred WSL1 components", "files", len(transferred))

	wslDir, err := winpe.FetchWSL1Engine(ctx, c.CacheDir, c.NoCache, func(f string, a ...any) {
		logger.Info(fmt.Sprintf(f, a...))
	})
	if err != nil {
		return err
	}
	patch, err := winpe.WSL1PEPatchSet(c.WorkDir, wslDir)
	if err != nil {
		return err
	}
	if err := winpe.PatchWIM(bootWimPath, patch); err != nil {
		return fmt.Errorf("patching WSL1 boot.wim: %w", err)
	}
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
	logger.Info("creating PE+WSL1 writable disk", "dest", dataDisk, "size_gb", PEWSL1DataDiskSizeGB)
	if err := qemu.CreateDisk(dataDisk, PEWSL1DataDiskSizeGB); err != nil {
		return err
	}
	return WriteArtifact(c.Dest, Artifact{
		Kind:       ArtifactKindPEWSL1,
		BootVolume: c.Dest,
		DataDisk:   dataDisk,
	})
}
