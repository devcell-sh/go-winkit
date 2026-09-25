//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
)

func detachPEAgent() {
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "detach: cannot resolve own path: %v\n", err)
		os.Exit(1)
	}
	cmd := exec.Command(self, "run", "--name", peAgentName)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "detach: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "detach: pe-agent started (pid %d)\n", cmd.Process.Pid)
}
