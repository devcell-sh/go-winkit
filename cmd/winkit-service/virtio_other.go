//go:build !windows

package main

import (
	"io"
	"os"
)

// openVirtioPort opens a virtio-serial port for writing. On non-Windows
// platforms the path is a regular file (useful for testing).
func openVirtioPort(path string) io.WriteCloser {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil
	}
	return f
}
