package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewWinPERunCmd_Exists(t *testing.T) {
	root := NewRootCmd()
	winpeCmd, _, err := root.Find([]string{"winpe", "run"})
	require.NoError(t, err)
	assert.Equal(t, "run", winpeCmd.Name())
}

func TestNewWinPERunCmd_RequiresISO(t *testing.T) {
	root := NewRootCmd()
	root.SetArgs([]string{"winpe", "run"})
	err := root.Execute()
	assert.Error(t, err)
}

func TestNewWinPERunCmd_HasFlags(t *testing.T) {
	root := NewRootCmd()
	winpeCmd, _, _ := root.Find([]string{"winpe", "run"})
	assert.NotNil(t, winpeCmd.Flags().Lookup("iso"))
	assert.NotNil(t, winpeCmd.Flags().Lookup("virtio-iso"))
	assert.NotNil(t, winpeCmd.Flags().Lookup("accel"))
	assert.NotNil(t, winpeCmd.Flags().Lookup("timeout"))
}
