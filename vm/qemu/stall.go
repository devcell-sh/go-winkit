package qemu

import (
	"fmt"
	"time"
)

// StallSignal is one poll's worth of liveness evidence.
type StallSignal struct {
	ScreenHash uint64
	ReadBytes  int64
	PC         string
}

// StallTracker counts consecutive polls in which nothing observable changed.
type StallTracker struct {
	prev     *StallSignal
	consec   int
	everRead bool
}

// Observe records a poll and returns the number of consecutive unchanged polls.
// Returns 0 until the guest has read at least one byte: before boot has touched
// the media, identical polls are expected and say nothing about liveness.
func (s *StallTracker) Observe(sig StallSignal) int {
	if sig.ReadBytes > 0 {
		s.everRead = true
	}
	prev := s.prev
	cur := sig
	s.prev = &cur

	if !s.everRead {
		s.consec = 0
		return 0
	}
	if prev == nil {
		s.consec = 0
		return 0
	}
	unchanged := prev.ScreenHash == sig.ScreenHash &&
		prev.ReadBytes == sig.ReadBytes
	if unchanged {
		s.consec++
	} else {
		s.consec = 0
	}
	return s.consec
}

// Stalled reports whether at least threshold consecutive unchanged polls have
// been observed.
func (s *StallTracker) Stalled(threshold int) bool {
	return s.consec >= threshold
}

// Reset zeroes the stall counter.
func (s *StallTracker) Reset() { s.consec = 0 }

// Consecutive returns the current unchanged-poll count.
func (s *StallTracker) Consecutive() int { return s.consec }

// StallPollsFor converts a stall budget in seconds into a poll count.
// Never returns fewer than 2: detecting "unchanged" needs two samples.
func StallPollsFor(budgetSeconds, intervalSeconds int) int {
	if intervalSeconds <= 0 {
		return 2
	}
	n := budgetSeconds / intervalSeconds
	if n < 2 {
		return 2
	}
	return n
}

// WriteProgressTracker watches cumulative bytes written to the guest's disk.
type WriteProgressTracker struct {
	Window   time.Duration
	started  bool
	lastData int64
	lastMove time.Duration
	elapsed  time.Duration
}

// Observe records cumulative bytes written at elapsed time since start.
func (w *WriteProgressTracker) Observe(written int64, elapsed time.Duration) bool {
	w.elapsed = elapsed
	if !w.started {
		w.started = true
		w.lastData = written
		w.lastMove = elapsed
		return false
	}
	if written != w.lastData {
		w.lastData = written
		w.lastMove = elapsed
	}
	return elapsed-w.lastMove >= w.Window
}

// Reason describes the stall in the terms it was measured in.
func (w *WriteProgressTracker) Reason() string {
	return fmt.Sprintf("guest wrote %d MB and nothing further for %s (total elapsed %s)",
		w.lastData>>20, (w.elapsed - w.lastMove).Round(time.Second), w.elapsed.Round(time.Second))
}
