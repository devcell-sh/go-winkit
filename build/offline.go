package build

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/devcell-sh/go-regedit"
	"github.com/devcell-sh/go-winkit/vm/qemu"
	"github.com/devcell-sh/go-winkit/winpe"
)

// OfflineImportOpts configures the build-time WSL1 offline import.
type OfflineImportOpts struct {
	// DiskPath is the output qcow2 path for the pre-populated data disk.
	DiskPath string
	// SizeGB is the virtual size of the NTFS data disk.
	SizeGB int
	// DistroTarball is the path to the .wsl/.tar.gz rootfs.
	DistroTarball string
	// DistroName is the WSL distribution name (e.g. "winkit").
	DistroName string
	// User is the Windows user name for the WSL profile.
	User string
	// BootWimPath is the path to the patched boot.wim (for extracting
	// the default NTUSER.DAT).
	BootWimPath string
}

// OfflineImportWSL1 creates a pre-populated NTFS data disk for WinPE WSL1.
// The disk contains:
//   - The extracted distro rootfs at wsl-<distroName>/rootfs/
//   - A user profile at Users/<user>/NTUSER.DAT with Lxss registry entries
//   - Required directory structure (Users/<user>/AppData/Local/Temp)
//
// At boot, the bootstrap detects the pre-populated disk and skips diskpart
// and wsl --import.
func OfflineImportWSL1(opts OfflineImportOpts) error {
	workDir := filepath.Dir(opts.DiskPath)

	ntuser, err := extractDefaultNTUSER(opts.BootWimPath, workDir)
	if err != nil {
		return fmt.Errorf("extracting default NTUSER.DAT: %w", err)
	}
	defer os.Remove(ntuser)

	if err := patchNTUSERLxss(ntuser, opts.DistroName); err != nil {
		return fmt.Errorf("patching NTUSER.DAT: %w", err)
	}

	distroAbs, err := filepath.Abs(opts.DistroTarball)
	if err != nil {
		return err
	}
	ntuserAbs, err := filepath.Abs(ntuser)
	if err != nil {
		return err
	}

	distroTarget := fmt.Sprintf("wsl-%s", opts.DistroName)

	return qemu.CreatePopulatedNTFSQcow2(opts.DiskPath, opts.SizeGB, func(rawPath string) error {
		rawDir := filepath.Dir(rawPath)
		rawName := filepath.Base(rawPath)

		stagedTarball := filepath.Join(rawDir, "distro-stage.wsl")
		stagedNTUSER := filepath.Join(rawDir, "NTUSER-stage.DAT")
		if err := copyFile(distroAbs, stagedTarball); err != nil {
			return err
		}
		defer os.Remove(stagedTarball)
		if err := copyFile(ntuserAbs, stagedNTUSER); err != nil {
			return err
		}
		defer os.Remove(stagedNTUSER)

		script := strings.Join([]string{
			"apk add --no-cache tar gzip >/dev/null 2>&1",
			fmt.Sprintf("mkdir -p /mnt/ntfs/%s/rootfs", distroTarget),
			fmt.Sprintf("tar xzf /work/distro-stage.wsl -C /mnt/ntfs/%s/rootfs", distroTarget),
			fmt.Sprintf("mkdir -p '/mnt/ntfs/Users/%s/AppData/Local/Temp'", opts.User),
			fmt.Sprintf("cp /work/NTUSER-stage.DAT '/mnt/ntfs/Users/%s/NTUSER.DAT'", opts.User),
			"mkdir -p '/mnt/ntfs/Program Files/WSL'",
			"mkdir -p /mnt/ntfs/winkit",
		}, " && ")

		return qemu.DockerNTFSFormat(filepath.Join(rawDir, rawName), "WSLROOT", script)
	})
}

const lxssDistroGUID = "{a1b2c3d4-e5f6-7890-abcd-ef0123456789}"

func extractDefaultNTUSER(bootWimPath, workDir string) (string, error) {
	destDir := filepath.Join(workDir, "ntuser-extract")
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", err
	}
	defer os.RemoveAll(destDir)

	cmd := exec.Command("wimextract", bootWimPath, "2",
		"/Users/Default/NTUSER.DAT", "--dest-dir="+destDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("wimextract NTUSER.DAT: %w\n%s", err, out)
	}

	src := filepath.Join(destDir, "NTUSER.DAT")
	dst := filepath.Join(workDir, "NTUSER-offline.DAT")
	if err := copyFile(src, dst); err != nil {
		return "", err
	}
	return dst, nil
}

func patchNTUSERLxss(ntuserPath, distroName string) error {
	distroDir := fmt.Sprintf(`E:\wsl-%s`, distroName)

	lxssKey := &regedit.Key{
		Values: map[string]regedit.Value{
			"DefaultVersion":      winpe.DwordValue(1),
			"NewDistributionLxFs": winpe.DwordValue(0),
			"DefaultDistribution": winpe.SzValue(lxssDistroGUID),
		},
		Subkeys: map[string]*regedit.Key{
			lxssDistroGUID: {
				Values: map[string]regedit.Value{
					"DistributionName":  winpe.SzValue(distroName),
					"BasePath":          winpe.SzValue(distroDir),
					"State":             winpe.DwordValue(1),
					"Version":           winpe.DwordValue(1),
					"DefaultUid":        winpe.DwordValue(0),
					"Flags":             winpe.DwordValue(0x0f),
					"PackageFamilyName": winpe.SzValue(""),
				},
			},
		},
	}

	if err := regedit.WriteKey(ntuserPath, `Software\Microsoft\Windows\CurrentVersion\Lxss`, lxssKey); err != nil {
		return fmt.Errorf("writing Lxss key: %w", err)
	}
	return nil
}

