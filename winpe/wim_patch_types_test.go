package winpe

import (
	"testing"

	"github.com/devcell-sh/go-regedit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncodeMultiSz_SingleString(t *testing.T) {
	data := EncodeMultiSz([]string{setupExecuteCmd})

	// Decode back via regedit.Value to verify round-trip.
	v := regedit.Value{Type: regedit.TypeMultiString, Data: data}
	got := v.Strings()
	require.Len(t, got, 1)
	assert.Equal(t, setupExecuteCmd, got[0])
}

func TestEncodeMultiSz_MultipleStrings(t *testing.T) {
	data := EncodeMultiSz([]string{"first", "second", "third"})
	v := regedit.Value{Type: regedit.TypeMultiString, Data: data}
	got := v.Strings()
	require.Len(t, got, 3)
	assert.Equal(t, []string{"first", "second", "third"}, got)
}

func TestPeAgentPatchSet_InstallWim(t *testing.T) {
	ps := PeAgentPatchSet(1, "/tmp/winkit-service.exe", true)

	assert.Equal(t, 1, ps.ImageNum)
	assert.Equal(t, "/tmp/winkit-service.exe", ps.Files[`\winkit-service.exe`])
	require.Len(t, ps.KeyWrites, 1)

	kw := ps.KeyWrites[0]
	assert.Equal(t, `\Windows\System32\config\SYSTEM`, kw.HivePath)
	assert.Equal(t, "Setup", kw.KeyPath)

	v, ok := kw.Spec.Values["SetupExecute"]
	require.True(t, ok)
	assert.Equal(t, regedit.TypeMultiString, v.Type)
	got := v.Strings()
	require.Len(t, got, 1)
	assert.Equal(t, setupExecuteCmd, got[0])
}

func TestPeAgentPatchSet_BootWim(t *testing.T) {
	ps := PeAgentPatchSet(2, "/tmp/winkit-service.exe", false)

	assert.Equal(t, 2, ps.ImageNum)
	assert.Equal(t, "/tmp/winkit-service.exe", ps.Files[`\winkit\winkit-service.exe`])
	assert.Empty(t, ps.KeyWrites)
}

func TestGroupRegistryWrites_OneGroupPerHive(t *testing.T) {
	ps := WimPatchSet{
		DWordPatches: []RegistryPatch{
			{HivePath: systemHivePath},
			{HivePath: softwareHivePath},
			{HivePath: systemHivePath},
		},
		KeyWrites: []RegistryKeyWrite{
			{HivePath: systemHivePath, KeyPath: "first"},
			{HivePath: softwareHivePath, KeyPath: "second"},
			{HivePath: systemHivePath, KeyPath: "third"},
		},
	}

	groups := groupRegistryWrites(ps)
	require.Len(t, groups, 2)
	assert.Equal(t, systemHivePath, groups[0].hivePath)
	assert.Len(t, groups[0].dwordPatches, 2)
	assert.Equal(t, []string{"first", "third"}, []string{
		groups[0].keyWrites[0].KeyPath,
		groups[0].keyWrites[1].KeyPath,
	})
	assert.Equal(t, softwareHivePath, groups[1].hivePath)
	assert.Len(t, groups[1].dwordPatches, 1)
	assert.Len(t, groups[1].keyWrites, 1)
}
