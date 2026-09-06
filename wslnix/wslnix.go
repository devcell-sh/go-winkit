// Package wslnix builds the WSL 1-compatible Nix rootfs tarball (.wsl) from
// the nixos/nix Docker image. The tarball ships on the wsl2 answer volume as
// nix.wsl; the first-logon bootstrap imports it as the configured distro.
//
// WSL 1 on purpose: it shares the Windows network stack and filesystem
// drivers (drvfs), needs no nested virtualization, and — after CELL-532 —
// can stat/read the host project share through the WinFsp fixed-disk mount.
package wslnix

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

//go:embed embed/ofd-shim.c
var ofdShimC []byte

// VolumeName is the filename the bootstrap's drive scan looks for.
const VolumeName = "nix.wsl"

// Dockerfile is the WSL1 Nix rootfs recipe. WSL_USER is the distro's default
// user, matched to the Windows account so `whoami` agrees across the fence.
//
// The load-bearing details, each proven the hard way (CELL-532):
//   - /etc/fstab must exist: WSL1 runs `mount -a` at boot and a missing
//     fstab fails the whole automount pass.
//   - util-linux must be installed and linked into /bin: drvfs automount of
//     C:\ needs /bin/mount.
//   - openssh: an in-distro sshd is the only way to get real PTY sessions
//     (Windows OpenSSH hands WSL pipes: no tab completion, no readline).
const Dockerfile = `FROM nixos/nix:latest

ARG WSL_USER=dev
# NIXHOME_REF, when non-empty, is a remote flake ref (git URL, github:owner/
# repo, …) used instead of the COPY'd ./nixhome directory.
ARG NIXHOME_REF=""

# --- WSL configuration (WSL 1: no systemd, no kernel) ---
RUN mkdir -p /etc \
 && printf "[user]\ndefault = ${WSL_USER}\n\n[interop]\nenabled = true\nappendWindowsPath = true\n\n[boot]\nsystemd = false\n" \
      > /etc/wsl.conf

# --- WSL distribution OOBE config ---
RUN printf "[oobe]\ncommand = /etc/wsl-oobe.sh\ndefaultUid = 1000\ndefaultName = nix-wsl1\n\n[shortcut]\nenabled = true\n" \
      > /etc/wsl-distribution.conf

# --- OOBE script: create user on first launch ---
RUN cat > /etc/wsl-oobe.sh <<OOBE
#!/bin/sh
set -e
if id ${WSL_USER} >/dev/null 2>&1; then
  exit 0
fi
adduser -D -u 1000 -s /bin/bash ${WSL_USER}
mkdir -p /etc/sudoers.d
echo '${WSL_USER} ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/${WSL_USER}
chmod 0440 /etc/sudoers.d/${WSL_USER}
OOBE
RUN chmod +x /etc/wsl-oobe.sh

# --- Empty fstab: WSL1 runs "mount -a" at boot; missing fstab causes errors ---
RUN touch /etc/fstab

# --- Nix config: disable sandbox (WSL 1 lacks namespaces) ---
RUN mkdir -p /etc/nix \
 && printf 'sandbox = false\nexperimental-features = nix-command flakes\n' \
      >> /etc/nix/nix.conf

# --- Profile hooks: sourced by login shells via /etc/profile ---
RUN mkdir -p /etc/profile.d \
 && printf '#!/bin/sh\nif [ -e /nix/var/nix/profiles/default/etc/profile.d/nix.sh ]; then\n  . /nix/var/nix/profiles/default/etc/profile.d/nix.sh\nfi\n' \
      > /etc/profile.d/nix.sh \
 && chmod +x /etc/profile.d/nix.sh

# --- Base /etc/profile: make sure login shells run the profile.d hooks ---
# The nixos/nix base image has no guarantee of one; without it a login shell
# (bash -l, wsl.exe -e bash -li, in-distro sshd) gets neither nix on PATH nor
# any of the hooks above.
RUN touch /etc/profile \
 && if ! grep -q 'profile\.d' /etc/profile; then \
      printf '\nfor f in /etc/profile.d/*.sh; do [ -r "$f" ] && . "$f"; done\nunset f\n' >> /etc/profile; \
    fi

# --- Boot-time system utilities (must be available before any user profile) ---
# util-linux: WSL1 runs "mount -a" at boot; missing /bin/mount breaks drvfs.
# glibcLocales: must be in the default profile so the /usr/lib/locale symlink
# resolves (HM's user profile isn't visible to glibc's setlocale at C level).
RUN nix-env -iA nixpkgs.util-linux nixpkgs.glibcLocales nixpkgs.s6

# --- /bin entries for WSL1's /init ---
# /init execs these by absolute path (or a PATH that never includes the nix
# profile) before any profile script runs, so they must exist in /bin:
#   mount/umount: the boot-time "mount -a" fstab pass and drvfs automount
#   bash: #!/bin/bash shebangs and the login-bash wrapper's PATH lookup
# The link target is the profile indirection (stable across generations),
# not a store hash.
RUN ln -sf /nix/var/nix/profiles/default/bin/mount  /bin/mount \
 && ln -sf /nix/var/nix/profiles/default/bin/umount /bin/umount \
 && ln -sf /nix/var/nix/profiles/default/bin/bash   /bin/bash

# --- Locale: symlink the nix locale archive to glibc's standard path so
# setlocale() finds it before any shell starts (Docker ENV is lost on export). ---
RUN mkdir -p /usr/lib/locale \
 && ln -sf /nix/var/nix/profiles/default/lib/locale/locale-archive /usr/lib/locale/locale-archive

# --- Login wrapper: WSL1's /init starts the user's shell without a PTY
# and without login/interactive flags, so bash reads no startup files.
# Docker ENV is lost on export, and WSL1 doesn't read /etc/environment.
# The wrapper re-execs as a login+interactive shell so bash loads
# /etc/profile and ~/.bashrc (HM-managed). ---
RUN mkdir -p /usr/local/bin \
 && printf '#!/bin/sh\nexec bash -li\n' > /usr/local/bin/login-bash \
 && chmod +x /usr/local/bin/login-bash \
 && ln -sf /usr/local/bin/login-bash /bin/login-bash

# --- Create user in the image (OOBE is a fallback) ---
RUN echo "${WSL_USER}:x:1000:1000::/home/${WSL_USER}:/bin/login-bash" >> /etc/passwd \
 && echo "${WSL_USER}:!:1::::::" >> /etc/shadow \
 && mkdir -p /home/${WSL_USER} \
 && chown 1000:1000 /home/${WSL_USER} \
 && mkdir -p /etc/sudoers.d \
 && echo "${WSL_USER} ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/${WSL_USER} \
 && chmod 0440 /etc/sudoers.d/${WSL_USER}

# --- Home Manager: the user environment is declarative nix config, not
# Dockerfile string-appends. The build context always carries ./nixhome
# (the embedded default flake, or the caller's WINKIT_NIXHOME/--nixhome
# directory); NIXHOME_REF overrides it with a remote flake ref. The flake
# must expose homeConfigurations.<user>. Users iterate inside the distro:
# edit ~/.config/home-manager && home-manager switch.
COPY nixhome /etc/nixhome
# Activation runs as root (single-user nix store) with HOME/USER pointing at
# the distro user; the per-user profile dirs must pre-exist or HM aborts
# with "could not find suitable profile directory". chown afterwards so
# "home-manager switch" works as the user inside the distro.
RUN FLAKE="${NIXHOME_REF:-path:/etc/nixhome}" \
 && mkdir -p /home/${WSL_USER}/.local/state/nix/profiles \
      /nix/var/nix/profiles/per-user/${WSL_USER} \
      /nix/var/nix/gcroots/per-user/${WSL_USER} \
 && chown -R 0:0 /home/${WSL_USER} \
 && ACT=$(nix build --no-link --print-out-paths "$FLAKE#homeConfigurations.${WSL_USER}.activationPackage") \
 && HOME=/home/${WSL_USER} USER=${WSL_USER} HOME_MANAGER_BACKUP_EXT=winkit-bak "$ACT/activate" \
 && chown -R 1000:1000 /home/${WSL_USER} \
      /nix/var/nix/profiles/per-user/${WSL_USER} \
      /nix/var/nix/gcroots/per-user/${WSL_USER}

# --- PATH setup: add nix profile dirs so all shells find nix/HM binaries.
# WSL1's minimal PATH is /usr/bin:/bin; instead of symlinking every binary
# there, we extend PATH in profile.d (login shells) and in the s6-init
# wrapper (direct wsl.exe -e invocations). ---
RUN printf "export PATH=\"/home/${WSL_USER}/.nix-profile/bin:/nix/var/nix/profiles/default/bin:\$PATH\"\n" \
      > /etc/profile.d/nix-path.sh \
 && chmod +x /etc/profile.d/nix-path.sh

# --- s6-init: entry point for "wsl.exe -e /bin/s6-init" (no login shell,
# so profile.d is not sourced). Sets PATH + LD_PRELOAD, then execs
# s6-svscan so child run scripts inherit the environment. ---
RUN printf '#!/bin/sh\n. /etc/profile.d/nix-path.sh\n. /etc/profile.d/ofd-shim.sh\nexec s6-svscan /etc/s6/services\n' \
      > /bin/s6-init \
 && chmod +x /bin/s6-init

# --- OFD lock shim: WSL1's lxcore.sys does not implement F_OFD_SETLK
# (fcntl 37), which Nix's C SQLite 3.49+ uses. Without this shim every
# nix-env/nix-build inside the running distro fails with "unable to open
# database file". The shim downgrades OFD locks to classic POSIX locks.
COPY ofd-shim.c /tmp/ofd-shim.c
RUN nix-shell -p gcc --run 'gcc -shared -fPIC -o /usr/lib/ofd-shim.so /tmp/ofd-shim.c -ldl' \
 && rm /tmp/ofd-shim.c
RUN printf 'export LD_PRELOAD=/usr/lib/ofd-shim.so\n' > /etc/profile.d/ofd-shim.sh \
 && chmod +x /etc/profile.d/ofd-shim.sh

# TODO: move s6 service dirs to wslnix/s6/ as templates, COPY into image.
# --- In-distro sshd via s6: WSL1 starts the user shell without a PTY
# (Windows OpenSSH hands WSL pipes), so tab completion and readline are
# broken. An in-distro sshd on port 2223 gives real PTY sessions.
# s6-svscan is the single process supervisor: one Windows scheduled task
# starts it, and it manages sshd (and future services). ---

# sshd_config: local-only, password auth, port 2223.
RUN mkdir -p /etc/ssh \
 && ssh-keygen -A \
 && cat > /etc/ssh/sshd_config <<SSHD
Port 2223
ListenAddress 127.0.0.1
PermitRootLogin yes
PasswordAuthentication yes
UsePAM no
Subsystem sftp internal-sftp
HostKey /etc/ssh/ssh_host_ed25519_key
HostKey /etc/ssh/ssh_host_rsa_key
SSHD

# s6 service directory for sshd.
RUN mkdir -p /etc/s6/services/sshd \
 && printf '#!/bin/sh\nexec sshd -D -e -f /etc/ssh/sshd_config 2>&1\n' \
      > /etc/s6/services/sshd/run \
 && chmod +x /etc/s6/services/sshd/run

# --- Fix sudo: nix cannot install setuid binaries (store is user-owned).
# Copy the real binary to /usr/bin with the setuid bit so "sudo" works. ---
RUN install -m 4755 -o root -g root \
      "$(readlink -f /home/${WSL_USER}/.nix-profile/bin/sudo)" /usr/bin/sudo

CMD ["/bin/bash", "-l"]
`

