package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// runChocolateyBootstrap installs chocolatey and then each listed package
// onto the persistent volume. The install directory is on the writable
// NTFS disk so packages survive VM reboots.
func runChocolateyBootstrap(out io.Writer, packages []string, driveLetter byte) error {
	if len(packages) == 0 {
		return nil
	}

	chocoDir := fmt.Sprintf("%c:\\choco", driveLetter)
	chocoExe := filepath.Join(chocoDir, "bin", "choco.exe")

	if _, err := os.Stat(chocoExe); err != nil {
		bootstrapLog(out, "choco-install", fmt.Sprintf("installing chocolatey to %s", chocoDir))
		if err := installChocolatey(out, chocoDir); err != nil {
			return fmt.Errorf("installing chocolatey: %w", err)
		}
		bootstrapLog(out, "choco-install-ok", "chocolatey installed")
	}

	for _, pkg := range packages {
		bootstrapLog(out, "choco-pkg", fmt.Sprintf("installing package: %s", pkg))
		if err := chocoInstallPackage(out, chocoExe, chocoDir, pkg); err != nil {
			bootstrapLog(out, "choco-pkg-error", fmt.Sprintf("%s: %v (continuing)", pkg, err))
			continue
		}
		bootstrapLog(out, "choco-pkg-ok", fmt.Sprintf("%s installed", pkg))
	}

	return nil
}

func installChocolatey(out io.Writer, chocoDir string) error {
	pwsh := findPwsh()
	if pwsh == "" {
		return fmt.Errorf("pwsh.exe not found")
	}

	if err := os.MkdirAll(chocoDir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", chocoDir, err)
	}

	script := fmt.Sprintf(
		"$ErrorActionPreference = 'Stop'\r\n"+
			"$env:ChocolateyInstall = '%s'\r\n"+
			"Set-ExecutionPolicy Bypass -Scope Process -Force\r\n"+
			"iex ((Invoke-WebRequest -Uri 'https://community.chocolatey.org/install.ps1' -UseBasicParsing).Content)\r\n",
		chocoDir,
	)

	scriptPath := filepath.Join(os.TempDir(), "choco-bootstrap.ps1")
	if err := os.WriteFile(scriptPath, []byte(script), 0o644); err != nil {
		return fmt.Errorf("writing bootstrap script: %w", err)
	}
	defer os.Remove(scriptPath)

	cmd := exec.Command(pwsh, "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", scriptPath)
	cmd.Env = append(os.Environ(), "ChocolateyInstall="+chocoDir)
	cmd.Stdout = out
	cmd.Stderr = out
	return cmd.Run()
}

func chocoInstallPackage(out io.Writer, chocoExe, chocoDir, pkg string) error {
	cmd := exec.Command(chocoExe, "install", pkg, "-y", "--no-progress")
	cmd.Env = append(os.Environ(), "ChocolateyInstall="+chocoDir)
	cmd.Stdout = out
	cmd.Stderr = out
	return cmd.Run()
}
