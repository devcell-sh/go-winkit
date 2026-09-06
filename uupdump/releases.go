package uupdump

import "strings"

// Release is one GA (generally available) Windows feature release. The UUP
// dump API exposes no channel field and titles are unreliable ("Windows 11,
// version 26H1" reads GA but is an unreleased preview branch), so shipped
// releases are curated facts. Preview/Insider builds package their install
// image across .cab/.msu files that ESD assembly cannot consume — GA
// releases publish complete ESD sets.
//
// Maintenance: append a row when a release goes GA and its ESD set proves
// assemblable (roughly twice a year).
type Release struct {
	Product string // "10" or "11"
	Version string // canonical name, e.g. "24H2"
	Series  string // build series, e.g. "26100"
}

// gaReleases is ordered newest-first within each product.
var gaReleases = []Release{
	{Product: "11", Version: "25H2", Series: "26200"},
	{Product: "11", Version: "24H2", Series: "26100"},
	{Product: "11", Version: "23H2", Series: "22631"},
	{Product: "11", Version: "22H2", Series: "22621"},
	{Product: "11", Version: "21H2", Series: "22000"},
	{Product: "10", Version: "22H2", Series: "19045"},
	{Product: "10", Version: "21H2", Series: "19044"},
	{Product: "10", Version: "21H1", Series: "19043"},
	{Product: "10", Version: "20H2", Series: "19042"},
	{Product: "10", Version: "2004", Series: "19041"},
}

// buildSeries returns the numeric series of a build string ("26100.9278"
// → "26100").
func buildSeries(build string) string {
	if i := strings.IndexByte(build, '.'); i >= 0 {
		return build[:i]
	}
	return build
}

// ReleaseForBuild returns the GA release a build belongs to, if any.
func ReleaseForBuild(build string) (Release, bool) {
	s := buildSeries(build)
	for _, r := range gaReleases {
		if r.Series == s {
			return r, true
		}
	}
	return Release{}, false
}

// ReleaseForVersion resolves a version name within a product ("11",
// "24H2") to its GA release.
func ReleaseForVersion(product, version string) (Release, bool) {
	for _, r := range gaReleases {
		if r.Product == product && strings.EqualFold(r.Version, version) {
			return r, true
		}
	}
	return Release{}, false
}

// IsGABuild reports whether a build belongs to a shipped release.
func IsGABuild(build string) bool {
	_, ok := ReleaseForBuild(build)
	return ok
}

// seriesForVersion returns every GA build series a version name maps to.
// Names repeat across products (Win10 and Win11 both shipped a 21H2 and a
// 22H2), so a name can resolve to more than one series.
func seriesForVersion(version string) []string {
	var out []string
	for _, r := range gaReleases {
		if strings.EqualFold(r.Version, version) {
			out = append(out, r.Series)
		}
	}
	return out
}
