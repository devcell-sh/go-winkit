package winpe

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// WSL1PEStartupCommand triggers the native Go WSL1 bootstrap inside
	// winkit-service init. The command string is only used as a non-empty
	// marker: init.go calls runBootstrapWSL1 directly in-process.
	WSL1PEStartupCommand = `X:\winkit\winkit-service.exe bootstrap-wsl1`
	WSL1PEDistroName     = "winkit"
	WSL1PEUserName       = "winkit"
	wsl1PEUserPassword   = "Winkit1234"
)

//go:embed runtime/wsl1/*
var wsl1PERuntimeFS embed.FS

var wsl1PERuntimeFiles = []string{
	"prepare-wsl1-disk.txt",
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
// boot.wim. The Go bootstrap-wsl1 verb handles provisioning; these files
// are non-code helpers (diskpart script, wsl.cmd shim).
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
