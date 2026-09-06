package qemu

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStartInstall_RequiresWindowsISO(t *testing.T) {
	_, err := StartInstall(context.Background(), InstallConfig{
		OutputDir: t.TempDir(),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "WindowsISO is required")
}

func TestStartInstall_RequiresDiskOrOutputDir(t *testing.T) {
	_, err := StartInstall(context.Background(), InstallConfig{
		WindowsISO: "/tmp/fake.iso",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DiskPath or OutputDir is required")
}

func TestInstallConfig_DefaultValues(t *testing.T) {
	cfg := InstallConfig{
		WindowsISO: "/tmp/fake.iso",
		OutputDir:  t.TempDir(),
		CPUs:       0,
		MemoryGB:   0,
		DiskSizeGB: 0,
	}
	assert.Equal(t, uint(0), cfg.CPUs)
	assert.Equal(t, uint64(0), cfg.MemoryGB)
	assert.Equal(t, 0, cfg.DiskSizeGB)
}

func TestInstallVM_Methods(t *testing.T) {
	vm := &InstallVM{
		qmpSock:   "/tmp/test.sock",
		serialLog: "/tmp/serial.log",
		outputDir: "/tmp/output",
	}
	assert.Equal(t, "/tmp/test.sock", vm.QMPSocket())
	assert.Equal(t, "/tmp/serial.log", vm.SerialLog())
	assert.Equal(t, "/tmp/output", vm.OutputDir())
}
