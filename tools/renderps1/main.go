// renderps1 renders the embedded PowerShell templates with representative
// sample data so PSScriptAnalyzer can lint the actual scripts a consumer
// ships. Wired into `task test:powershell:lint`.
//
// Usage: go run ./tools/renderps1 [-out dir]
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/devcell-sh/go-winkit/templates"
	"github.com/devcell-sh/go-winkit/unattend"
)

type rendered struct {
	name   string
	script string
}

func main() {
	out := flag.String("out", "", "output directory (required)")
	flag.Parse()
	if *out == "" {
		fmt.Fprintln(os.Stderr, "usage: renderps1 -out <dir>")
		os.Exit(1)
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir %s: %v\n", *out, err)
		os.Exit(1)
	}

	const distro = "NixOS"

	scripts := []rendered{
		// provision templates
		{"provision--ssh-config.ps1", templates.Render("provision/ssh-config.ps1.tmpl", struct{ PubKey string }{"ssh-ed25519 AAAA_PLACEHOLDER_KEY"})},
		{"provision--create-session-user.ps1", templates.Render("provision/create-session-user.ps1.tmpl", struct {
			Username string
			Password string
		}{"testuser", "P@ssw0rd!"})},
		{"provision--harden-emulation.ps1", templates.Render("provision/harden-emulation.ps1.tmpl", nil)},
		{"provision--dev-tools.ps1", templates.Render("provision/dev-tools.ps1.tmpl", nil)},
		{"provision--project-mount.ps1", templates.Render("provision/project-mount.ps1.tmpl", struct{ ProjectName string }{"myproject"})},

		// devenv templates
		{"devenv--driver-trust.ps1", templates.Render("devenv/driver-trust.ps1.tmpl", nil)},
		{"devenv--virtio-agent-install.ps1", templates.Render("devenv/virtio-agent-install.ps1.tmpl", nil)},
		{"devenv--winfsp-install.ps1", templates.Render("devenv/winfsp-install.ps1.tmpl", nil)},
		{"devenv--virtiofs-mount.ps1", templates.Render("devenv/virtiofs-mount.ps1.tmpl", struct {
			Tag   string
			Drive string
		}{"devcell-project", "Z"})},
		{"devenv--virtualization-probe.ps1", templates.Render("devenv/virtualization-probe.ps1.tmpl", nil)},
		{"devenv--wsl2-enable.ps1", templates.Render("devenv/wsl2-enable.ps1.tmpl", nil)},
		{"devenv--wsl-engine-install.ps1", templates.Render("devenv/wsl-engine-install.ps1.tmpl", nil)},
		{"devenv--hyperv-enable.ps1", templates.Render("devenv/hyperv-enable.ps1.tmpl", nil)},
		{"devenv--hyperv-verify.ps1", templates.Render("devenv/hyperv-verify.ps1.tmpl", nil)},
		{"devenv--nixos-wsl-import.ps1", templates.Render("devenv/nixos-wsl-import.ps1.tmpl", struct{ Distro string }{distro})},
		{"devenv--wsl-user.ps1", templates.Render("devenv/wsl-user.ps1.tmpl", struct {
			User   string
			Distro string
		}{"testuser", distro})},
		{"devenv--nix-verify.ps1", templates.Render("devenv/nix-verify.ps1.tmpl", struct{ Distro string }{distro})},
		{"devenv--home-manager.ps1", templates.Render("devenv/home-manager.ps1.tmpl", struct {
			User   string
			Mount  string
			Drive  string
			Distro string
		}{"testuser", "/mnt/z", "Z", distro})},

		// bootstrap
		{"bootstrap.ps1", string(unattend.GenerateBootstrapScript(unattend.Config{
			Username:       "testuser",
			Password:       "P@ssw0rd!",
			SSHPubKey:      "ssh-ed25519 AAAA_PLACEHOLDER_KEY",
			Hostname:       "DEVCELL-TEST",
			OpenSSHPayload: "openssh-arm64.zip",
		}))},
	}

	for _, s := range scripts {
		p := filepath.Join(*out, s.name)
		if err := os.WriteFile(p, []byte(s.script), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "write %s: %v\n", p, err)
			os.Exit(1)
		}
		fmt.Printf("  rendered %s\n", s.name)
	}
	fmt.Printf("\n%d scripts rendered to %s\n", len(scripts), *out)
}
