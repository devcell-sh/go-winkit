// Package wsl builds WSL 1-compatible rootfs tarballs (.wsl) from Docker
// images. A Recipe describes the distro (Dockerfile, build args, context
// files); BuildTarball turns it into a cached tarball that ships on the
// answer volume as distro.wsl and is imported by the first-logon bootstrap.
//
// WSL 1 on purpose: it shares the Windows network stack and filesystem
// drivers (drvfs), needs no nested virtualization, and — after CELL-532 —
// can stat/read the host project share through the WinFsp fixed-disk mount.
package wsl

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// VolumeName is the filename the bootstrap's drive scan looks for.
const VolumeName = "distro.wsl"

// Recipe describes a WSL rootfs built from a Dockerfile. Everything that
// shapes the produced tarball must be in here: the cache key is derived
// from the recipe content, so any change invalidates the cached build.
type Recipe struct {
	// Image is the recipe selector name ("nix", "alpine"); it keys the
	// cache filename and log lines.
	Image string
	// User is the distro's default user, matched to the Windows account.
	User string
	// DistroName is the WSL registration name.
	DistroName string
	// Dockerfile is the full rootfs recipe.
	Dockerfile string
	// BuildArgs are extra --build-arg values (WSL_USER is set from User
	// automatically).
	BuildArgs map[string]string
	// ContextFiles are extra files written into the docker build context,
	// keyed by relative path (subdirectories allowed).
	ContextFiles map[string][]byte
	// Fingerprint is extra cache-key material for inputs not captured
	// above (e.g. a remote flake ref that cannot be content-hashed).
	Fingerprint string
	// VerifyCommand is a /bin/sh -lc command run inside the imported
	// distro after the build; VerifyContains must appear in its output.
	VerifyCommand  string
	VerifyContains string
}

// cachePath keys the cached tarball on everything that shapes the rootfs:
// any change must invalidate (a version bump that kept the old name would
// silently keep shipping the previous rootfs).
func cachePath(cacheDir string, r Recipe) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s\x00%s\x00", r.Image, r.User, r.Dockerfile, r.Fingerprint)
	keys := make([]string, 0, len(r.BuildArgs)+len(r.ContextFiles))
	for k := range r.BuildArgs {
		keys = append(keys, "arg:"+k)
	}
	for k := range r.ContextFiles {
		keys = append(keys, "file:"+k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if strings.HasPrefix(k, "arg:") {
			fmt.Fprintf(h, "%s=%s\x00", k, r.BuildArgs[k[4:]])
		} else {
			fmt.Fprintf(h, "%s\x00", k)
			h.Write(r.ContextFiles[k[5:]])
		}
	}
	return filepath.Join(cacheDir, fmt.Sprintf("wsl1-%s-%s-%x.wsl", imageSlug(r.Image), r.User, h.Sum(nil)[:4]))
}

// BuildTarball produces the .wsl rootfs tarball via docker build + export,
// caching the result in cacheDir. Requires docker on PATH (callers should
// degrade gracefully when it is absent). Several minutes cold; free warm.
func BuildTarball(ctx context.Context, cacheDir string, r Recipe, noCache bool, logf func(string, ...any)) (string, error) {
	if r.User == "" {
		return "", fmt.Errorf("wsl: Recipe.User is empty")
	}
	dest := cachePath(cacheDir, r)
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", fmt.Errorf("wsl: cache dir: %w", err)
	}
	if noCache {
		os.Remove(dest + ".done")
	}
	if _, err := os.Stat(dest + ".done"); err == nil {
		if _, err := os.Stat(dest); err == nil {
			logf("wsl: using cached tarball %s", dest)
			return dest, nil
		}
		os.Remove(dest + ".done")
	}

	if _, err := exec.LookPath("docker"); err != nil {
		return "", fmt.Errorf("wsl: docker not on PATH: %w", err)
	}

	dockerCtx, err := os.MkdirTemp("", "wsl-docker-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dockerCtx)
	if err := os.WriteFile(filepath.Join(dockerCtx, "Dockerfile"), []byte(r.Dockerfile), 0o644); err != nil {
		return "", err
	}
	for rel, data := range r.ContextFiles {
		p := filepath.Join(dockerCtx, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			return "", err
		}
	}

	buildArgs := []string{"build", "--build-arg", "WSL_USER=" + r.User}
	for k, v := range r.BuildArgs {
		buildArgs = append(buildArgs, "--build-arg", k+"="+v)
	}
	tag := "winkit-wsl1-" + imageSlug(r.Image) + ":latest"
	logf("wsl: docker build (image=%s user=%s) …", r.Image, r.User)
	buildArgs = append(buildArgs, "-t", tag, dockerCtx)
	build := exec.CommandContext(ctx, "docker", buildArgs...)
	build.Stdout = os.Stderr
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		return "", fmt.Errorf("wsl: docker build: %w", err)
	}

	createOut, err := exec.CommandContext(ctx, "docker", "create", tag).Output()
	if err != nil {
		return "", fmt.Errorf("wsl: docker create: %w", err)
	}
	containerID := strings.TrimRight(string(createOut), "\r\n")
	defer exec.Command("docker", "rm", containerID).Run()

	logf("wsl: docker export | gzip …")
	tmp := dest + ".part"
	if err := exportGzip(ctx, containerID, tmp); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("wsl: export: %w", err)
	}
	if err := os.Rename(tmp, dest); err != nil {
		return "", err
	}
	if err := os.WriteFile(dest+".done", nil, 0o644); err != nil {
		return "", err
	}
	fi, _ := os.Stat(dest)
	logf("wsl: tarball ready %s (%.1f MB)", dest, float64(fi.Size())/(1<<20))
	return dest, nil
}

func exportGzip(ctx context.Context, containerID, outPath string) error {
	out, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer out.Close()

	gw := gzip.NewWriter(out)
	export := exec.CommandContext(ctx, "docker", "export", containerID)
	export.Stdout = gw
	export.Stderr = os.Stderr
	if err := export.Run(); err != nil {
		return err
	}
	if err := gw.Close(); err != nil {
		return err
	}
	return out.Close()
}
