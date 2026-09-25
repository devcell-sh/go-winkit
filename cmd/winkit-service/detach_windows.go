//go:build windows

package main

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

func detachPEAgent() {
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "detach: cannot resolve own path: %v\n", err)
		os.Exit(1)
	}
	args := []string{"run", "--name", peAgentName}
	argv0, err := syscall.UTF16PtrFromString(self)
	if err != nil {
		fmt.Fprintf(os.Stderr, "detach: %v\n", err)
		os.Exit(1)
	}
	cmdLine, err := syscall.UTF16PtrFromString(self + " " + joinArgs(args))
	if err != nil {
		fmt.Fprintf(os.Stderr, "detach: %v\n", err)
		os.Exit(1)
	}
	var si syscall.StartupInfo
	si.Cb = uint32(unsafe.Sizeof(si))
	var pi syscall.ProcessInformation
	err = syscall.CreateProcess(
		argv0,
		cmdLine,
		nil, nil,
		false,
		syscall.CREATE_NEW_PROCESS_GROUP,
		nil, nil,
		&si, &pi,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "detach: CreateProcess: %v\n", err)
		os.Exit(1)
	}
	syscall.CloseHandle(pi.Thread)
	syscall.CloseHandle(pi.Process)
	fmt.Fprintf(os.Stderr, "detach: pe-agent started (pid %d)\n", pi.ProcessId)
}

func joinArgs(args []string) string {
	var s string
	for i, a := range args {
		if i > 0 {
			s += " "
		}
		s += a
	}
	return s
}
