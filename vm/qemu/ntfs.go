package qemu

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// CreatePopulatedNTFSQcow2 creates a qcow2 disk from a raw image. populate
// receives the raw disk path; the caller is responsible for formatting and
// writing content (e.g. via DockerNTFSFormat). The raw image is deleted
// after conversion.
func CreatePopulatedNTFSQcow2(qcow2Path string, sizeGB int, populate func(rawPath string) error) error {
	if err := os.MkdirAll(filepath.Dir(qcow2Path), 0o755); err != nil {
		return err
	}
	rawPath := qcow2Path + ".ntfs.raw"

	f, err := os.Create(rawPath)
	if err != nil {
		return fmt.Errorf("creating raw disk: %w", err)
	}
	if err := f.Truncate(int64(sizeGB) * 1024 * 1024 * 1024); err != nil {
		f.Close()
		return fmt.Errorf("sizing raw disk: %w", err)
	}
	f.Close()
	defer os.Remove(rawPath)

	if err := populate(rawPath); err != nil {
		return fmt.Errorf("populating NTFS: %w", err)
	}

	cmd := exec.Command("qemu-img", "convert", "-f", "raw", "-O", "qcow2", rawPath, qcow2Path)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("converting raw to qcow2: %w\n%s", err, out)
	}
	return nil
}

// DockerNTFSFormat formats a raw disk with NTFS and populates it inside a
// privileged Docker container (ntfs-3g provides both mkntfs and the FUSE
// mount). The disk file must be under a directory visible to the Docker
// daemon.
func DockerNTFSFormat(rawDiskPath, label, script string) error {
	absPath, err := filepath.Abs(rawDiskPath)
	if err != nil {
		return err
	}
	workDir := filepath.Dir(absPath)
	hostWorkDir := dockerHostDir(workDir)
	diskName := filepath.Base(absPath)

	fullScript := strings.Join([]string{
		"set -e",
		"apk add --no-cache ntfs-3g >/dev/null 2>&1",
		fmt.Sprintf("mkntfs -F -Q -L '%s' /work/%s", label, diskName),
		"mkdir -p /mnt/ntfs",
		fmt.Sprintf("ntfs-3g /work/%s /mnt/ntfs", diskName),
		script,
	}, " && ")
	// Ensure umount runs even on failure.
	wrapped := fmt.Sprintf("(%s); RET=$?; umount /mnt/ntfs 2>/dev/null; exit $RET", fullScript)

	cmd := exec.Command("docker", "run", "--rm", "--privileged",
		"-v", hostWorkDir+":/work",
		"alpine:3.21", "sh", "-c", wrapped,
	)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker ntfs populate: %w", err)
	}
	return nil
}

// dockerHostDir translates a container-local path to the path the Docker
// daemon sees. In Docker-in-Docker (devcell containers), the daemon runs on
// the host; DEVCELL_HOST_PROJECT_DIR provides the mapping.
func dockerHostDir(containerDir string) string {
	hostProjectDir := os.Getenv("DEVCELL_HOST_PROJECT_DIR")
	if hostProjectDir == "" {
		return containerDir
	}
	dir := containerDir
	for {
		if _, err := os.Stat(filepath.Join(dir, ".devcell.toml")); err == nil {
			rel, err := filepath.Rel(dir, containerDir)
			if err != nil {
				return containerDir
			}
			return filepath.Join(hostProjectDir, rel)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return containerDir
}
