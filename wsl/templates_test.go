package wsl

import (
	"strings"
	"testing"
)

// Both templates must carry the WSL1 plumbing that cost real debugging
// time when missing (CELL-532): fstab for the boot-time mount -a pass,
// util-linux for drvfs automount of C:, wsl.conf for the default user,
// and the user build-arg. Per-template extras assert what makes each
// distro itself.
func TestTemplatesCarryWSLEssentials(t *testing.T) {
	essentials := []string{
		"ARG WSL_USER",
		"touch /etc/fstab",
		"util-linux",
		"/etc/wsl.conf",
		"/etc/wsl-distribution.conf",
		"sshd",
		"s6-svscan",
		"sudo",
		// Service dirs come from the build context (Recipe.AddService /
		// the embedded sshd catalog), never inline printf.
		"COPY s6/ /etc/s6/services/",
	}
	cases := []struct {
		name    string
		render  func() (string, error)
		extras  []string
		forbids []string
	}{
		{
			name:   "nix",
			render: func() (string, error) { return nixDockerfile, nil },
			extras: []string{
				"FROM nixos/nix:",
				"/bin/mount", "/bin/umount", "/bin/bash",
				"touch /etc/profile",
				"COPY nixhome /etc/nixhome",
				"ARG NIXHOME_REF",
				"homeConfigurations.${WSL_USER}.activationPackage",
			},
			forbids: []string{"mkdir -p /etc/s6/services/sshd"},
		},
		{
			name:   "base-alpine",
			render: func() (string, error) { return renderDockerfile("Dockerfile.base.tmpl", "alpine") },
			extras: []string{"FROM alpine", "login-shell", "/bin/s6-init", "apk add"},
			// The default image must not drag the nix stack in, and no
			// template may recreate service dirs inline.
			forbids: []string{"nix", "mkdir -p /etc/s6/services/sshd"},
		},
		{
			name:   "base-ubuntu",
			render: func() (string, error) { return renderDockerfile("Dockerfile.base.tmpl", "ubuntu:24.04") },
			extras: []string{"FROM ubuntu:24.04", "login-shell", "/bin/s6-init", "apt-get install"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			df, err := c.render()
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range append(essentials, c.extras...) {
				if !strings.Contains(df, want) {
					t.Errorf("%s Dockerfile missing %q", c.name, want)
				}
			}
			for _, bad := range c.forbids {
				if strings.Contains(df, bad) {
					t.Errorf("%s Dockerfile must not reference %q", c.name, bad)
				}
			}
		})
	}
}
