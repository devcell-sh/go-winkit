package winpe

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockGuest struct {
	files   map[string][]byte
	stopped bool
}

func (g *mockGuest) ReadSharedFile(path string) ([]byte, error) {
	if data, ok := g.files[path]; ok {
		return data, nil
	}
	return nil, &SharedFileNotFoundError{Path: path}
}

func (g *mockGuest) Stop() error {
	g.stopped = true
	return nil
}

func (g *mockGuest) ScreenshotDir() string           { return "" }
func (g *mockGuest) Done() <-chan struct{}           { return make(chan struct{}) }
func (g *mockGuest) TakeScreenshot() (string, error) { return "", nil }

type mockRunner struct {
	guest   *mockGuest
	booted  bool
	bootErr error
}

func (r *mockRunner) Boot(_ context.Context, _ BootSpec) (Guest, error) {
	if r.bootErr != nil {
		return nil, r.bootErr
	}
	r.booted = true
	return r.guest, nil
}

func TestRunner_InterfaceSatisfaction(t *testing.T) {
	var _ Runner = (*mockRunner)(nil)
	var _ Guest = (*mockGuest)(nil)
}

func TestBootSpec_HasRequiredFields(t *testing.T) {
	spec := BootSpec{
		WinPEISO:    "/tmp/winpe.iso",
		SharedFiles: map[string][]byte{"/boot.wim": []byte("wim")},
		WindowsISO:  "/tmp/windows.iso",
		VirtIOISO:   "/tmp/virtio.iso",
		CPUs:        2,
		MemoryGB:    4,
	}
	assert.Equal(t, "/tmp/winpe.iso", spec.WinPEISO)
	assert.Equal(t, 2, spec.CPUs)
	assert.Equal(t, 4, spec.MemoryGB)
}

func TestMockRunner_BootReturnsGuest(t *testing.T) {
	guest := &mockGuest{files: map[string][]byte{"/done.txt": []byte("SUCCESS")}}
	runner := &mockRunner{guest: guest}

	g, err := runner.Boot(context.Background(), BootSpec{})
	require.NoError(t, err)
	assert.True(t, runner.booted)

	data, err := g.ReadSharedFile("/done.txt")
	require.NoError(t, err)
	assert.Equal(t, []byte("SUCCESS"), data)
}

func TestMockGuest_ReadSharedFile_NotFound(t *testing.T) {
	guest := &mockGuest{files: map[string][]byte{}}
	_, err := guest.ReadSharedFile("/missing")
	assert.Error(t, err)

	var notFound *SharedFileNotFoundError
	assert.ErrorAs(t, err, &notFound)
	assert.Equal(t, "/missing", notFound.Path)
}

func TestMockGuest_Stop(t *testing.T) {
	guest := &mockGuest{files: map[string][]byte{}}
	require.NoError(t, guest.Stop())
	assert.True(t, guest.stopped)
}