// DefaultFlakeNix is the embedded home-manager flake, written into the
// docker build context when no external nixhome is given. @USER@ is
// substituted with the distro user at context-write time (flake outputs
// are attribute sets, so the username cannot be a runtime variable).
const DefaultFlakeNix = `{
  description = "winkit home environment";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    home-manager = {
      url = "github:nix-community/home-manager";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs = { nixpkgs, home-manager, ... }: {
    # The guests are Windows-on-ARM VMs, so the distro is aarch64-linux.
    homeConfigurations."@USER@" = home-manager.lib.homeManagerConfiguration {
      pkgs = nixpkgs.legacyPackages.aarch64-linux;
      modules = [ ./home.nix ];
    };
  };
}
`

// DefaultHomeNix is the embedded home-manager module: the shell UX that
// used to be imperative .bashrc appends (completion, PS1, nix on PATH for
// non-login shells), now owned by home-manager.
const DefaultHomeNix = `{ pkgs, ... }:
{
  home.username = "@USER@";
  home.homeDirectory = "/home/@USER@";
  home.stateVersion = "25.05";

  programs.home-manager.enable = true;

  programs.bash = {
    enable = true;
    package = pkgs.bash;
    enableCompletion = true;
    initExtra = ''
      export PS1='\u@@DISTRO@:\w\$ '
      export LOCALE_ARCHIVE=/nix/var/nix/profiles/default/lib/locale/locale-archive
      [ -r /etc/profile.d/nix.sh ] && . /etc/profile.d/nix.sh
    '';
  };

  programs.git.enable = true;

  home.packages = with pkgs; [
    coreutils findutils gnugrep
    sudo openssh
    less ripgrep jq
  ];
}
`

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

