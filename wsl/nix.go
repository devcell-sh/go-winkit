package wsl

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed embed/ofd-shim.c
var ofdShimC []byte

// nixDockerfile is the WSL1 Nix rootfs recipe, rendered from
// templates/Dockerfile.nix.tmpl. WSL_USER is the distro's default user,
// matched to the Windows account so `whoami` agrees across the fence.
//
// The load-bearing details, each proven the hard way (CELL-532):
//   - /etc/fstab must exist: WSL1 runs `mount -a` at boot and a missing
//     fstab fails the whole automount pass.
//   - util-linux must be installed and linked into /bin: drvfs automount of
//     C:\ needs /bin/mount.
//   - openssh: an in-distro sshd is the only way to get real PTY sessions
//     (Windows OpenSSH hands WSL pipes: no tab completion, no readline).
var nixDockerfile = mustRenderDockerfile("Dockerfile.nix.tmpl", "")

// DefaultFlakeNix is the embedded home-manager flake, written into the
// docker build context when no external nixhome is given. @USER@ is
// substituted with the distro user at context-write time (flake outputs
// are attribute sets, so the username cannot be a runtime variable) —
// custom placeholders, not Go template syntax, so the files stay valid
// nix for editors and `nix fmt`.
//
//go:embed templates/nixhome/flake.nix
var DefaultFlakeNix string

// DefaultHomeNix is the embedded home-manager module: the shell UX that
// used to be imperative .bashrc appends (completion, PS1, nix on PATH for
// non-login shells), now owned by home-manager. @DISTRO@ becomes the
// distro name in the prompt.
//
//go:embed templates/nixhome/home.nix
var DefaultHomeNix string

// IsFlakeRef reports whether a nixhome value is a remote flake reference
// (passed to nix as-is) rather than a local directory (copied into the
// docker build context).
func IsFlakeRef(nixHome string) bool {
	return strings.Contains(nixHome, "://") ||
		strings.HasPrefix(nixHome, "git+") ||
		strings.HasPrefix(nixHome, "github:") ||
		strings.HasPrefix(nixHome, "gitlab:") ||
		strings.HasPrefix(nixHome, "sourcehut:") ||
		strings.HasPrefix(nixHome, "flake:")
}

// NixRecipe builds the nix image recipe. nixHome selects the home-manager
// configuration baked into the rootfs: empty for the embedded default
// flake, a local directory (must contain a flake exposing
// homeConfigurations.<user>), or a remote flake ref (git+https://…,
// github:owner/repo, …). The CLI feeds this from --nixhome /
// WINKIT_NIXHOME; library callers pass it directly.
func NixRecipe(user, distroName, nixHome string) (Recipe, error) {
	if distroName == "" {
		distroName = "winkit"
	}
	r := Recipe{
		Image:      "nix",
		User:       user,
		DistroName: distroName,
		Dockerfile: nixDockerfile,
		ContextFiles: map[string][]byte{
			"ofd-shim.c": ofdShimC,
		},
		// -lc: a login shell sources /etc/profile -> profile.d/nix.sh,
		// putting nix on PATH; init's default PATH has no nix.
		VerifyCommand:  "nix --version",
		VerifyContains: "nix (Nix)",
	}
	// The template COPYs s6/ into /etc/s6/services unconditionally, so the
	// context must always carry at least the sshd built-in.
	if err := r.AddService(SSHDNixService()); err != nil {
		return Recipe{}, err
	}

	// The build context always carries ./nixhome (the Dockerfile COPYs it):
	// the embedded default flake templated with the user, a copy of the
	// caller's local directory, or a placeholder when a remote ref is used.
	switch {
	case nixHome == "":
		for name, content := range map[string]string{
			"flake.nix": DefaultFlakeNix,
			"home.nix":  DefaultHomeNix,
		} {
			rendered := strings.ReplaceAll(strings.ReplaceAll(content, "@DISTRO@", distroName), "@USER@", user)
			r.ContextFiles["nixhome/"+name] = []byte(rendered)
		}
	case IsFlakeRef(nixHome):
		r.BuildArgs = map[string]string{"NIXHOME_REF": nixHome}
		// A remote ref's content cannot be known without fetching — pass a
		// pinned ref or use noCache to force a rebuild.
		r.Fingerprint = "ref:" + nixHome
		r.ContextFiles["nixhome/.keep"] = nil
	default:
		files, err := readDirFiles(nixHome, "nixhome/")
		if err != nil {
			return Recipe{}, fmt.Errorf("wsl: reading nixhome %s: %w", nixHome, err)
		}
		for k, v := range files {
			r.ContextFiles[k] = v
		}
	}
	return r, nil
}

// readDirFiles loads every file under dir into a map keyed by prefix+relpath
// (slash-separated), so a local nixhome directory participates in both the
// docker build context and the content-derived cache key.
func readDirFiles(dir, prefix string) (map[string][]byte, error) {
	out := map[string][]byte{}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[prefix+filepath.ToSlash(rel)] = data
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
