package cache

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDir_HonoursEnvOverride(t *testing.T) {
	t.Setenv(DirEnv, "/custom/cache")
	assert.Equal(t, "/custom/cache", Dir())
}

// With no override the location must land under the user cache dir, not
// somewhere test-specific: this is the path `winkit fetch` seeds by default.
func TestDir_DefaultsUnderUserCacheDir(t *testing.T) {
	t.Setenv(DirEnv, "")
	t.Setenv("XDG_CACHE_HOME", "/xdg-cache")
	assert.Equal(t, filepath.Join("/xdg-cache", "winkit"), Dir())
}

// The artifact helpers are the contract between `winkit fetch` and the
// integration tests, so they must agree on both directory and filename.
func TestArtifactPaths_LiveInTheCacheDir(t *testing.T) {
	t.Setenv(DirEnv, "/custom/cache")

	assert.Equal(t, "/custom/cache/"+WindowsISOName, WindowsISO())
	assert.Equal(t, "/custom/cache/"+VirtIOISOName, VirtIOISO())
}
