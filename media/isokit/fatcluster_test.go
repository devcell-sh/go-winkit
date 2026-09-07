package isokit

import (
	"bytes"
	"path/filepath"
	"testing"
)

// TestFATClusterBytes pins the go-diskfs size→cluster table the padding relies
// on. If a go-diskfs upgrade changes the thresholds, this fails loudly rather
// than silently shipping mis-aligned (corrupt) answer volumes.
func TestFATClusterBytes(t *testing.T) {
	const MB = 1024 * 1024
	const GB = 1024 * MB
	cases := []struct {
		name          string
		content, want int64
	}{
		{"empty→64MB floor", 0, 512},
		{"small", 100 * MB, 512},
		{"just under 260MB disk", 249 * MB, 512},
		{"past 260MB → 4K", 300 * MB, 4096},
		{"multi-GB → 4K", 5 * GB, 4096},
		{"past 8GB → 8K", 9 * GB, 8192},
	}
	for _, c := range cases {
		if got := FATClusterBytes(c.content); got != c.want {
			t.Errorf("%s: FATClusterBytes(%d)=%d, want %d", c.name, c.content, got, c.want)
		}
	}
}

// TestCreateFATImagePaddedLargeVolume reproduces the wsl answer-volume bug: a
// payload past 260MB makes go-diskfs format 4KB clusters, so a file that is a
// multiple of the old fixed 2048 pad but NOT of 4096 (14336 bytes) used to be
// read back cluster-rounded (16384) and corrupted. Cluster-aware padding fixes
// it. CreateFATImage's own round-trip verification means a nil return already
// proves the stored bytes match; the extra assertions document the intent.
func TestCreateFATImagePaddedLargeVolume(t *testing.T) {
	if testing.Short() {
		t.Skip("writes a >260MB FAT image")
	}
	img := filepath.Join(t.TempDir(), "answer.img")

	// 14336 = 7*2048 = 3.5*4096: aligned to the old boundary, not the new one.
	victim := bytes.Repeat([]byte("x"), 14336)
	// Filler pushes the volume past 260MB so the cluster size becomes 4096,
	// exactly the condition that broke the real build.
	filler := bytes.Repeat([]byte("F"), 300*1024*1024)

	padded := map[string][]byte{
		"/developerReference.xsd": victim,
		"/filler.bin":             filler,
	}
	if err := CreateFATImagePadded(img, padded, nil); err != nil {
		t.Fatalf("CreateFATImagePadded on a 4KB-cluster volume: %v", err)
	}

	got, err := ReadFileFromFAT(img, "/developerReference.xsd")
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(got)%4096 != 0 {
		t.Errorf("stored size %d is not 4KB-cluster aligned", len(got))
	}
	if !bytes.HasPrefix(got, victim) {
		t.Errorf("victim payload corrupted: got %d bytes, want prefix of %d", len(got), len(victim))
	}
}
