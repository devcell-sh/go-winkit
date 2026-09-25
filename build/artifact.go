package build

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// ArtifactSchemaVersion is the on-disk sidecar schema understood by winkit.
	ArtifactSchemaVersion = 1
	// ArtifactKindPEWSL1 is a WinPE boot volume paired with a writable NTFS disk.
	ArtifactKindPEWSL1 = "pe-wsl1"
)

// Artifact describes the media that make up a built image. Most winkit builds
// are a single disk and need no manifest. PE+WSL1 needs two devices: the FAT
// boot volume and an ordinary writable disk for the WSL runtime and distro.
// Paths in the JSON file are relative to the manifest directory.
type Artifact struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"`
	BootVolume    string `json:"boot_volume"`
	DataDisk      string `json:"data_disk"`
}

// ArtifactManifestPath returns the sidecar manifest path for image.
func ArtifactManifestPath(image string) string { return image + ".winkit.json" }

// PEDataDiskPath returns the conventional writable-disk path paired with a
// PE+WSL1 boot volume.
func PEDataDiskPath(bootVolume string) string {
	ext := filepath.Ext(bootVolume)
	return strings.TrimSuffix(bootVolume, ext) + "-data" + ext
}

// WriteArtifact writes an artifact manifest next to image. Media paths are
// stored as basenames so the bundle remains relocatable as one directory.
func WriteArtifact(image string, artifact Artifact) error {
	artifact.SchemaVersion = ArtifactSchemaVersion
	artifact.BootVolume = filepath.Base(artifact.BootVolume)
	artifact.DataDisk = filepath.Base(artifact.DataDisk)
	if err := artifact.validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding artifact manifest: %w", err)
	}
	data = append(data, '\n')
	path := ArtifactManifestPath(image)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("writing artifact manifest: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("installing artifact manifest: %w", err)
	}
	return nil
}

// LoadArtifact reads image's optional manifest and resolves its media paths.
// A nil artifact with no error means image is a conventional single disk.
func LoadArtifact(image string) (*Artifact, error) {
	path := ArtifactManifestPath(image)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading artifact manifest %s: %w", path, err)
	}
	var artifact Artifact
	if err := json.Unmarshal(data, &artifact); err != nil {
		return nil, fmt.Errorf("parsing artifact manifest %s: %w", path, err)
	}
	if err := artifact.validate(); err != nil {
		return nil, fmt.Errorf("artifact manifest %s: %w", path, err)
	}
	dir := filepath.Dir(path)
	artifact.BootVolume = resolveArtifactPath(dir, artifact.BootVolume)
	artifact.DataDisk = resolveArtifactPath(dir, artifact.DataDisk)
	for label, mediaPath := range map[string]string{
		"boot volume": artifact.BootVolume,
		"data disk":   artifact.DataDisk,
	} {
		if _, err := os.Stat(mediaPath); err != nil {
			return nil, fmt.Errorf("artifact %s %s: %w", label, mediaPath, err)
		}
	}
	return &artifact, nil
}

func resolveArtifactPath(dir, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(dir, path)
}

func (a Artifact) validate() error {
	if a.SchemaVersion != ArtifactSchemaVersion {
		return fmt.Errorf("unsupported schema_version %d", a.SchemaVersion)
	}
	if a.Kind != ArtifactKindPEWSL1 {
		return fmt.Errorf("unsupported artifact kind %q", a.Kind)
	}
	if a.BootVolume == "" || a.DataDisk == "" {
		return fmt.Errorf("boot_volume and data_disk are required")
	}
	return nil
}
