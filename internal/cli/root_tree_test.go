package cli

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommandTree_TopLevel(t *testing.T) {
	root := NewRootCmd()
	var names []string
	for _, c := range root.Commands() {
		if c.Name() == "help" || c.Name() == "completion" {
			continue
		}
		names = append(names, c.Name())
	}
	sort.Strings(names)
	assert.Equal(t, []string{"build", "debug", "export", "init", "start", "status", "stop"}, names)
}

func TestCommandTree_Export(t *testing.T) {
	root := NewRootCmd()
	exportCmd, _, err := root.Find([]string{"export"})
	require.NoError(t, err)
	require.Equal(t, "export", exportCmd.Name())

	var names []string
	for _, c := range exportCmd.Commands() {
		if c.Name() == "help" {
			continue
		}
		names = append(names, c.Name())
	}
	sort.Strings(names)
	assert.Equal(t, []string{"iso", "unattend", "vagrant", "wim", "winpe"}, names)
}

func TestCommandTree_Debug(t *testing.T) {
	root := NewRootCmd()
	debugCmd, _, err := root.Find([]string{"debug"})
	require.NoError(t, err)
	require.Equal(t, "debug", debugCmd.Name())

	var names []string
	for _, c := range debugCmd.Commands() {
		if c.Name() == "help" {
			continue
		}
		names = append(names, c.Name())
	}
	sort.Strings(names)
	assert.Equal(t, []string{"diag", "features", "fetch"}, names)
}

func TestCommandTree_ExportSubcommandDepth(t *testing.T) {
	root := NewRootCmd()

	tests := []struct {
		path []string
		name string
	}{
		{[]string{"export", "iso", "create"}, "create"},
		{[]string{"export", "iso", "inspect"}, "inspect"},
		{[]string{"export", "winpe", "build"}, "build"},
		{[]string{"export", "winpe", "run"}, "run"},
		{[]string{"export", "unattend", "generate"}, "generate"},
		{[]string{"export", "wim", "inject"}, "inject"},
		{[]string{"debug", "fetch", "virtio"}, "virtio"},
		{[]string{"debug", "diag", "guest"}, "guest"},
		{[]string{"debug", "diag", "panther"}, "panther"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, _, err := root.Find(tt.path)
			require.NoError(t, err)
			assert.Equal(t, tt.name, cmd.Name())
		})
	}
}
