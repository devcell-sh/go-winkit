package build

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPEDataDiskPath(t *testing.T) {
	got := PEDataDiskPath(filepath.Join("out", "winkit-core.qcow2"))
	want := filepath.Join("out", "winkit-core-data.qcow2")
	if got != want {
		t.Fatalf("PEDataDiskPath = %q, want %q", got, want)
	}
}

func TestArtifactRoundTripResolvesRelocatableMedia(t *testing.T) {
	dir := t.TempDir()
	boot := filepath.Join(dir, "winkit-core.qcow2")
	dataDisk := filepath.Join(dir, "winkit-core-data.qcow2")
	for _, path := range []string{boot, dataDisk} {
		if err := os.WriteFile(path, []byte("media"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := WriteArtifact(boot, Artifact{
		Kind:       ArtifactKindPEWSL1,
		BootVolume: boot,
		DataDisk:   dataDisk,
	}); err != nil {
		t.Fatal(err)
	}

	artifact, err := LoadArtifact(boot)
	if err != nil {
		t.Fatal(err)
	}
	if artifact == nil || artifact.Kind != ArtifactKindPEWSL1 {
		t.Fatalf("artifact = %+v", artifact)
	}
	if artifact.BootVolume != boot || artifact.DataDisk != dataDisk {
		t.Fatalf("resolved artifact = %+v", artifact)
	}
}

func TestLoadArtifactMissingIsSingleDisk(t *testing.T) {
	artifact, err := LoadArtifact(filepath.Join(t.TempDir(), "plain.qcow2"))
	if err != nil || artifact != nil {
		t.Fatalf("LoadArtifact = %+v, %v; want nil, nil", artifact, err)
	}
}
