package winpe

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pollableGuest simulates a guest where files appear over time.
type pollableGuest struct {
	mu          sync.Mutex
	files       map[string][]byte
	stopped     bool
	doneCh      chan struct{}
	screenshots int
}

func newPollableGuest() *pollableGuest {
	return &pollableGuest{
		files:  map[string][]byte{},
		doneCh: make(chan struct{}),
	}
}

func (g *pollableGuest) SetFile(path string, data []byte) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.files[path] = data
}

func (g *pollableGuest) ReadSharedFile(path string) ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if data, ok := g.files[path]; ok {
		return data, nil
	}
	return nil, &SharedFileNotFoundError{Path: path}
}

func (g *pollableGuest) Stop() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.stopped = true
	return nil
}

func (g *pollableGuest) ScreenshotDir() string { return "" }

func (g *pollableGuest) Done() <-chan struct{} { return g.doneCh }

func (g *pollableGuest) TakeScreenshot() (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.screenshots++
	return "", nil
}

func (g *pollableGuest) SimulateExit() {
	close(g.doneCh)
}

func (g *pollableGuest) ScreenshotCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.screenshots
}

type orchestratorRunner struct {
	guest   *pollableGuest
	bootErr error
}

func (r *orchestratorRunner) Boot(_ context.Context, _ BootSpec) (Guest, error) {
	if r.bootErr != nil {
		return nil, r.bootErr
	}
	return r.guest, nil
}

func TestRun_Success(t *testing.T) {
	guest := newPollableGuest()
	runner := &orchestratorRunner{guest: guest}

	// Simulate async: done marker + wim appear after a short delay
	go func() {
		time.Sleep(50 * time.Millisecond)
		guest.SetFile("/"+WimBuilderDoneFile, []byte("SUCCESS"))
		guest.SetFile("/winkit.wim", []byte("fake-wim-data"))
	}()

	result, err := Run(context.Background(), runner, RunConfig{
		Build:        BuildConfig{OutputDir: t.TempDir()},
		PollInterval: 20 * time.Millisecond,
		Timeout:      5 * time.Second,
	})
	require.NoError(t, err)
	assert.Equal(t, []byte("fake-wim-data"), result.DevcellWim)
	assert.True(t, guest.stopped)
}

func TestRun_Timeout(t *testing.T) {
	guest := newPollableGuest()
	runner := &orchestratorRunner{guest: guest}

	_, err := Run(context.Background(), runner, RunConfig{
		Build:        BuildConfig{OutputDir: t.TempDir()},
		PollInterval: 10 * time.Millisecond,
		Timeout:      100 * time.Millisecond,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "timed out")
}

func TestRun_DISMFailure(t *testing.T) {
	guest := newPollableGuest()
	runner := &orchestratorRunner{guest: guest}

	go func() {
		time.Sleep(30 * time.Millisecond)
		guest.SetFile("/"+WimBuilderDoneFile, []byte("FAILED: DISM error 740"))
	}()

	_, err := Run(context.Background(), runner, RunConfig{
		Build:        BuildConfig{OutputDir: t.TempDir()},
		PollInterval: 10 * time.Millisecond,
		Timeout:      5 * time.Second,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "FAILED")
}

func TestRun_BootError(t *testing.T) {
	runner := &orchestratorRunner{bootErr: assert.AnError}

	_, err := Run(context.Background(), runner, RunConfig{
		Build:        BuildConfig{OutputDir: t.TempDir()},
		PollInterval: 10 * time.Millisecond,
		Timeout:      5 * time.Second,
	})
	assert.Error(t, err)
}

func TestRun_ContextCancellation(t *testing.T) {
	guest := newPollableGuest()
	runner := &orchestratorRunner{guest: guest}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, err := Run(ctx, runner, RunConfig{
		Build:        BuildConfig{OutputDir: t.TempDir()},
		PollInterval: 10 * time.Millisecond,
		Timeout:      5 * time.Second,
	})
	assert.Error(t, err)
}

func TestRun_VMExitAbortsPolling(t *testing.T) {
	guest := newPollableGuest()
	runner := &orchestratorRunner{guest: guest}

	// VM exits after 50ms without writing the done marker.
	go func() {
		time.Sleep(50 * time.Millisecond)
		guest.SimulateExit()
	}()

	start := time.Now()
	_, err := Run(context.Background(), runner, RunConfig{
		Build:        BuildConfig{OutputDir: t.TempDir()},
		PollInterval: 10 * time.Millisecond,
		Timeout:      10 * time.Second,
	})
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "exited")
	assert.Less(t, elapsed, 2*time.Second,
		"poll must abort promptly when VM exits, not wait for timeout")
}

func TestRun_TakesScreenshotsDuringPolling(t *testing.T) {
	guest := newPollableGuest()
	runner := &orchestratorRunner{guest: guest}

	go func() {
		time.Sleep(200 * time.Millisecond)
		guest.SetFile("/"+WimBuilderDoneFile, []byte("SUCCESS"))
		guest.SetFile("/winkit.wim", []byte("fake"))
	}()

	_, err := Run(context.Background(), runner, RunConfig{
		Build:              BuildConfig{OutputDir: t.TempDir()},
		PollInterval:       20 * time.Millisecond,
		ScreenshotInterval: 30 * time.Millisecond,
		Timeout:            5 * time.Second,
	})
	require.NoError(t, err)
	assert.Greater(t, guest.ScreenshotCount(), 0,
		"screenshot loop must capture frames independently of poll interval")
}

func TestRun_ScreenshotsCapturedBeforeEarlyExit(t *testing.T) {
	guest := newPollableGuest()
	runner := &orchestratorRunner{guest: guest}

	// VM dies after 200ms without writing the done marker.
	// Screenshot cadence is independent of the poll interval, so even
	// with a long poll interval, screenshots should accumulate.
	go func() {
		time.Sleep(200 * time.Millisecond)
		guest.SimulateExit()
	}()

	_, err := Run(context.Background(), runner, RunConfig{
		Build:              BuildConfig{OutputDir: t.TempDir()},
		PollInterval:       5 * time.Second,
		ScreenshotInterval: 30 * time.Millisecond,
		Timeout:            30 * time.Second,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exited")
	assert.Greater(t, guest.ScreenshotCount(), 0,
		"screenshots must be captured before VM exit, not tied to poll interval")
}