// nixHomeFingerprint reduces a nixhome source to a stable string for the
// cache key. Empty → the embedded defaults; flake ref → the ref itself (the
// remote's content cannot be known without fetching — pass a pinned ref or
// use noCache to force a rebuild); local dir → a content hash of its files.
func nixHomeFingerprint(nixHome string) (string, error) {
	if nixHome == "" {
		return DefaultFlakeNix + "\x00" + DefaultHomeNix, nil
	}
	if IsFlakeRef(nixHome) {
		return "ref:" + nixHome, nil
	}
	h := sha256.New()
	err := filepath.WalkDir(nixHome, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(nixHome, path)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", rel, len(data))
		h.Write(data)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("wslnix: hashing nixhome %s: %w", nixHome, err)
	}
	return fmt.Sprintf("dir:%x", h.Sum(nil)), nil
}

// cachePath keys the cached tarball on the distro user, the Dockerfile and
// the nixhome fingerprint: any changing must invalidate (a version bump that
// kept the old name would silently keep shipping the previous rootfs).
func cachePath(cacheDir, wslUser, nixHomeFP string) string {
	sum := sha256.Sum256([]byte(wslUser + "\x00" + Dockerfile + "\x00" + string(ofdShimC) + "\x00" + nixHomeFP))
	return filepath.Join(cacheDir, fmt.Sprintf("nix-wsl1-%s-%x.wsl", wslUser, sum[:4]))
}

