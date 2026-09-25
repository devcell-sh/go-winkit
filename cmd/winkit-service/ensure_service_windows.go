//go:build windows

package main

import (
	"fmt"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// ensureService starts an existing Windows service or kernel driver and waits
// for the SCM to report it running. This keeps guest scripts independent of
// localized sc.exe output and treats an already-running service as success.
func ensureService(name string, timeout time.Duration) error {
	manager, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connecting to service manager: %w", err)
	}
	defer manager.Disconnect()

	service, err := manager.OpenService(name)
	if err != nil {
		return fmt.Errorf("opening service %s: %w", name, err)
	}
	defer service.Close()

	status, err := service.Query()
	if err != nil {
		return fmt.Errorf("querying service %s: %w", name, err)
	}
	if status.State == svc.Running {
		return nil
	}
	if status.State != svc.StartPending {
		if err := service.Start(); err != nil {
			return fmt.Errorf("starting service %s: %w", name, err)
		}
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		status, err = service.Query()
		if err != nil {
			return fmt.Errorf("querying service %s after start: %w", name, err)
		}
		if status.State == svc.Running {
			return nil
		}
		if status.State == svc.Stopped {
			return fmt.Errorf("service %s stopped before reaching running state (exit code %d)", name, status.Win32ExitCode)
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("service %s did not reach running state within %s", name, timeout)
}
