package winpe

import (
	"context"
	"fmt"
)

// Runner boots a WinPE guest from the artifacts winpe.Build() produces.
// Implementations are hypervisor-specific (QEMU, libvirt, Hyper-V).
type Runner interface {
	Boot(ctx context.Context, spec BootSpec) (Guest, error)
}

// BootSpec describes what the Runner needs to start a WinPE VM.
type BootSpec struct {
	WinPEISO    string
	SharedFiles map[string][]byte
	WindowsISO  string
	VirtIOISO   string
	CPUs        int
	MemoryGB    int
	Accel       string
	OutputDir   string
}

// Guest is a handle to a running WinPE VM.
type Guest interface {
	ReadSharedFile(path string) ([]byte, error)
	Stop() error
	ScreenshotDir() string
	Done() <-chan struct{}
	TakeScreenshot() (string, error)
}

// SharedFileNotFoundError is returned when a file is not present on the
// shared volume.
type SharedFileNotFoundError struct {
	Path string
}

func (e *SharedFileNotFoundError) Error() string {
	return fmt.Sprintf("shared file not found: %s", e.Path)
}
