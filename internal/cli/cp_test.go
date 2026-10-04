package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSplitCopyTarget(t *testing.T) {
	vm, p, err := splitCopyTarget(`winkit-pe-wsl:E:\apps\x.exe`)
	require.NoError(t, err)
	assert.Equal(t, "winkit-pe-wsl", vm)
	assert.Equal(t, `E:\apps\x.exe`, p)

	vm, p, err = splitCopyTarget(`E:\apps\x.exe`)
	require.NoError(t, err)
	assert.Equal(t, "", vm, "a drive-letter path alone means the only running VM")
	assert.Equal(t, `E:\apps\x.exe`, p)

	_, _, err = splitCopyTarget(`justaname`)
	assert.Error(t, err)
	_, _, err = splitCopyTarget(`vm:`)
	assert.Error(t, err)
}

func TestGuestRecvCommand_NoQuotes(t *testing.T) {
	c := guestRecvCommand(`E:\apps\putty.exe`)
	assert.NotContains(t, c, `"`)
	assert.Contains(t, c, `X:\winkit\winkit-service.exe recv --to E:\apps\putty.exe`)
	assert.Contains(t, c, `C:\winkit-service.exe recv --to E:\apps\putty.exe`)
}
