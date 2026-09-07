package uupdump

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// MediaSpec names Windows installation media along orthogonal axes. Every
// field is optional; Resolve fills defaults and cross-checks the rest.
// The CLI maps its flags onto this 1:1 (--os, --product, --arch,
// --version, --build, --edition, --lang).
type MediaSpec struct {
	OS       string // "windows" (the only OS)
	Product  string // "10" or "11"; derived from Build/Version when empty
	Arch     string // "arm64" (the only supported arch today)
	Version  string // release name ("24H2"), or "latest" for the GA ladder
	Build    string // series ("26100") or exact build ("26100.9278"); authoritative
	Edition  string // e.g. "PROFESSIONAL"
	Language string // e.g. "en-us"
}

// ResolvedSpec is a MediaSpec with every axis decided. Build is
// authoritative: when set it derives Version and Product, and a
// caller-supplied Version/Product is only a cross-check.
type ResolvedSpec struct {
	OS       string
	Product  string
	Version  string // canonical name ("24H2"); "" when the series has none
	Series   string // build series pin; "" only when Latest
	Build    string // exact build pin; "" unless one was given
	Arch     string
	Edition  string
	Language string

	// Latest means no build pin: walk the newest-first GA ladder.
	Latest bool
	// BuildPinned means the user named a build explicitly, so it is
	// honoured verbatim — even off the GA table.
	BuildPinned bool
}

// defaultVersion pins what an unqualified spec means per product. 24H2 is
// deliberately not "newest GA": it is the newest release proven to
// publish assemblable ESD sets. Bump alongside gaReleases.
var defaultVersion = map[string]string{
	"11": "24H2",
	"10": "22H2",
}

// buildForm matches a build pin: a series or a series.qfe number.
var buildForm = regexp.MustCompile(`^\d{5}(\.\d+)?$`)

// Resolve validates the spec, fills defaults and derives the dependent
// axes. It never touches the network: unknown version names fail here,
// before any download starts.
func (s MediaSpec) Resolve() (ResolvedSpec, error) {
	r := ResolvedSpec{
		OS:       strings.ToLower(strings.TrimSpace(s.OS)),
		Arch:     strings.ToLower(strings.TrimSpace(s.Arch)),
		Edition:  strings.ToUpper(strings.TrimSpace(s.Edition)),
		Language: strings.ToLower(strings.TrimSpace(s.Language)),
	}
	if r.OS == "" {
		r.OS = "windows"
	}
	if r.OS != "windows" {
		return r, fmt.Errorf("unsupported os %q (only windows)", s.OS)
	}
	if r.Arch == "" {
		r.Arch = "arm64"
	}
	if r.Arch != "arm64" {
		return r, fmt.Errorf("unsupported arch %q (only arm64 today)", s.Arch)
	}
	if r.Edition == "" {
		r.Edition = "PROFESSIONAL"
	}
	if r.Language == "" {
		r.Language = "en-us"
	}

	product := strings.TrimSpace(s.Product)
	if product != "" && product != "10" && product != "11" {
		return r, fmt.Errorf("unsupported product %q (want 10 or 11)", s.Product)
	}

	version := strings.ToLower(strings.TrimSpace(s.Version))
	build := strings.TrimSpace(s.Build)

	// Back-compat: --version has always accepted build numbers too.
	if buildForm.MatchString(version) {
		if build != "" && build != version {
			return r, fmt.Errorf("conflict: --version %s vs --build %s", version, build)
		}
		build, version = version, ""
	}

	switch {
	case build != "":
		if version == "latest" {
			return r, fmt.Errorf("cannot combine --version latest with --build %s", build)
		}
		if !buildForm.MatchString(build) {
			return r, fmt.Errorf("invalid build %q (want e.g. 26100 or 26100.9278)", build)
		}
		r.BuildPinned = true
		r.Series = buildSeries(build)
		if strings.Contains(build, ".") {
			r.Build = build
		}
		if rel, ok := ReleaseForBuild(build); ok {
			r.Version = rel.Version
			r.Product = rel.Product
		} else {
			// Off the GA table: infer the product from the series
			// threshold (Windows 11 started at 22000).
			n, _ := strconv.Atoi(r.Series)
			if n >= 22000 {
				r.Product = "11"
			} else {
				r.Product = "10"
			}
		}
		if version != "" && !strings.EqualFold(version, r.Version) {
			return r, fmt.Errorf("conflict: build %s is %s %s, not version %s",
				build, productName(r.Product), displayOrUnknown(r.Version), s.Version)
		}
		if product != "" && product != r.Product {
			return r, fmt.Errorf("conflict: build %s belongs to %s, not %s",
				build, productName(r.Product), productName(product))
		}
		return r, nil

	case version == "latest":
		r.Latest = true
		r.Product = product
		if r.Product == "" {
			r.Product = "11"
		}
		return r, nil

	case version != "":
		rel, ok := releaseForName(version, product)
		if !ok {
			return r, fmt.Errorf(
				"version %q is not a GA release winkit knows (ESD assembly needs GA media); "+
					"pass --build to force a specific build", s.Version)
		}
		r.Version, r.Series, r.Product = rel.Version, rel.Series, rel.Product
		return r, nil

	default:
		r.Product = product
		if r.Product == "" {
			r.Product = "11"
		}
		rel, _ := ReleaseForVersion(r.Product, defaultVersion[r.Product])
		r.Version, r.Series = rel.Version, rel.Series
		return r, nil
	}
}

