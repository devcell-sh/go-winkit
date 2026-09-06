//go:build windows

package main

import (
	"io"
	"os"
	"syscall"
	"unsafe"
)

var (
	kernel32   = syscall.NewLazyDLL("kernel32.dll")
	createFile = kernel32.NewProc("CreateFileW")
)

// openVirtioPort opens a virtio-serial device path for writing.
// Regular os.OpenFile rejects device paths (\\.\Global\...);
// CreateFileW is required.
func openVirtioPort(path string) io.WriteCloser {
	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil
	}
	const (
		genericWrite    = 0x40000000
		fileShareRW     = 0x3
		openExisting    = 3
	)
	h, _, _ := createFile.Call(
		uintptr(unsafe.Pointer(pathPtr)),
		genericWrite,
		fileShareRW,
		0,
		openExisting,
		0,
		0,
	)
	if h == uintptr(syscall.InvalidHandle) {
		return nil
	}
	return os.NewFile(h, path)
}
