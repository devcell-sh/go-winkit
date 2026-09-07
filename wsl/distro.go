package wsl

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Distro is a resolvable WSL rootfs source: a docker-built recipe (nix,
// alpine), a URL to a published image (e.g. Canonical's .wsl builds), or a
// local tarball path. Exactly one of Recipe/URL/Path is set.
type Distro struct {
	// Image is the config value this resolved from ("nix", "alpine", the
	// URL, or the path) — used in logs and errors.
	Image  string
	Recipe *Recipe
	URL    string
	Path   string
	// VerifyCommand / VerifyContains: see Recipe. External images get a
	// generic liveness check — their content is the publisher's contract.
	VerifyCommand  string
	VerifyContains string
}

// tarballSuffixes are the local-path/URL forms wsl --import accepts.
var tarballSuffixes = []string{".wsl", ".tar", ".tar.gz", ".tgz", ".tar.xz"}

func hasTarballSuffix(s string) bool {
	for _, suf := range tarballSuffixes {
		if strings.HasSuffix(s, suf) {
			return true
		}
	}
	return false
}

// dockerRefLike reports whether image plausibly names a docker image
// (ubuntu:24.04, ghcr.io/org/img:tag, plain "ubuntu"). Loose on purpose —
// docker itself gives the authoritative error — but rejects strings that
// can only be mistakes (spaces, backslashes, empty).
func dockerRefLike(image string) bool {
	if image == "" {
		return false
	}
	return !strings.ContainsAny(image, " \t\\")
}

// namedVerify holds distro-specific verify commands, keyed by the docker
// ref's base name (tag stripped). A distro without an entry gets the
// generic Linux liveness check.
var namedVerify = map[string]struct{ cmd, contains string }{
	"alpine": {"cat /etc/os-release", "Alpine"},
	"ubuntu": {"cat /etc/os-release", "Ubuntu"},
	"debian": {"cat /etc/os-release", "Debian"},
}

// refBaseName strips the tag and registry path from a docker ref:
// "alpine:3.21" → "alpine", "ghcr.io/org/img:tag" → "img".
func refBaseName(ref string) string {
	if i := strings.LastIndex(ref, "/"); i >= 0 {
		ref = ref[i+1:]
	}
	if i := strings.Index(ref, ":"); i >= 0 {
		ref = ref[:i]
	}
	return ref
}

// DistroFor resolves a config image value to its source. nixHome is only
// meaningful for the nix image; other sources ignore it.
//
// Accepted forms:
//   - "" or any docker ref — docker-built from that base with the
//     universal plumbing template (alpine is the default; ubuntu:24.04,
//     ghcr.io/org/img:tag, …)
//   - "nix"             — docker-built Nix + home-manager rootfs (the one
//     distro with its own template + recipe logic)
//   - "https://…/x.wsl" — published image, downloaded and cached; the
//     publisher owns the WSL plumbing (wsl.conf, default user, …)
//   - "./x.wsl"         — local tarball, shipped verbatim
func DistroFor(image, user, distroName, nixHome string) (Distro, error) {
	if image == "" {
		image = "alpine"
	}
	fromRecipe := func(r Recipe, err error) (Distro, error) {
		if err != nil {
			return Distro{}, err
		}
		return Distro{Image: image, Recipe: &r,
			VerifyCommand: r.VerifyCommand, VerifyContains: r.VerifyContains}, nil
	}
	switch {
	case image == "nix":
		// nix needs recipe logic beyond a template: nixhome resolution
		// and context-file assembly (see NixRecipe).
		return fromRecipe(NixRecipe(user, distroName, nixHome))
	case strings.HasPrefix(image, "http://"), strings.HasPrefix(image, "https://"):
		return Distro{Image: image, URL: image,
			VerifyCommand: "uname -a", VerifyContains: "Linux"}, nil
	case hasTarballSuffix(image):
		return Distro{Image: image, Path: image,
			VerifyCommand: "uname -a", VerifyContains: "Linux"}, nil
	case dockerRefLike(image):
		d, err := fromRecipe(BaseRecipe(image, user, distroName))
		if err == nil {
			if v, ok := namedVerify[refBaseName(image)]; ok {
				d.VerifyCommand, d.VerifyContains = v.cmd, v.contains
				d.Recipe.VerifyCommand, d.Recipe.VerifyContains = v.cmd, v.contains
			}
		}
		return d, err
	default:
		return Distro{}, fmt.Errorf("wsl: unknown image %q (supported: nix, a docker image ref like alpine or ubuntu:24.04, a %s URL, or a local tarball path)",
			image, strings.Join(tarballSuffixes, "/"))
	}
}

// NeedsDocker reports whether materializing this distro runs docker.
// Callers degrade gracefully (skip the preload) when docker is absent —
// but only for docker-built sources; URLs and local files never need it.
func (d Distro) NeedsDocker() bool { return d.Recipe != nil }

// Materialize produces the rootfs tarball and returns its path: a docker
// build for recipes, a cached download for URLs, the file itself for
// local paths.
func (d Distro) Materialize(ctx context.Context, cacheDir string, noCache bool, logf func(string, ...any)) (string, error) {
	switch {
	case d.Recipe != nil:
		return BuildTarball(ctx, cacheDir, *d.Recipe, noCache, logf)
	case d.URL != "":
		return fetchTarball(ctx, cacheDir, d.URL, noCache, logf)
	default:
		if _, err := os.Stat(d.Path); err != nil {
			return "", fmt.Errorf("wsl: local image %s: %w", d.Path, err)
		}
		return d.Path, nil
	}
}

// fetchTarball downloads url into cacheDir, keyed by the URL. The same
// .done-marker protocol as BuildTarball: a torn download can never be
// mistaken for a complete one.
func fetchTarball(ctx context.Context, cacheDir, url string, noCache bool, logf func(string, ...any)) (string, error) {
	sum := sha256.Sum256([]byte(url))
	dest := filepath.Join(cacheDir, fmt.Sprintf("wsl1-url-%x.wsl", sum[:8]))
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", fmt.Errorf("wsl: cache dir: %w", err)
	}
	if noCache {
		os.Remove(dest + ".done")
	}
	if _, err := os.Stat(dest + ".done"); err == nil {
		if _, err := os.Stat(dest); err == nil {
			logf("wsl: using cached image %s (%s)", dest, url)
			return dest, nil
		}
		os.Remove(dest + ".done")
	}

	logf("wsl: downloading %s …", url)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("wsl: fetching %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("wsl: fetching %s: HTTP %s", url, resp.Status)
	}

	tmp := dest + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	n, err := io.Copy(f, resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("wsl: downloading %s: %w", url, err)
	}
	if err := os.Rename(tmp, dest); err != nil {
		return "", err
	}
	if err := os.WriteFile(dest+".done", nil, 0o644); err != nil {
		return "", err
	}
	logf("wsl: image ready %s (%.1f MB)", dest, float64(n)/(1<<20))
	return dest, nil
}
