package uupdump

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Titles verbatim from the live listing on 2026-08-30.
var liveBuilds = []Build{
	{Title: "Windows 11, version 26H1 (28000.2804)", Build: "28000.2804"},
	{Title: "Windows 11, version 26H2 (26300.9278)", Build: "26300.9278"},
	{Title: "Windows 11, version 24H2 (26100.9278)", Build: "26100.9278"},
	{Title: "Windows 11, version 25H2 (26200.9278)", Build: "26200.9278"},
	{Title: "Windows 11 Insider Preview Feature Update (28120.2760)", Build: "28120.2760"},
}

func matching(version string) []string {
	var out []string
	for _, b := range liveBuilds {
		if BuildMatchesVersion(b, version) {
			out = append(out, b.Build)
		}
	}
	return out
}

func TestBuildMatchesVersion_NameIsSufficient(t *testing.T) {
	assert.Equal(t, []string{"26100.9278"}, matching("24H2"))
	assert.Equal(t, []string{"26100.9278"}, matching("24h2"))
	assert.Equal(t, []string{"26200.9278"}, matching("25h2"))
	assert.Equal(t, []string{"28000.2804"}, matching("26h1"))
}

func TestBuildMatchesVersion_BuildForms(t *testing.T) {
	assert.Equal(t, []string{"26100.9278"}, matching("26100"), "series prefix")
	assert.Equal(t, []string{"26100.9278"}, matching("26100.9278"), "exact build")
	assert.Empty(t, matching("26100.1"), "prefix must not match across the dot boundary loosely")
}

func TestBuildMatchesVersion_LatestMatchesAll(t *testing.T) {
	assert.Len(t, matching(""), len(liveBuilds))
	assert.Len(t, matching("latest"), len(liveBuilds))
	assert.Len(t, matching("Latest"), len(liveBuilds))
}
