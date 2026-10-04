package winpe

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDWMServicePatchSet_Structure(t *testing.T) {
	ps := DWMServicePatchSet()

	assert.Equal(t, 2, ps.ImageNum, "DWM patch targets boot.wim image 2")
	require.Len(t, ps.KeyWrites, 7, "DWM + BasicRender + dxgmms1 + dxgmms2 + DWM software composition + Display class + Monitor class")

	// Verify DWM service registration.
	dwm := ps.KeyWrites[0]
	assert.Equal(t, systemHivePath, dwm.HivePath)
	assert.Equal(t, `ControlSet001\Services\DWM`, dwm.KeyPath)
	require.NotNil(t, dwm.Spec)
	assert.Equal(t, "DWM", dwm.Spec.Name)

	vals := dwm.Spec.Values
	require.Contains(t, vals, "Type")
	require.Contains(t, vals, "Start")
	require.Contains(t, vals, "ImagePath")
	require.Contains(t, vals, "ObjectName")

	// DWM must run as SYSTEM.
	assert.Equal(t, szValue("LocalSystem"), vals["ObjectName"])

	// Demand-start so winkit-service controls the launch timing.
	assert.Equal(t, dwordValue(3), vals["Start"])

	// Verify BasicRender adapter registration.
	core := ps.KeyWrites[1]
	assert.Equal(t, systemHivePath, core.HivePath)
	assert.Equal(t, `ControlSet001\Services\BasicRender`, core.KeyPath)
	require.NotNil(t, core.Spec)
	// Kernel driver type, system-start.
	assert.Equal(t, dwordValue(1), core.Spec.Values["Type"])
	assert.Equal(t, dwordValue(1), core.Spec.Values["Start"])

	// Verify dxgmms1 driver registration (boot-start for WDDM display).
	mm1 := ps.KeyWrites[2]
	assert.Equal(t, systemHivePath, mm1.HivePath)
	assert.Equal(t, `ControlSet001\Services\dxgmms1`, mm1.KeyPath)
	require.NotNil(t, mm1.Spec)
	assert.Equal(t, dwordValue(1), mm1.Spec.Values["Type"])
	assert.Equal(t, dwordValue(0), mm1.Spec.Values["Start"])

	// Verify dxgmms2 driver registration (boot-start for WDDM display).
	mm2 := ps.KeyWrites[3]
	assert.Equal(t, systemHivePath, mm2.HivePath)
	assert.Equal(t, `ControlSet001\Services\dxgmms2`, mm2.KeyPath)
	require.NotNil(t, mm2.Spec)
	assert.Equal(t, dwordValue(1), mm2.Spec.Values["Type"])
	assert.Equal(t, dwordValue(0), mm2.Spec.Values["Start"])

	// Verify DWM software composition config in SOFTWARE hive.
	dwmCfg := ps.KeyWrites[4]
	assert.Equal(t, softwareHivePath, dwmCfg.HivePath)
	assert.Equal(t, `Microsoft\Windows\DWM`, dwmCfg.KeyPath)
	require.NotNil(t, dwmCfg.Spec)
	assert.Equal(t, dwordValue(1), dwmCfg.Spec.Values["Composition"])
	assert.Equal(t, dwordValue(0), dwmCfg.Spec.Values["UseMachineCheck"])

	dispClass := ps.KeyWrites[5]
	assert.Equal(t, systemHivePath, dispClass.HivePath)
	assert.Equal(t, `ControlSet001\Control\Class\{4d36e968-e325-11ce-bfc1-08002be10318}`, dispClass.KeyPath)
	require.NotNil(t, dispClass.Spec)
	assert.Equal(t, szValue("Display"), dispClass.Spec.Values["Class"])

	monClass := ps.KeyWrites[6]
	assert.Equal(t, systemHivePath, monClass.HivePath)
	assert.Equal(t, `ControlSet001\Control\Class\{4d36e96e-e325-11ce-bfc1-08002be10318}`, monClass.KeyPath)
	require.NotNil(t, monClass.Spec)
	assert.Equal(t, szValue("Monitor"), monClass.Spec.Values["Class"])
	assert.Equal(t, szValue("1"), monClass.Spec.Values["NoInstallClass"])
}

func TestDWMServicePatchSet_NoOverlapWithWSL1(t *testing.T) {
	dwm := DWMServicePatchSet()
	wsl := WSL1ServicePatchSet()

	dwmKeys := make(map[string]bool)
	for _, kw := range dwm.KeyWrites {
		dwmKeys[kw.HivePath+`\`+kw.KeyPath] = true
	}
	for _, kw := range wsl.KeyWrites {
		key := kw.HivePath + `\` + kw.KeyPath
		assert.False(t, dwmKeys[key],
			"DWM and WSL1 registry patches must not overlap: %s", key)
	}
}
