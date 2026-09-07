package qemu

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateFATQcow2_RoundTrip(t *testing.T) {
	if _, err := exec.LookPath("qemu-img"); err != nil {
		t.Skip("qemu-img not available")
	}

	dir := t.TempDir()
	imgPath := filepath.Join(dir, "test.qcow2")
	files := map[string][]byte{
		"/hello.txt": []byte("world"),
		"/sub/data":  []byte("nested"),
	}

	require.NoError(t, CreateFATQcow2(imgPath, files, 64*1024*1024))

	data, err := ReadFileFromFATQcow2(imgPath, "/hello.txt")
	require.NoError(t, err)
	assert.Equal(t, []byte("world"), data)

	data, err = ReadFileFromFATQcow2(imgPath, "/sub/data")
	require.NoError(t, err)
	assert.Equal(t, []byte("nested"), data)
}