// releaseForName resolves a version name, preferring Windows 11 when the
// name shipped for both products and no product was given.
func releaseForName(version, product string) (Release, bool) {
	candidates := []string{"11", "10"}
	if product != "" {
		candidates = []string{product}
	}
	for _, p := range candidates {
		if rel, ok := ReleaseForVersion(p, version); ok {
			return rel, true
		}
	}
	return Release{}, false
}

func productName(product string) string { return "Windows " + product }

func displayOrUnknown(v string) string {
	if v == "" {
		return "an unnamed series"
	}
	return "version " + v
}

// MatchesBuild reports whether a listed build satisfies the pin.
func (r ResolvedSpec) MatchesBuild(b Build) bool {
	switch {
	case r.Build != "":
		return b.Build == r.Build
	case r.Series != "":
		return b.Build == r.Series || strings.HasPrefix(b.Build, r.Series+".")
	default:
		return true
	}
}

// FilterGA splits candidates into assemblable and skipped. Pre-release
// builds ship install images split across .cab/.msu payloads the
// ESD-only assembler cannot consume, so anything off the GA table is
// skipped — unless the user pinned a build explicitly, which is honoured
// verbatim.
func (r ResolvedSpec) FilterGA(candidates []Build) (kept, skipped []Build) {
	if r.BuildPinned {
		return candidates, nil
	}
	for _, b := range candidates {
		if IsGABuild(b.Build) {
			kept = append(kept, b)
		} else {
			skipped = append(skipped, b)
		}
	}
	return kept, skipped
}

// ISOName is the cache filename for this spec. Every deciding axis is in
// the name so that changing --version or --build can never silently
// reuse another spec's cached ISO.
func (r ResolvedSpec) ISOName() string {
	pin := "latest"
	switch {
	case r.Build != "":
		pin = r.Build
	case r.Version != "":
		pin = strings.ToLower(r.Version)
	case r.Series != "":
		pin = r.Series
	}
	product := r.Product
	if product == "" {
		product = "11"
	}
	return fmt.Sprintf("windows-%s-%s-%s-%s.iso", product, pin, r.Arch, r.Language)
}

// SearchQueries returns the UUP dump listing searches to merge, broadest
// first. A version-qualified query is added for recall: the broad listing
// is capped and preview branches can crowd a pinned release out of it.
func (r ResolvedSpec) SearchQueries() []string {
	broad := fmt.Sprintf("windows %s %s", r.Product, r.Arch)
	if r.Version == "" {
		return []string{broad}
	}
	return []string{broad, fmt.Sprintf("windows %s %s %s", r.Product, strings.ToLower(r.Version), r.Arch)}
}
