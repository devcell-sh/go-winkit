//go:build !windows

package s6

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func s6Available(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("s6-svscan")
	if err != nil {
		t.Skip("s6-svscan not on PATH")
	}
	return p
}

func startSvscan(t *testing.T, scanDir string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(s6Available(t), scanDir)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// Negative pid: kill the whole process group (svscan + supervises).
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		cmd.Wait()
	})
	return cmd
}

func waitForFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", path)
}

// TestIntegration_UserServicesSupervised proves the end-to-end contract for
// additional user services: WriteDir-materialized services under one scan
// dir are all picked up and run by a real s6-svscan.
func TestIntegration_UserServicesSupervised(t *testing.T) {
	s6Available(t)
	scan := t.TempDir()
	out := t.TempDir()

	for _, name := range []string{"alpha", "beta"} {
		svc := Service{
			Name: name,
			Run: fmt.Sprintf("#!/bin/sh\ntouch %s/%s.started\nexec sleep 60\n",
				out, name),
		}
		if err := svc.WriteDir(scan); err != nil {
			t.Fatal(err)
		}
	}

	startSvscan(t, scan)
	waitForFile(t, filepath.Join(out, "alpha.started"), 5*time.Second)
	waitForFile(t, filepath.Join(out, "beta.started"), 5*time.Second)
}

// TestIntegration_ServiceDependency proves the readiness-polling dependency
// pattern user services must use under plain s6-svscan (no s6-rc ordering):
// "consumer" blocks until "producer" has published its readiness marker,
// then does its own work. Both services are added as user services.
func TestIntegration_ServiceDependency(t *testing.T) {
	s6Available(t)
	scan := t.TempDir()
	out := t.TempDir()
	ready := filepath.Join(out, "producer.ready")
	done := filepath.Join(out, "consumer.done")

	producer := Service{
		Name: "producer",
		Run: fmt.Sprintf("#!/bin/sh\nsleep 0.3\ntouch %s\nexec sleep 60\n",
			ready),
	}
	consumer := Service{
		Name: "consumer",
		Run: fmt.Sprintf("#!/bin/sh\nwhile [ ! -e %s ]; do sleep 0.05; done\ntouch %s\nexec sleep 60\n",
			ready, done),
	}
	for _, svc := range []Service{producer, consumer} {
		if err := svc.WriteDir(scan); err != nil {
			t.Fatal(err)
		}
	}

	startSvscan(t, scan)
	waitForFile(t, done, 5*time.Second)

	// The dependency must have been real: the consumer marker cannot
	// predate the producer's readiness marker.
	rfi, err := os.Stat(ready)
	if err != nil {
		t.Fatal(err)
	}
	dfi, err := os.Stat(done)
	if err != nil {
		t.Fatal(err)
	}
	if dfi.ModTime().Before(rfi.ModTime()) {
		t.Errorf("consumer finished before producer was ready: %v < %v",
			dfi.ModTime(), rfi.ModTime())
	}
}

// TestIntegration_RestartOnCrash: s6-supervise restarts a user service
// whose run script exits, the core supervision guarantee winkit promises
// for registered services.
func TestIntegration_RestartOnCrash(t *testing.T) {
	s6Available(t)
	scan := t.TempDir()
	out := t.TempDir()
	counter := filepath.Join(out, "runs")

	svc := Service{
		Name: "flaky",
		Run: fmt.Sprintf("#!/bin/sh\necho run >> %s\nexit 1\n",
			counter),
	}
	if err := svc.WriteDir(scan); err != nil {
		t.Fatal(err)
	}

	startSvscan(t, scan)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(counter); err == nil {
			if lines := len(splitNonEmpty(string(data))); lines >= 2 {
				return // restarted at least once
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("service was not restarted after crashing")
}

func splitNonEmpty(s string) []string {
	var out []string
	start := -1
	for i, r := range s {
		if r == '\n' {
			if start >= 0 {
				out = append(out, s[start:i])
				start = -1
			}
			continue
		}
		if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		out = append(out, s[start:])
	}
	return out
}
