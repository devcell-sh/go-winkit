//go:build darwin

package vz

import (
	"context"

	"github.com/tmc/apple/dispatch"
)

// dispatchCall dispatches fn on the VM's serial dispatch queue and blocks
// until the completion handler delivers a result or ctx is cancelled.
//
// All Virtualization.framework VM operations (Start, Stop, ConnectToPort, etc.)
// MUST run on the VM's associated dispatch queue. The tmc/apple purego bindings
// send ObjC messages directly from the calling goroutine's thread, so callers
// must dispatch explicitly. Two pitfalls this helper prevents:
//   - SIGTRAP: calling a VM method from a thread not on the VM's queue.
//   - Deadlock: using queue.AsyncAndWait, which blocks the serial queue while
//     the ObjC completion handler tries to deliver on the same queue.
//
// The pattern: dispatch the ObjC call via queue.Async, let the completion
// handler send its result through a Go channel, and wait for that channel
// OFF the queue.
func dispatchCall[T any](q dispatch.Queue, ctx context.Context, fn func(done func(T))) (T, error) {
	ch := make(chan T, 1)
	q.Async(func() {
		fn(func(result T) {
			ch <- result
		})
	})
	select {
	case result := <-ch:
		return result, nil
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
}
