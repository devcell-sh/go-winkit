package qemu

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func sig(hash uint64, rd int64, pc string) StallSignal {
	return StallSignal{ScreenHash: hash, ReadBytes: rd, PC: pc}
}

func TestStallTracker_CountsIdenticalPolls(t *testing.T) {
	var s StallTracker
	assert.Equal(t, 0, s.Observe(sig(1, 1081344, "PC=A")))
	assert.Equal(t, 1, s.Observe(sig(1, 1081344, "PC=A")))
	assert.Equal(t, 2, s.Observe(sig(1, 1081344, "PC=A")))
}

func TestStallTracker_ResetsOnDiskProgress(t *testing.T) {
	var s StallTracker
	s.Observe(sig(1, 1000, "PC=A"))
	s.Observe(sig(1, 1000, "PC=A"))
	assert.Equal(t, 0, s.Observe(sig(1, 2000, "PC=A")))
}

func TestStallTracker_PCMovementAloneDoesNotReset(t *testing.T) {
	var s StallTracker
	s.Observe(sig(1, 1000, "PC=A"))
	s.Observe(sig(1, 1000, "PC=A"))
	assert.Equal(t, 2, s.Observe(sig(1, 1000, "PC=B")))
}

func TestStallTracker_ResetsOnScreenChange(t *testing.T) {
	var s StallTracker
	s.Observe(sig(1, 1000, "PC=A"))
	s.Observe(sig(1, 1000, "PC=A"))
	assert.Equal(t, 0, s.Observe(sig(2, 1000, "PC=A")))
}

func TestStallTracker_IgnoresPollsBeforeFirstRead(t *testing.T) {
	var s StallTracker
	for i := 0; i < 5; i++ {
		assert.Equal(t, 0, s.Observe(sig(1, 0, "PC=A")))
	}
}

func TestStallTracker_FrozenScreenAndDiskCountsEvenWithPCMovement(t *testing.T) {
	var s StallTracker
	s.Observe(sig(99, 5_000_000, "PC=fffff80401b6e80c"))
	assert.Equal(t, 1, s.Observe(sig(99, 5_000_000, "PC=fffff80401ab6770")))
	assert.Equal(t, 2, s.Observe(sig(99, 5_000_000, "PC=fffff80401e25428")))
}

func TestStallTracker_StalledAtThreshold(t *testing.T) {
	var s StallTracker
	s.Observe(sig(1, 1081344, "PC=A"))
	for i := 0; i < 3; i++ {
		s.Observe(sig(1, 1081344, "PC=A"))
	}
	assert.True(t, s.Stalled(3))
	assert.False(t, s.Stalled(4))
}

func TestStallPollsForDuration(t *testing.T) {
	assert.Equal(t, 4, StallPollsFor(60, 15))
	assert.Equal(t, 2, StallPollsFor(30, 15))
	assert.Equal(t, 2, StallPollsFor(1, 15))
}

func TestWriteProgress_ZeroWritesPastTheWindowIsAStall(t *testing.T) {
	w := &WriteProgressTracker{Window: 10 * time.Minute}
	assert.False(t, w.Observe(0, 0))
	assert.False(t, w.Observe(0, 5*time.Minute))
	assert.True(t, w.Observe(0, 11*time.Minute))
}

func TestWriteProgress_AnyWriteRestartsTheWindow(t *testing.T) {
	w := &WriteProgressTracker{Window: 10 * time.Minute}
	w.Observe(0, 0)
	assert.False(t, w.Observe(1<<20, 9*time.Minute))
	assert.False(t, w.Observe(1<<20, 18*time.Minute))
	assert.True(t, w.Observe(1<<20, 20*time.Minute))
}

func TestWriteProgress_ReasonNamesBytesAndElapsed(t *testing.T) {
	w := &WriteProgressTracker{Window: time.Minute}
	w.Observe(0, 0)
	w.Observe(0, 2*time.Minute)
	assert.Contains(t, w.Reason(), "0 MB")
	assert.Contains(t, w.Reason(), "2m")
}
