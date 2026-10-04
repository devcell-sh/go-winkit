package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseArgs_RecvRequiresTo(t *testing.T) {
	_, err := parseOptions([]string{"recv"})
	assert.Error(t, err)
	opts, err := parseOptions([]string{"recv", "--to", `E:\apps\x.bin`})
	require.NoError(t, err)
	assert.Equal(t, `E:\apps\x.bin`, opts.toPath)
}

func TestReceiveFile_WritesStdinAndCreatesDirs(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "a", "b", "file.bin")
	payload := bytes.Repeat([]byte{0, 1, 2, 0xff, '\n', '\r'}, 1000)
	n, err := receiveFile(bytes.NewReader(payload), dst)
	require.NoError(t, err)
	assert.Equal(t, int64(len(payload)), n)
	got, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, payload, got)
	_, err = os.Stat(dst + ".winkit-partial")
	assert.True(t, os.IsNotExist(err), "partial file must be renamed away")
}
