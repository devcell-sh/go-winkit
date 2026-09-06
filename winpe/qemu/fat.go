package qemu

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/devcell-sh/go-winkit/isokit"
)

// CreateFATQcow2 creates a qcow2 disk with a FAT32 filesystem containing
// the given files. The virtual capacity is set to capacity bytes but the
// on-disk file is sparse.
func CreateFATQcow2(imgPath string, files map[string][]byte, capacity int64) error {
	rawPath := imgPath + ".raw"
	if err := isokit.CreateFATImageSized(rawPath, files, capacity); err != nil {
		return fmt.Errorf("creating raw FAT32: %w", err)
	}
	defer os.Remove(rawPath)

	cmd := exec.Command("qemu-img", "convert", "-f", "raw", "-O", "qcow2", rawPath, imgPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("converting raw to qcow2: %w\n%s", err, out)
	}
	return nil
}

// ReadFileFromFATQcow2 reads a single file from a qcow2-backed FAT32 image.
func ReadFileFromFATQcow2(imgPath, filePath string) ([]byte, error) {
	tmpDir, err := os.MkdirTemp("", "qcow2-read-")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	rawPath := filepath.Join(tmpDir, "disk.raw")
	cmd := exec.Command("qemu-img", "convert", "-f", "qcow2", "-O", "raw", imgPath, rawPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("converting qcow2 to raw: %w\n%s", err, out)
	}
	return isokit.ReadFileFromFAT(rawPath, filePath)
}
