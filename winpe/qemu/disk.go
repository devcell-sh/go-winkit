package qemu

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// CreateDisk creates a qcow2 disk image of the given size.
func CreateDisk(path string, sizeGB int) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	cmd := exec.Command("qemu-img", "create", "-f", "qcow2", path, fmt.Sprintf("%dG", sizeGB))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("qemu-img create: %w\n%s", err, out)
	}
	return nil
}

// CreateOverlay creates a copy-on-write qcow2 at path backed by base. Writes
// land in the overlay; base is never modified. base must be an absolute path so
// the overlay resolves it regardless of the working directory.
func CreateOverlay(path, base string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	abs, err := filepath.Abs(base)
	if err != nil {
		return err
	}
	cmd := exec.Command("qemu-img", "create", "-f", "qcow2", "-b", abs, "-F", "qcow2", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("qemu-img create overlay: %w\n%s", err, out)
	}
	return nil
}

// FlattenQcow2 writes a standalone qcow2 at dst that merges src and its backing
// chain, so the result no longer depends on the base image. Used to bake an
// overlay (a continue-mode session's changes) into a self-contained artifact.
func FlattenQcow2(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	cmd := exec.Command("qemu-img", "convert", "-O", "qcow2", src, dst)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("qemu-img convert (flatten): %w\n%s", err, out)
	}
	return nil
}

// QEMUBinaryPath returns the path to qemu-system-aarch64.
func QEMUBinaryPath() (string, error) {
	path, err := exec.LookPath("qemu-system-aarch64")
	if err != nil {
		return "", fmt.Errorf("qemu-system-aarch64 not found in PATH")
	}
	return path, nil
}

// FirmwarePath returns the EDK2 UEFI firmware path.
func FirmwarePath() string {
	if p := firmwareFromBinary(); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	for _, p := range firmwareCandidates(home) {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "/usr/share/AAVMF/AAVMF_CODE.fd"
}

// KernelFirmwarePath returns the EDK2 firmware suitable for -kernel loading
// (the EL3/secure-world boot stub). Returns "" if not found.
func KernelFirmwarePath() string {
	if p := firmwareFromBinary(); p != "" {
		dir := filepath.Dir(p)
		for _, name := range []string{"edk2-aarch64-code.kernel.fd", "QEMU_EFI.kernel.fd"} {
			c := filepath.Join(dir, name)
			if _, err := os.Stat(c); err == nil {
				return c
			}
		}
	}
	home, _ := os.UserHomeDir()
	for _, p := range kernelFirmwareCandidates(home) {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func kernelFirmwareCandidates(home string) []string {
	return []string{
		filepath.Join(home, ".winkit", "cache", "qemu", "QEMU_EFI.kernel.fd"),
		filepath.Join(home, ".cache", "winkit", "QEMU_EFI.kernel.fd"),
	}
}

// PrepareVarsFile copies the firmware to create a writable vars store.
func PrepareVarsFile(firmwarePath, varsPath string) error {
	if err := os.MkdirAll(filepath.Dir(varsPath), 0o755); err != nil {
		return fmt.Errorf("creating vars directory: %w", err)
	}
	src, err := os.ReadFile(firmwarePath)
	if err != nil {
		return fmt.Errorf("reading firmware: %w", err)
	}
	return os.WriteFile(varsPath, src, 0o644)
}

func firmwareFromBinary() string {
	bin, err := exec.LookPath("qemu-system-aarch64")
	if err != nil {
		return ""
	}
	real, err := filepath.EvalSymlinks(bin)
	if err != nil {
		return ""
	}
	p := filepath.Join(filepath.Dir(real), "..", "share", "qemu", "edk2-aarch64-code.fd")
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

func firmwareCandidates(home string) []string {
	candidates := []string{
		filepath.Join(home, ".winkit", "cache", "qemu", "edk2-aarch64-code.fd"),
	}
	if runtime.GOOS == "darwin" {
		candidates = append(candidates,
			"/opt/homebrew/share/qemu/edk2-aarch64-code.fd",
			"/usr/local/share/qemu/edk2-aarch64-code.fd",
		)
	} else {
		candidates = append(candidates,
			"/usr/share/AAVMF/AAVMF_CODE.fd",
			"/usr/share/qemu-efi-aarch64/QEMU_EFI.fd",
		)
	}
	return candidates
}
