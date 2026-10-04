//go:build windows

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

// ensureDWMRegistryKeys writes the SOFTWARE hive DWM configuration keys
// at runtime. These tell DWM to use software composition and skip the
// hardware capability check. On a freshly built WIM these are already
// present; this is a fallback for images built before the patch.
func ensureDWMRegistryKeys(out io.Writer) {
	regKeys := []struct {
		name  string
		value string
	}{
		{"Composition", "1"},
		{"ForceEffectMode", "0"},
		{"UseMachineCheck", "0"},
	}
	for _, k := range regKeys {
		cmd := exec.Command("reg", "add",
			`HKLM\SOFTWARE\Microsoft\Windows\DWM`,
			"/v", k.name, "/t", "REG_DWORD", "/d", k.value, "/f")
		cmd.Stdout = out
		cmd.Stderr = out
		if err := cmd.Run(); err != nil {
			initLog(out, "init-dwm-reg-error", fmt.Sprintf("reg add DWM\\%s: %v", k.name, err))
		}
	}
}

// ensureDisplayClasses registers PnP device classes for Display and
// Monitor if they're missing. WinPE ships without these, so child
// monitor devices from WDDM adapters stay as "Unknown" class and
// DWM can't acquire VidPn sources. After writing the class keys we
// trigger a device rescan so PnP re-classifies existing devices.
func ensureDisplayClasses(out io.Writer) {
	classes := []struct {
		guid   string
		class  string
		extras []struct{ name, value string }
	}{
		{
			guid:  `{4d36e968-e325-11ce-bfc1-08002be10318}`,
			class: "Display",
		},
		{
			guid:  `{4d36e96e-e325-11ce-bfc1-08002be10318}`,
			class: "Monitor",
			extras: []struct{ name, value string }{
				{"NoInstallClass", "1"},
			},
		},
	}
	for _, c := range classes {
		keyPath := `HKLM\SYSTEM\CurrentControlSet\Control\Class\` + c.guid
		cmd := exec.Command("reg", "add", keyPath,
			"/v", "Class", "/t", "REG_SZ", "/d", c.class, "/f")
		cmd.Stdout = out
		cmd.Stderr = out
		if err := cmd.Run(); err != nil {
			initLog(out, "init-display-class-error", fmt.Sprintf("reg add %s\\Class: %v", c.guid, err))
		}
		for _, e := range c.extras {
			cmd = exec.Command("reg", "add", keyPath,
				"/v", e.name, "/t", "REG_SZ", "/d", e.value, "/f")
			cmd.Stdout = out
			cmd.Stderr = out
			_ = cmd.Run()
		}
	}

	cmd := exec.Command("pnputil", "/scan-devices")
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		initLog(out, "init-pnp-scan-error", fmt.Sprintf("pnputil /scan-devices: %v", err))
	}
}

// startDWM launches the Desktop Window Manager compositor. DWM must run
// as SYSTEM and needs the display subsystem initialized (BasicDisplay.sys
// loaded by wpeinit). We start it directly rather than through the SCM
// because the DWM service definition may not be fully initialized in
// WinPE's minimal service control manager.
func startDWM(out io.Writer) error {
	dwmExe := `X:\windows\system32\dwm.exe`
	if _, err := os.Stat(dwmExe); err != nil {
		return fmt.Errorf("dwm.exe not found: %w", err)
	}

	ensureDisplayClasses(out)
	ensureDWMRegistryKeys(out)

	cmd := exec.Command(dwmExe)
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting dwm.exe: %w", err)
	}

	// Give DWM a moment to initialize, then check it's still running.
	time.Sleep(3 * time.Second)
	if cmd.Process == nil {
		return fmt.Errorf("dwm.exe process is nil after start")
	}

	// Check if it exited immediately (common failure mode in WinPE).
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("dwm.exe exited: %w", err)
		}
		return fmt.Errorf("dwm.exe exited immediately with code 0")
	default:
		initLog(out, "init-dwm-pid", fmt.Sprintf("dwm.exe running as PID %d", cmd.Process.Pid))
		return nil
	}
}