// BuildTarball produces the .wsl rootfs tarball via docker build + export,
// caching the result in cacheDir. Requires docker on PATH (callers should
// degrade gracefully when it is absent). Several minutes cold; free warm.
//
// nixHome selects the home-manager configuration baked into the rootfs:
// empty for the embedded default flake, a local directory (must contain a
// flake exposing homeConfigurations.<wslUser>), or a remote flake ref
// (git+https://…, github:owner/repo, …). The CLI feeds this from --nixhome
// / WINKIT_NIXHOME; library callers pass it directly.
func BuildTarball(ctx context.Context, cacheDir, wslUser, distroName, nixHome string, noCache bool, logf func(string, ...any)) (string, error) {
	if distroName == "" {
		distroName = "winkit"
	}
	nixHomeFP, err := nixHomeFingerprint(nixHome)
	if err != nil {
		return "", err
	}
	dest := cachePath(cacheDir, wslUser, nixHomeFP)
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", fmt.Errorf("wslnix: cache dir: %w", err)
	}
	if noCache {
		os.Remove(dest + ".done")
	}
	if _, err := os.Stat(dest + ".done"); err == nil {
		if _, err := os.Stat(dest); err == nil {
			logf("wslnix: using cached tarball %s", dest)
			return dest, nil
		}
		os.Remove(dest + ".done")
	}

	if _, err := exec.LookPath("docker"); err != nil {
		return "", fmt.Errorf("wslnix: docker not on PATH: %w", err)
	}

	dockerCtx, err := os.MkdirTemp("", "wslnix-docker-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dockerCtx)
	if err := os.WriteFile(filepath.Join(dockerCtx, "Dockerfile"), []byte(Dockerfile), 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dockerCtx, "ofd-shim.c"), ofdShimC, 0o644); err != nil {
		return "", err
	}

	// The build context always carries ./nixhome (the Dockerfile COPYs it):
	// the embedded default flake templated with the user, a copy of the
	// caller's local directory, or empty when a remote ref is used instead.
	ctxNixHome := filepath.Join(dockerCtx, "nixhome")
	if err := os.MkdirAll(ctxNixHome, 0o755); err != nil {
		return "", err
	}
	buildArgs := []string{"build", "--build-arg", "WSL_USER=" + wslUser}
	switch {
	case nixHome == "":
		for name, content := range map[string]string{
			"flake.nix": DefaultFlakeNix,
			"home.nix":  DefaultHomeNix,
		} {
			rendered := strings.ReplaceAll(strings.ReplaceAll(content, "@DISTRO@", distroName), "@USER@", wslUser)
			if err := os.WriteFile(filepath.Join(ctxNixHome, name), []byte(rendered), 0o644); err != nil {
				return "", err
			}
		}
	case IsFlakeRef(nixHome):
		buildArgs = append(buildArgs, "--build-arg", "NIXHOME_REF="+nixHome)
	default:
		if err := os.CopyFS(ctxNixHome, os.DirFS(nixHome)); err != nil {
			return "", fmt.Errorf("wslnix: copying nixhome %s: %w", nixHome, err)
		}
	}

	tag := "winkit-nix-wsl1:latest"
	logf("wslnix: docker build (user=%s nixhome=%s) …", wslUser, orDefault(nixHome, "embedded"))
	buildArgs = append(buildArgs, "-t", tag, dockerCtx)
	build := exec.CommandContext(ctx, "docker", buildArgs...)
	build.Stdout = os.Stderr
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		return "", fmt.Errorf("wslnix: docker build: %w", err)
	}

	createOut, err := exec.CommandContext(ctx, "docker", "create", tag).Output()
	if err != nil {
		return "", fmt.Errorf("wslnix: docker create: %w", err)
	}
	containerID := string(createOut)
	for len(containerID) > 0 && (containerID[len(containerID)-1] == '\n' || containerID[len(containerID)-1] == '\r') {
		containerID = containerID[:len(containerID)-1]
	}
	defer exec.Command("docker", "rm", containerID).Run()

	logf("wslnix: docker export | gzip …")
	tmp := dest + ".part"
	if err := exportGzip(ctx, containerID, tmp); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("wslnix: export: %w", err)
	}
	if err := os.Rename(tmp, dest); err != nil {
		return "", err
	}
	if err := os.WriteFile(dest+".done", nil, 0o644); err != nil {
		return "", err
	}
	fi, _ := os.Stat(dest)
	logf("wslnix: tarball ready %s (%.1f MB)", dest, float64(fi.Size())/(1<<20))
	return dest, nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
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
