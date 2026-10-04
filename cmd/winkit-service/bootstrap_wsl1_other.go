//go:build !windows

package main

import (
	"fmt"
	"io"
	"os/exec"
	"strings"
)

func runCmdOutput(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if text != "" {
			return text, fmt.Errorf("%s failed: %w\n%s", name, err, text)
		}
		return "", fmt.Errorf("%s failed: %w", name, err)
	}
	return text, nil
}

func patchRamdiskBPB(_ byte) error {
	return fmt.Errorf("BPB patching is only supported on Windows")
}

func ensureWritableVolume(_ io.Writer, _ wsl1BootstrapConfig) error {
	return fmt.Errorf("volume preparation is only supported on Windows")
}

func relocateWSLRuntime(_ io.Writer, _, _ string) error {
	return fmt.Errorf("WSL runtime relocation is only supported on Windows")
}

func setupWSLUser(_ io.Writer, _ wsl1BootstrapConfig) error {
	return fmt.Errorf("WSL user setup is only supported on Windows")
}

func exportDNSServers(_ io.Writer, _ byte) error {
	return fmt.Errorf("DNS export is only supported on Windows")
}

func importAndProbeDistro(_ io.Writer, _ wsl1BootstrapConfig) error {
	return fmt.Errorf("WSL distro import is only supported on Windows")
}
