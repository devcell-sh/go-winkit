package wsl

import (
	"embed"
	"fmt"
	"strings"

	"github.com/devcell-sh/go-winkit/s6"
	"github.com/devcell-sh/go-winkit/sftpshare"
)

// ServicePrefix is the reserved ContextFiles prefix for s6 service dirs;
// the Dockerfile templates COPY it to /etc/s6/services, the scan dir of
// the s6-svscan loop the Windows bootstrap starts via /bin/s6-init.
const ServicePrefix = "s6/"

// builtinS6 is the catalog of baked-in services, stored as real service
// dirs (templates/s6/<flavor>/<name>/run) — the same on-disk shape as a
// user's winkit.yaml services directory and as /etc/s6/services.
//
//go:embed templates/s6
var builtinS6 embed.FS

// builtinService loads one service from the embedded catalog; the embed
// content is compile-time, so failure is a programming error (panic, like
// nixDockerfile's render at package init).
func builtinService(flavor, name string) s6.Service {
	svc, err := s6.FromFS(builtinS6, "templates/s6/"+flavor+"/"+name, name)
	if err != nil {
		panic(err)
	}
	return svc
}

// SSHDBaseService is the in-distro sshd for arbitrary docker bases: the
// run script tolerates sshd living outside PATH (/usr/sbin on debian-family
// bases). Port 2223 gives real PTY sessions where Windows OpenSSH hands
// WSL pipes.
func SSHDBaseService() s6.Service { return builtinService("base", "sshd") }

// SSHDNixService is the sshd variant for the nix rootfs: sshd is on the
// nix profile PATH set by /bin/s6-init, and -f pins the config since nix's
// compiled-in default path differs.
func SSHDNixService() s6.Service { return builtinService("nix", "sshd") }

// RcloneMountService supervises the Windows rclone.exe SFTP mount from the
// distro's s6 loop via WSL1 interop, replacing the winkit-rclone-mount
// scheduled task. The Windows side still owns the mount machinery (WinFsp,
// rclone.exe installed by the bootstrap); s6 owns process lifetime. The
// run script is the embedded templates/s6/common/rclone-mount module with
// the build-time share parameters substituted in.
func RcloneMountService(host string, port int, user, password, drive, volname string) (s6.Service, error) {
	if host == "" || port <= 0 || user == "" || drive == "" {
		return s6.Service{}, fmt.Errorf("wsl: rclone mount needs host, port, user and drive (got host=%q port=%d user=%q drive=%q)", host, port, user, drive)
	}
	svc := builtinService("common", "rclone-mount")
	svc.Run = strings.NewReplacer(
		"@HOST@", host,
		"@PORT@", fmt.Sprintf("%d", port),
		"@USER@", user,
		"@PASS@", shellQuote(password),
		"@DRIVE@", drive,
		"@VOLNAME@", volname,
	).Replace(svc.Run)
	if err := svc.Validate(); err != nil {
		return s6.Service{}, err
	}
	return svc, nil
}

// shellQuote single-quotes s for POSIX sh, escaping embedded quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// DirShareService generates a service that (re)applies the host dir-share
// symlinks every boot: the mount script runs, then the service parks so
// s6-supervise does not respawn it in a loop.
func DirShareService(shares []sftpshare.DirShare) (s6.Service, error) {
	script, err := MountScriptMulti(shares)
	if err != nil {
		return s6.Service{}, err
	}
	// Drop the script's set -e exit and park: the symlinks are idempotent
	// and a supervised long-running no-op keeps svscan quiet.
	return s6.Service{
		Name: "dirshare",
		Run:  script + "exec sleep infinity\n",
	}, nil
}

// AddService registers an s6 service in the recipe's docker build context
// under s6/<name>/. It participates in the cache key like any context
// file, so adding or editing a service invalidates the cached tarball.
// It errors if anything already occupies s6/<name>/ (duplicate service or
// a hand-set ContextFiles entry); use PutService to override.
//
// On bases without an s6 package (dnf family) registered services are
// still baked into /etc/s6/services but are not supervised: /bin/s6-init
// falls back to exec'ing sshd's run script directly.
func (r *Recipe) AddService(svc s6.Service) error {
	prefix := ServicePrefix + svc.Name + "/"
	for k := range r.ContextFiles {
		if strings.HasPrefix(k, prefix) {
			return fmt.Errorf("wsl: service %s already present (key %s); use PutService to override", svc.Name, k)
		}
	}
	return r.putService(svc)
}

// PutService is AddService with replace-or-add semantics: any existing
// files under s6/<name>/ are removed first, so a user-supplied service
// (e.g. sshd from a winkit.yaml services dir) cleanly overrides the
// built-in of the same name.
func (r *Recipe) PutService(svc s6.Service) error {
	prefix := ServicePrefix + svc.Name + "/"
	for k := range r.ContextFiles {
		if strings.HasPrefix(k, prefix) {
			delete(r.ContextFiles, k)
		}
	}
	return r.putService(svc)
}

func (r *Recipe) putService(svc s6.Service) error {
	files, err := svc.ContextFiles(ServicePrefix)
	if err != nil {
		return err
	}
	if r.ContextFiles == nil {
		r.ContextFiles = map[string][]byte{}
	}
	for k, v := range files {
		r.ContextFiles[k] = v
	}
	return nil
}

// AddService registers an s6 service on a docker-built distro. URL and
// local-tarball distros are prebuilt — baking services in is impossible —
// so it errors rather than silently dropping them.
func (d *Distro) AddService(svc s6.Service) error {
	if d.Recipe == nil {
		return fmt.Errorf("wsl: image %q is a prebuilt tarball; s6 services require a docker-built image (a docker ref or nix)", d.Image)
	}
	return d.Recipe.AddService(svc)
}

// PutService is AddService with override semantics; see Recipe.PutService.
func (d *Distro) PutService(svc s6.Service) error {
	if d.Recipe == nil {
		return fmt.Errorf("wsl: image %q is a prebuilt tarball; s6 services require a docker-built image (a docker ref or nix)", d.Image)
	}
	return d.Recipe.PutService(svc)
}
