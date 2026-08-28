package winpe

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractPwshFiles(t *testing.T) {
	zipPath := filepath.Join(t.TempDir(), "test-pwsh.zip")
	f, err := os.Create(zipPath)
	require.NoError(t, err)
	w := zip.NewWriter(f)
	for _, name := range []string{"pwsh.exe", "pwsh.dll", "Modules/PSReadLine/PSReadLine.psd1"} {
		fw, err := w.Create(name)
		require.NoError(t, err)
		_, err = fw.Write([]byte("content-" + name))
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	require.NoError(t, f.Close())

	files, err := ExtractPwshFiles(zipPath)
	require.NoError(t, err)
	assert.Len(t, files, 3)
	assert.Contains(t, files, "/"+PwshVolDir+"/pwsh.exe")
	assert.Contains(t, files, "/"+PwshVolDir+"/pwsh.dll")
	assert.Contains(t, files, "/"+PwshVolDir+"/Modules/PSReadLine/PSReadLine.psd1")
	assert.Equal(t, []byte("content-pwsh.exe"), files["/"+PwshVolDir+"/pwsh.exe"])
}
