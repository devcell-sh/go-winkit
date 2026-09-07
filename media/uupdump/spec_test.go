package uupdump

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustResolve(t *testing.T, s MediaSpec) ResolvedSpec {
	t.Helper()
	r, err := s.Resolve()
	require.NoError(t, err)
	return r
}

func TestResolve_DefaultsPreserveCurrentBehaviour(t *testing.T) {
	// `winkit build qemu-image` with no flags must mean exactly what it
	// means today: Windows 11 24H2 arm64 en-us PROFESSIONAL.
	r := mustResolve(t, MediaSpec{})
	assert.Equal(t, "windows", r.OS)
	assert.Equal(t, "11", r.Product)
	assert.Equal(t, "24H2", r.Version)
	assert.Equal(t, "26100", r.Series)
	assert.Equal(t, "arm64", r.Arch)
	assert.Equal(t, "PROFESSIONAL", r.Edition)
	assert.Equal(t, "en-us", r.Language)
	assert.False(t, r.Latest)
	assert.False(t, r.BuildPinned)
}

func TestResolve_BuildIsAuthoritative(t *testing.T) {
	r := mustResolve(t, MediaSpec{Build: "26100.9278"})
	assert.Equal(t, "26100.9278", r.Build)
	assert.Equal(t, "26100", r.Series)
	assert.Equal(t, "24H2", r.Version, "version derived from build")
	assert.Equal(t, "11", r.Product, "product derived from build")
	assert.True(t, r.BuildPinned)
}

func TestResolve_BuildSeriesForm(t *testing.T) {
	r := mustResolve(t, MediaSpec{Build: "19045"})
	assert.Equal(t, "", r.Build, "series pin is not an exact build")
	assert.Equal(t, "19045", r.Series)
	assert.Equal(t, "22H2", r.Version)
	assert.Equal(t, "10", r.Product)
}

func TestResolve_NonGABuildIsHonouredWithProductInferred(t *testing.T) {
	// An explicit --build must be honoured even off the GA table — the
	// user asked for that exact build. Product falls back to the
	// 22000 threshold.
	r := mustResolve(t, MediaSpec{Build: "28000.2804"})
	assert.Equal(t, "11", r.Product)
	assert.Equal(t, "", r.Version, "unknown series has no version name")
	assert.True(t, r.BuildPinned)
}

func TestResolve_ConflictsAreErrors(t *testing.T) {
	_, err := MediaSpec{Build: "26100.9278", Version: "23H2"}.Resolve()
	assert.ErrorContains(t, err, "conflict")

	_, err = MediaSpec{Build: "26100.9278", Product: "10"}.Resolve()
	assert.ErrorContains(t, err, "conflict")

	_, err = MediaSpec{Version: "latest", Build: "26100.9278"}.Resolve()
	assert.Error(t, err)
}

func TestResolve_VersionNameUsesGATable(t *testing.T) {
	r := mustResolve(t, MediaSpec{Version: "25h2"})
	assert.Equal(t, "26200", r.Series)
	assert.Equal(t, "25H2", r.Version)
	assert.Equal(t, "11", r.Product)
}

func TestResolve_AmbiguousNamePrefersWin11UnlessProductSet(t *testing.T) {
	// 22H2 shipped for both products; bare name means the newer product.
	r := mustResolve(t, MediaSpec{Version: "22h2"})
	assert.Equal(t, "22621", r.Series)
	assert.Equal(t, "11", r.Product)

	r = mustResolve(t, MediaSpec{Version: "22h2", Product: "10"})
	assert.Equal(t, "19045", r.Series)
	assert.Equal(t, "10", r.Product)
}

func TestResolve_UnknownVersionNameErrs(t *testing.T) {
	// 26H2 titles look GA on UUP dump but the release never shipped;
	// its ESD sets are unassemblable. Only --build can force it.
	_, err := MediaSpec{Version: "26h2"}.Resolve()
	require.Error(t, err)
	assert.ErrorContains(t, err, "--build")
}

func TestResolve_NumericVersionIsABuildPin(t *testing.T) {
	// Back-compat: --version 26100.9278 keeps working as a build pin.
	r := mustResolve(t, MediaSpec{Version: "26100.9278"})
	assert.Equal(t, "26100.9278", r.Build)
	assert.True(t, r.BuildPinned)
}

