package winpe

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestChromiumConstants(t *testing.T) {
	assert.NotEmpty(t, chromiumSnapshotRev, "snapshot revision must be set")
	assert.NotEmpty(t, chromiumChromeURL, "chrome download URL must be set")
	assert.NotEmpty(t, chromiumShellURL, "content-shell download URL must be set")
	assert.Contains(t, chromiumChromeURL, "Win_Arm64")
	assert.Contains(t, chromiumShellURL, "Win_Arm64")
}

func TestSkipChromiumFiles(t *testing.T) {
	assert.NotEmpty(t, skipChromiumFiles)
	for _, pat := range skipChromiumFiles {
		assert.NotEmpty(t, pat)
	}
}

func TestChromiumVolDirs(t *testing.T) {
	assert.Equal(t, "chrome", ChromiumVolDir)
	assert.Equal(t, "content-shell", ContentShellVolDir)
	assert.NotEqual(t, ChromiumVolDir, WebView2VolDir,
		"chrome and webview2 must use different volume dirs")
}
