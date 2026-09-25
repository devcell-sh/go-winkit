//go:build !windows

package main

import (
	"io"
	"os"
)

// openSerialPort opens a serial port for writing. On non-Windows
// platforms the path is a regular file (useful for testing).
func openSerialPort(path string) io.WriteCloser {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil
	}
	return f
}
