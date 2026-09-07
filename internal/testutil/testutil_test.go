package testutil

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResultDir_CreatesDirectory(t *testing.T) {
	dir := ResultDir(t)
	info, err := os.Stat(dir)
	require.NoError(t, err, "result dir must exist")
	assert.True(t, info.IsDir())
}

func TestResultDir_MatchesPattern(t *testing.T) {
	dir := ResultDir(t)
	base := filepath.Base(dir)
	pattern := regexp.MustCompile(`^\d{8}T\d{6}-`)
	assert.True(t, pattern.MatchString(base),
		"directory name %q must start with dateTimeISO prefix", base)
}

func TestResultDir_ContainsTestName(t *testing.T) {
	dir := ResultDir(t)
	base := filepath.Base(dir)
	assert.True(t, strings.Contains(base, "TestResultDir_ContainsTestName"),
		"directory name %q must contain the test name", base)
}

func TestResultDir_UnderTestResults(t *testing.T) {
	dir := ResultDir(t)
	assert.True(t, strings.Contains(dir, filepath.Join("test", "results")),
		"result dir %q must be under test/results/", dir)
}

func TestResultDir_SubtestSlashSanitized(t *testing.T) {
	t.Run("sub/with/slashes", func(t *testing.T) {
		dir := ResultDir(t)
		base := filepath.Base(dir)
		assert.False(t, strings.Contains(base, "/"),
			"directory name %q must not contain slashes from subtest names", base)
	})
}

func TestResultDir_Idempotent(t *testing.T) {
	dir1 := ResultDir(t)
	dir2 := ResultDir(t)
	assert.Equal(t, dir1, dir2, "repeated calls must return the same path")
}
