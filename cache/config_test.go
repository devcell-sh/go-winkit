package cache

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// An application embedding winkit picks the location itself and usually
// cannot set environment variables for the process, so an explicit Dir has
// to win over the env override.
func TestConfig_ExplicitDirBeatsTheEnvironment(t *testing.T) {
	t.Setenv(DirEnv, "/from-env")
	cfg := Config{Dir: "/from-caller"}

	assert.Equal(t, "/from-caller", cfg.Resolve())
	assert.Equal(t, "/from-caller/"+WindowsISOName, cfg.WindowsISO())
	assert.Equal(t, "/from-caller/"+VirtIOISOName, cfg.VirtIOISO())
}

// A zero Config is the common case for the CLI, which wants the shared
// default rather than a second, divergent notion of "unset".
func TestConfig_ZeroValueUsesTheSharedDefault(t *testing.T) {
	t.Setenv(DirEnv, "/from-env")

	assert.Equal(t, "/from-env", Config{}.Resolve())
	assert.Equal(t, Dir(), Config{}.Resolve(),
		"a zero Config must resolve exactly where Dir does")
}

// Config and the package helpers must not drift: tests read one, fetch
// writes the other.
func TestConfig_AgreesWithPackageHelpers(t *testing.T) {
	t.Setenv(DirEnv, "/shared")

	assert.Equal(t, WindowsISO(), Config{}.WindowsISO())
	assert.Equal(t, VirtIOISO(), Config{}.VirtIOISO())
}
