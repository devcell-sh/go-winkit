package winpe

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildConfig_DefaultOps(t *testing.T) {
	cfg := BuildConfig{
		OpenSSH: true,
		VirtIO:  true,
	}
	ops := cfg.wimPrepOps()
	assert.NotEmpty(t, ops, "enabled features must produce WimPrepOps")

	var hasCapability, hasDriver bool
	for _, op := range ops {
		if op.Capability != "" {
			hasCapability = true
		}
		if op.Driver != "" {
			hasDriver = true
		}
	}
	assert.True(t, hasCapability, "OpenSSH should produce Capability ops")
	assert.True(t, hasDriver, "VirtIO should produce Driver ops")
}

func TestBuildConfig_NoOpsWhenAllDisabled(t *testing.T) {
	cfg := BuildConfig{}
	ops := cfg.wimPrepOps()
	assert.Empty(t, ops)
}

func TestBuildConfig_SharedFiles(t *testing.T) {
	cfg := BuildConfig{
		OpenSSH: true,
		PwshFiles: map[string][]byte{
			"/pwsh/pwsh.exe": []byte("fake-pwsh"),
		},
	}
	efiBootLoader := []byte("fake-efi")
	files := cfg.sharedFiles(efiBootLoader)

	require.Contains(t, files, "/startup.nsh", "shared volume must include startup.nsh")
	require.Contains(t, files, "/pwsh/pwsh.exe", "PowerShell files must be on shared volume")
}

func TestPayloadFiles_CompleteBootChain(t *testing.T) {
	files := payloadFiles(PayloadConfig{WPEInit: true, SyncAgent: true})

	require.Contains(t, files, "winpeshl.ini")
	require.Contains(t, files, "bootstrap.cmd",
		"winpeshl.ini launches bootstrap.cmd — omitting it leaves WinPE with nothing to run")
	require.Contains(t, files, "bootstrap.ps1")
	require.Contains(t, files, "agent.ps1")

	assert.Contains(t, string(files["winpeshl.ini"]), WinPEBootstrapCmdPath,
		"winpeshl.ini must point at the injected bootstrap.cmd")
	assert.Contains(t, string(files["bootstrap.cmd"]), `pwsh.exe`,
		"the shim must probe volumes for pwsh")
}

func TestBuildConfig_PayloadConfig(t *testing.T) {
	cfg := BuildConfig{
		ProgressPort: `\\.\Global\test.port`,
	}
	drivers := map[string][]byte{
		"drivers/vioserial/vioser.inf": []byte("inf"),
	}
	pc := cfg.payloadConfig(drivers, nil)
	assert.Equal(t, `\\.\Global\test.port`, pc.ProgressPort)
	assert.True(t, pc.WPEInit)
	assert.True(t, pc.SyncAgent)
	assert.Contains(t, pc.DriverINFs, `X:\winkit\drivers\vioserial\vioser.inf`)
}
