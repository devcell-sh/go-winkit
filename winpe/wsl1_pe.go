package winpe

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// WSL1PEStartupCommand starts the PowerShell-owned first-boot provisioner.
	// The base image launches it concurrently so gosshd remains available for
	// progress and failure diagnostics while provisioning runs.
	WSL1PEStartupCommand = `X:\winkit\pwsh\pwsh.exe -NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File X:\winkit\bootstrap-wsl1.ps1`
	WSL1PEDistroName     = "winkit"
	WSL1PEUserName       = "winkit"
	wsl1PEUserPassword   = "Winkit1234"
)

//go:embed runtime/wsl1/*
var wsl1PERuntimeFS embed.FS

var wsl1PERuntimeFiles = []string{
	"prepare-wsl1-disk.txt",
	"relocate-wsl.ps1",
	"setup-wsl-user.ps1",
	"probe-wsl1.ps1",
	"bootstrap-wsl1.ps1",
	"status-wsl1.ps1",
	"invoke-wsl.ps1",
	"wsl.cmd",
}

// WSL1PEPatchSet combines the packaged WSL engine, offline service/COM
// registry, and first-boot helpers needed by the standard PE+WSL1 build.
func WSL1PEPatchSet(workDir, wslDir string) (WimPatchSet, error) {
	patch, err := WSL1EnginePatchSet(wslDir)
	if err != nil {
		return WimPatchSet{}, err
	}
	patch.KeyWrites = append(patch.KeyWrites, WSL1ServicePatchSet().KeyWrites...)

	helperDir := filepath.Join(workDir, "wsl1-runtime-helpers")
	if err := os.MkdirAll(helperDir, 0o755); err != nil {
		return WimPatchSet{}, fmt.Errorf("creating WSL1 helper directory: %w", err)
	}
	if patch.Files == nil {
		patch.Files = make(map[string]string)
	}
	for name, contents := range WSL1PERuntimeHelpers() {
		hostPath := filepath.Join(helperDir, name)
		windowsText := strings.ReplaceAll(contents, "\n", "\r\n")
		if err := os.WriteFile(hostPath, []byte(windowsText), 0o644); err != nil {
			return WimPatchSet{}, fmt.Errorf("writing WSL1 helper %s: %w", name, err)
		}
		patch.Files[`\winkit\`+name] = hostPath
	}
	return patch, nil
}

// WSL1PERuntimeHelpers returns the embedded guest runtime staged into
// boot.wim. PowerShell owns provisioning and WSL invocation. The sole CMD
// file only makes the conventional `wsl` command name resolvable by cmd.exe.
func WSL1PERuntimeHelpers() map[string]string {
	replacer := strings.NewReplacer(
		"{{DISTRO_NAME}}", WSL1PEDistroName,
		"{{USER_NAME}}", WSL1PEUserName,
		"{{USER_PASSWORD}}", wsl1PEUserPassword,
	)
	helpers := make(map[string]string, len(wsl1PERuntimeFiles))
	for _, name := range wsl1PERuntimeFiles {
		data, err := wsl1PERuntimeFS.ReadFile("runtime/wsl1/" + name)
		if err != nil {
			panic(fmt.Sprintf("reading embedded WSL1 runtime %s: %v", name, err))
		}
		helpers[name] = replacer.Replace(string(data))
	}
	return helpers
}
