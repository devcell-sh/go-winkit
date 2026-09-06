//go:build integration && !darwin_vz

package cli

import "testing"

// captureVZScreenshots stub: the vz backend is not compiled in. The e2e only
// reaches this on WINKIT_VM_BACKEND=vz, which the wsl build rejects anyway.
func captureVZScreenshots(_ string, _ <-chan struct{}, done chan<- struct{}, t *testing.T) {
	defer close(done)
	t.Log("vz screenshots unavailable: build with -tags darwin_vz")
}
