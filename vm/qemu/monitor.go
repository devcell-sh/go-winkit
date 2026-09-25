package qemu

import (
	"context"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"time"
)

// MonitorConfig drives Monitor, the supported observability loop for a
// running install/boot VM. Consumers (the build package, devcell) get
// stall events as callbacks instead of parsing logs.
type MonitorConfig struct {
	// Poll gathers one liveness observation. Required. Production
	// callers use ScreenStatePoller; tests inject synthetic signals.
	Poll func(ctx context.Context) (StallSignal, error)
	// Interval between polls (default 10s).
	Interval time.Duration
	// StallThreshold is the consecutive-unchanged-poll count that
	// declares a stall (default 30). See StallPollsFor to derive it
	// from a time budget.
	StallThreshold int
	// OnPoll fires after every successful poll with the running
	// unchanged count.
	OnPoll func(sig StallSignal, consecutive int)
	// OnStall fires once per stall episode: when the unchanged count
	// first reaches StallThreshold. Progress resets the episode.
	OnStall func(consecutive int)
	// OnError receives poll errors; the loop continues (a QMP hiccup
	// during firmware transitions is normal). Nil ignores them.
	OnError func(err error)
}

// Monitor polls until ctx is done, tracking stalls with a StallTracker
// and firing the configured callbacks. It returns nil on ctx
// cancellation — the VM outcome is the caller's to judge — and an error
// only for an invalid config.
func Monitor(ctx context.Context, cfg MonitorConfig) error {
	if cfg.Poll == nil {
		return fmt.Errorf("qemu.Monitor: Poll is required")
	}
	interval := cfg.Interval
	if interval <= 0 {
		interval = 10 * time.Second
	}
	threshold := cfg.StallThreshold
	if threshold <= 0 {
		threshold = 30
	}

	var tracker StallTracker
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}

		sig, err := cfg.Poll(ctx)
		if err != nil {
			if cfg.OnError != nil {
				cfg.OnError(err)
			}
			continue
		}
		consec := tracker.Observe(sig)
		if cfg.OnPoll != nil {
			cfg.OnPoll(sig, consec)
		}
		if consec == threshold && cfg.OnStall != nil {
			cfg.OnStall(consec)
		}
	}
}

// ScreenStatePoller returns a Poll function for Monitor that samples a
// live VM over QMP: a screendump into shotDir (overwritten each poll,
// hashed for the screen signal) plus cumulative block-device read bytes.
func ScreenStatePoller(qmpSock, shotDir string) func(ctx context.Context) (StallSignal, error) {
	ppm := filepath.Join(shotDir, "monitor.ppm")
	return func(ctx context.Context) (StallSignal, error) {
		var sig StallSignal
		if err := QMPScreendump(qmpSock, ppm); err != nil {
			return sig, err
		}
		data, err := os.ReadFile(ppm)
		if err != nil {
			return sig, err
		}
		h := fnv.New64a()
		h.Write(data)
		sig.ScreenHash = h.Sum64()

		rd, err := QMPBlockReadBytes(qmpSock)
		if err != nil {
			return sig, err
		}
		sig.ReadBytes = rd
		return sig, nil
	}
}