func TestResolve_Latest(t *testing.T) {
	r := mustResolve(t, MediaSpec{Version: "latest"})
	assert.True(t, r.Latest)
	assert.Empty(t, r.Series)
	assert.False(t, r.BuildPinned)
}

func TestResolve_ProductDefaultVersions(t *testing.T) {
	r := mustResolve(t, MediaSpec{Product: "10"})
	assert.Equal(t, "22H2", r.Version)
	assert.Equal(t, "19045", r.Series)
}

func TestResolve_ValidatesEnums(t *testing.T) {
	_, err := MediaSpec{OS: "linux"}.Resolve()
	assert.Error(t, err)
	_, err = MediaSpec{Arch: "amd64"}.Resolve()
	assert.Error(t, err)
	_, err = MediaSpec{Product: "12"}.Resolve()
	assert.Error(t, err)
}

func TestResolve_NormalizesEditionAndLanguage(t *testing.T) {
	r := mustResolve(t, MediaSpec{Edition: "professional", Language: "EN-US"})
	assert.Equal(t, "PROFESSIONAL", r.Edition)
	assert.Equal(t, "en-us", r.Language)
}

func TestMatchesBuild(t *testing.T) {
	def := mustResolve(t, MediaSpec{})
	assert.True(t, def.MatchesBuild(Build{Build: "26100.9278"}))
	assert.False(t, def.MatchesBuild(Build{Build: "26200.9278"}))

	exact := mustResolve(t, MediaSpec{Build: "26100.9278"})
	assert.True(t, exact.MatchesBuild(Build{Build: "26100.9278"}))
	assert.False(t, exact.MatchesBuild(Build{Build: "26100.1"}))

	latest := mustResolve(t, MediaSpec{Version: "latest"})
	for _, b := range liveBuilds {
		assert.True(t, latest.MatchesBuild(b))
	}
}

func TestISOName_EncodesTheSpec(t *testing.T) {
	assert.Equal(t, "windows-11-24h2-arm64-en-us.iso",
		mustResolve(t, MediaSpec{}).ISOName())
	assert.Equal(t, "windows-11-26100.9278-arm64-en-us.iso",
		mustResolve(t, MediaSpec{Build: "26100.9278"}).ISOName())
	assert.Equal(t, "windows-11-28000-arm64-en-us.iso",
		mustResolve(t, MediaSpec{Build: "28000"}).ISOName())
	assert.Equal(t, "windows-11-latest-arm64-en-us.iso",
		mustResolve(t, MediaSpec{Version: "latest"}).ISOName())
	assert.Equal(t, "windows-10-22h2-arm64-en-us.iso",
		mustResolve(t, MediaSpec{Product: "10"}).ISOName())
}

func TestSearchQueries_IncludeVersionForRecall(t *testing.T) {
	q := mustResolve(t, MediaSpec{}).SearchQueries()
	assert.Contains(t, q, "windows 11 arm64")
	assert.Contains(t, q, "windows 11 24h2 arm64")

	q = mustResolve(t, MediaSpec{Version: "latest"}).SearchQueries()
	assert.Equal(t, []string{"windows 11 arm64"}, q)
}

func TestGASkip_FiltersPreReleaseUnlessBuildPinned(t *testing.T) {
	latest := mustResolve(t, MediaSpec{Version: "latest"})
	kept, skipped := latest.FilterGA(liveBuilds)
	var keptBuilds []string
	for _, b := range kept {
		keptBuilds = append(keptBuilds, b.Build)
	}
	assert.Equal(t, []string{"26100.9278", "26200.9278"}, keptBuilds,
		"26H1/26H2/Insider previews must be skipped for ESD assembly")
	assert.Len(t, skipped, 3)

	pinned := mustResolve(t, MediaSpec{Build: "28000.2804"})
	kept, skipped = pinned.FilterGA(liveBuilds)
	assert.Len(t, kept, len(liveBuilds), "explicit --build bypasses the GA filter")
	assert.Empty(t, skipped)
}

func TestFoundBuildMsg_NoDuplicateBuildNumber(t *testing.T) {
	assert.Equal(t, "Windows 11, version 24H2 (26100.9278)",
		foundBuildMsg(Build{Title: "Windows 11, version 24H2 (26100.9278)", Build: "26100.9278"}))
	assert.Equal(t, "Some Title (26100.9278)",
		foundBuildMsg(Build{Title: "Some Title", Build: "26100.9278"}))
}
