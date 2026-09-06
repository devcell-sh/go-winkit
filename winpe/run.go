package winpe

import (
	"context"
	"fmt"
	"runtime/debug"
	"strings"
	"time"
)

// RunConfig configures the full build-boot-extract cycle.
type RunConfig struct {
	Build              BuildConfig
	PollInterval       time.Duration
	Timeout            time.Duration
	ScreenshotInterval time.Duration

	// CPUs and MemoryGB size the builder VM (runner defaults apply when 0).
	CPUs     int
	MemoryGB int
}

// RunResult holds the output of a completed WIM builder run.
type RunResult struct {
	DevcellWim []byte
	GuestLogs  map[string]string

	// Events is the parsed structured guest log (build.jsonl), populated
	// when the guest exposes its path. SkippedEvents counts malformed
	// lines (partial writes at power-loss are normal, not fatal).
	Events        []GuestEvent
	SkippedEvents int
}

// structuredLogger is implemented by guests that stream build.jsonl.
type structuredLogger interface {
	StructuredLogPath() string
}

// Run executes the full WIM builder cycle: build WinPE artifacts, boot via
// runner, poll for completion, extract the serviced winkit.wim.
func Run(ctx context.Context, runner Runner, cfg RunConfig) (*RunResult, error) {
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 15 * time.Second
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 15 * time.Minute
	}

	var spec BootSpec
	if cfg.Build.WindowsISO != "" {
		buildResult, err := Build(cfg.Build)
		if err != nil {
			return nil, fmt.Errorf("building WinPE artifacts: %w", err)
		}
		spec = BootSpec{
			WinPEISO:    buildResult.WinPEISO,
			SharedFiles: buildResult.SharedFiles,
			WindowsISO:  cfg.Build.WindowsISO,
			VirtIOISO:   cfg.Build.VirtIOISO,
			OutputDir:   cfg.Build.OutputDir,
		}
	} else {
		spec = BootSpec{OutputDir: cfg.Build.OutputDir}
	}
	spec.CPUs = cfg.CPUs
	spec.MemoryGB = cfg.MemoryGB

	guest, err := runner.Boot(ctx, spec)
	if err != nil {
		return nil, fmt.Errorf("booting WinPE: %w", err)
	}
	defer guest.Stop()
	// Boot packed SharedFiles into the guest's volume; free the ~2GB of
	// buffers instead of holding them for the whole poll loop. FreeOSMemory
	// returns them to the kernel now — in a memory-cgroup this is the
	// difference between the guest fitting and an OOM kill.
	spec.SharedFiles = nil
	debug.FreeOSMemory()

	screenshotInterval := cfg.ScreenshotInterval
	if screenshotInterval == 0 {
		screenshotInterval = 5 * time.Second
	}

	doneMarker, err := pollForCompletion(ctx, guest, cfg.PollInterval, cfg.Timeout, screenshotInterval)
	if err != nil {
		return nil, err
	}

	result := strings.TrimSpace(string(doneMarker))
	if result != "SUCCESS" {
		return nil, fmt.Errorf("builder reported %s", result)
	}

	wimData, err := guest.ReadSharedFile("/winkit.wim")
	if err != nil {
		return nil, fmt.Errorf("reading winkit.wim: %w", err)
	}

	logs := collectLogs(guest)

	res := &RunResult{
		DevcellWim: wimData,
		GuestLogs:  logs,
	}
	if sl, ok := guest.(structuredLogger); ok {
		res.Events, res.SkippedEvents, _ = ReadGuestEvents(sl.StructuredLogPath())
	}
	return res, nil
}

// fatalWatcher is implemented by guests that monitor the serial console for
// unrecoverable boot states (firmware synchronous exception, EFI shell drop).
// Waiting out the timeout on those wastes tens of minutes per run.
type fatalWatcher interface {
	FatalBoot() <-chan string
}

// progressWatcher exposes the live guest progress stream. The guest's FAT
// writes only reach the shared image after shutdown, so the done marker is
// invisible to ReadSharedFile while the VM runs — the streamed log is the
// only live completion signal.
type progressWatcher interface {
	ProgressContains(token string) bool
}

// shutdowner supports graceful VM quit, flushing writeback caches so the
// shared volume is consistent before it is read.
type shutdowner interface {
	Shutdown() error
}

func pollForCompletion(ctx context.Context, guest Guest, interval, timeout, screenshotInterval time.Duration) ([]byte, error) {
	deadline := time.After(timeout)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	done := guest.Done()

	var fatal <-chan string
	if fw, ok := guest.(fatalWatcher); ok {
		fatal = fw.FatalBoot()
	}

	stopScreenshots := startScreenshotLoop(guest, screenshotInterval, done)
	defer stopScreenshots()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case reason := <-fatal:
			return nil, fmt.Errorf("boot failed: %s", reason)
		case <-deadline:
			return nil, fmt.Errorf("builder timed out after %s", timeout)
		case <-done:
			data, err := guest.ReadSharedFile("/" + WimBuilderDoneFile)
			if err != nil {
				return nil, fmt.Errorf("VM exited before writing done marker")
			}
			return data, nil
		case <-ticker.C:
			data, err := guest.ReadSharedFile("/" + WimBuilderDoneFile)
			if err == nil {
				return data, nil
			}
			pw, ok := guest.(progressWatcher)
			if !ok || !pw.ProgressContains(WimBuilderCompleteToken) {
				continue
			}
			// Builder reported completion on the live stream. Quit the VM
			// so its FAT writes flush, then read the real done marker.
			if sd, ok := guest.(shutdowner); ok {
				sd.Shutdown()
			}
			<-done
			data, err = guest.ReadSharedFile("/" + WimBuilderDoneFile)
			if err != nil {
				return nil, fmt.Errorf("builder reported done but no done marker on shared volume")
			}
			return data, nil
		}
	}
}

func startScreenshotLoop(guest Guest, interval time.Duration, done <-chan struct{}) func() {
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-done:
				return
			case <-ticker.C:
				guest.TakeScreenshot()
			}
		}
	}()
	return func() { close(stop) }
}

func collectLogs(guest Guest) map[string]string {
	logs := make(map[string]string)
	for _, name := range []string{AgentResultFile, "serial.log", "guest-progress.log"} {
		if data, err := guest.ReadSharedFile("/" + name); err == nil {
			logs[name] = string(data)
		}
	}
	return logs
}
