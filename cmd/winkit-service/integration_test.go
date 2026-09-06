//go:build !windows

package main

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kardianos/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func s6Available(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("s6-svscan")
	if err != nil {
		t.Skip("s6-svscan not on PATH")
	}
	return p
}

func TestParseOptions_LogFlags(t *testing.T) {
	opts, err := parseOptions([]string{"run", "--name", "svc", "--log-file", "/tmp/svc.log", "--log-virtio", `\\.\Global\winkit.progress.0`, "--", "sleep", "1"})
	require.NoError(t, err)
	assert.Equal(t, "/tmp/svc.log", opts.logFile)
	assert.Equal(t, `\\.\Global\winkit.progress.0`, opts.logVirtio)
	assert.Equal(t, []string{"sleep", "1"}, opts.cmd)
}

func TestIntegration_LogFileCapture(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "test.log")
	virtioPath := filepath.Join(dir, "virtio.log")

	opts := options{
		logFile:   logPath,
		logVirtio: virtioPath,
	}
	out, closers := buildOutput(opts, nil)
	defer func() {
		for _, c := range closers {
			c.Close()
		}
	}()

	w := &wrapper{
		cmdArg: []string{"/bin/sh", "-c", "echo log-line-1; echo log-line-2; echo log-line-3"},
		out:    out,
	}
	cfg := &service.Config{Name: "test-log", DisplayName: "test-log"}
	svc, err := service.New(w, cfg)
	require.NoError(t, err)

	require.NoError(t, w.Start(svc))
	<-w.done

	for _, c := range closers {
		c.Close()
	}

	data, err := os.ReadFile(logPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "log-line-1")
	assert.Contains(t, string(data), "log-line-2")
	assert.Contains(t, string(data), "log-line-3")
	t.Logf("log file: %s", strings.TrimSpace(string(data)))

	vdata, err := os.ReadFile(virtioPath)
	require.NoError(t, err)
	assert.Contains(t, string(vdata), "log-line-1")
	t.Logf("virtio file: %s", strings.TrimSpace(string(vdata)))
}

func TestIntegration_S6LogPiping(t *testing.T) {
	s6Path := s6Available(t)

	dir := t.TempDir()
	svcDir := filepath.Join(dir, "services")
	echoSvc := filepath.Join(svcDir, "echo-svc")
	require.NoError(t, os.MkdirAll(echoSvc, 0o755))

	// Service writes to stdout (which winkit-service captures).
	run := "#!/bin/sh\necho svc-output-hello\nexec sleep 3600\n"
	require.NoError(t, os.WriteFile(filepath.Join(echoSvc, "run"), []byte(run), 0o755))

	logPath := filepath.Join(dir, "captured.log")
	virtioPath := filepath.Join(dir, "virtio.log")

	opts := options{logFile: logPath, logVirtio: virtioPath}
	out, closers := buildOutput(opts, nil)
	defer func() {
		for _, c := range closers {
			c.Close()
		}
	}()

	// Use process group so cleanup kills all children (s6-supervise, sleep).
	cmd := exec.Command(s6Path, svcDir)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	cmd.Stderr = cmd.Stdout
	require.NoError(t, cmd.Start())

	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			out.Write([]byte(scanner.Text() + "\n"))
		}
	}()

	t.Cleanup(func() {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		stdout.Close()
		cmd.Wait()
	})

	require.Eventually(t, func() bool {
		_, err := os.Stat(filepath.Join(echoSvc, "supervise"))
		return err == nil
	}, 5*time.Second, 100*time.Millisecond, "s6 should create supervise dir")
}

func TestIntegration_WrapperStartStop(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "alive")

	w := &wrapper{
		cmdArg: []string{"/bin/sh", "-c", "touch " + marker + " && sleep 3600"},
	}
	cfg := &service.Config{Name: "test-wrapper", DisplayName: "test-wrapper"}
	svc, err := service.New(w, cfg)
	require.NoError(t, err)

	require.NoError(t, w.Start(svc))

	require.Eventually(t, func() bool {
		_, err := os.Stat(marker)
		return err == nil
	}, 2*time.Second, 50*time.Millisecond, "child should create marker file")

	require.NoError(t, w.Stop(svc))

	select {
	case <-w.done:
	case <-time.After(2 * time.Second):
		t.Fatal("child did not exit after Stop")
	}
}

func TestIntegration_S6Svscan(t *testing.T) {
	s6Path := s6Available(t)

	svcDir := t.TempDir()
	echoSvc := filepath.Join(svcDir, "echo-svc")
	require.NoError(t, os.MkdirAll(echoSvc, 0o755))

	logFile := filepath.Join(svcDir, "echo.log")
	run := "#!/bin/sh\necho s6-hello >> " + logFile + "\nexec sleep 3600\n"
	require.NoError(t, os.WriteFile(filepath.Join(echoSvc, "run"), []byte(run), 0o755))

	cmd := exec.Command(s6Path, svcDir)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdout = nil
	cmd.Stderr = nil
	require.NoError(t, cmd.Start())

	t.Cleanup(func() {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		cmd.Wait()
	})

	require.Eventually(t, func() bool {
		data, err := os.ReadFile(logFile)
		if err != nil {
			return false
		}
		return strings.Contains(string(data), "s6-hello")
	}, 5*time.Second, 100*time.Millisecond, "s6 should start echo-svc and write to log")

	data, err := os.ReadFile(logFile)
	require.NoError(t, err)
	assert.Contains(t, string(data), "s6-hello")
	t.Logf("s6 log: %s", strings.TrimSpace(string(data)))

	// Verify s6-supervise is managing the service.
	superviseDir := filepath.Join(echoSvc, "supervise")
	assert.DirExists(t, superviseDir, "s6-supervise should create supervise/ dir")
}

func TestIntegration_S6Restart(t *testing.T) {
	s6Path := s6Available(t)

	svcDir := t.TempDir()
	counterSvc := filepath.Join(svcDir, "counter")
	require.NoError(t, os.MkdirAll(counterSvc, 0o755))

	logFile := filepath.Join(svcDir, "counter.log")
	run := "#!/bin/sh\ndate +%s.%N >> " + logFile + "\nexec sleep 3600\n"
	require.NoError(t, os.WriteFile(filepath.Join(counterSvc, "run"), []byte(run), 0o755))

	cmd := exec.Command(s6Path, svcDir)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdout = nil
	cmd.Stderr = nil
	require.NoError(t, cmd.Start())

	t.Cleanup(func() {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		cmd.Wait()
	})

	// Wait for first start.
	require.Eventually(t, func() bool {
		data, _ := os.ReadFile(logFile)
		return len(strings.TrimSpace(string(data))) > 0
	}, 5*time.Second, 100*time.Millisecond)

	// Kill the service process (not s6-svscan): s6 should restart it.
	svcCtl, err := exec.LookPath("s6-svc")
	require.NoError(t, err)
	out, err := exec.Command(svcCtl, "-k", counterSvc).CombinedOutput()
	require.NoError(t, err, "s6-svc -k: %s", out)

	// Wait for restart: log should have 2+ lines.
	require.Eventually(t, func() bool {
		data, _ := os.ReadFile(logFile)
		lines := strings.Split(strings.TrimSpace(string(data)), "\n")
		return len(lines) >= 2
	}, 5*time.Second, 100*time.Millisecond, "s6 should restart the service after kill")

	data, _ := os.ReadFile(logFile)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	t.Logf("restart log (%d starts): %s", len(lines), strings.TrimSpace(string(data)))
	assert.GreaterOrEqual(t, len(lines), 2, "service should have been started at least twice")
}
