package winkit

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devcell-sh/go-winkit/vm/vmstate"
)

func TestStart_RequiresImage(t *testing.T) {
	_, err := Start(context.Background(), StartOpts{})
	if err == nil || !strings.Contains(err.Error(), "Image") {
		t.Fatalf("expected Image error, got %v", err)
	}
}

func TestStart_MissingImage(t *testing.T) {
	_, err := Start(context.Background(), StartOpts{
		Image:    filepath.Join(t.TempDir(), "gone.qcow2"),
		StateDir: t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "image not found") {
		t.Fatalf("expected image-not-found error, got %v", err)
	}
}

func TestStart_RejectsAlreadyRunning(t *testing.T) {
	dir := t.TempDir()
	image := filepath.Join(dir, "winkit-base.qcow2")
	if err := os.WriteFile(image, []byte("disk"), 0o644); err != nil {
		t.Fatal(err)
	}
	stateDir := t.TempDir()

	// Register a "running" VM for this image using our own live PID.
	if err := vmstate.Save(stateDir, &vmstate.State{
		Name:      "winkit-base",
		ImagePath: image,
		PID:       os.Getpid(),
		Backend:   "qemu",
		StartedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	_, err := Start(context.Background(), StartOpts{Image: image, StateDir: stateDir})
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("expected already-running error, got %v", err)
	}
}
