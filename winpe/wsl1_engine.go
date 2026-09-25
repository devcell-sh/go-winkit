package winpe

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WSL1EngineDestDir is the path used by the official WSL MSI. Keeping that
// layout is important because WSLService locates its tools relative to its
// installation directory.
const WSL1EngineDestDir = `\Program Files\WSL`

// WSL1PackageVersion is the packaged Microsoft WSL runtime whose service and
// COM registration are mirrored by WSL1ServicePatchSet.
const WSL1PackageVersion = "2.7.12"

// WSL1EngineFiles returns the packaged WSL files needed by the WSL1 path.
// The WSL2 kernel, initrd, module VHD, device host, system distro, WSLg, and
// settings application are intentionally excluded.
func WSL1EngineFiles() []string {
	return []string{
		"wslservice.exe",
		"wsl.exe",
		"libwsl.dll",
		"wsldeps.dll",
		"wslserviceproxystub.dll",
		"wslhost.exe",
		"wslrelay.exe",
		"tools/init",
		"tools/bsdtar",
	}
}

// WSL1ServiceDependencyPaths are direct PE-loader dependencies of the
// packaged wslservice.exe that stock WinPE does not carry. They come from
// the same-build install.wim and are materialized if CBS stored them as DCS
// containers. computecore.dll imports vid.dll even when WSLService ultimately
// selects its WSL1 path, so vid.dll must be present for process startup.
var WSL1ServiceDependencyPaths = []string{
	`\Windows\System32\computecore.dll`,
	`\Windows\System32\computenetwork.dll`,
	`\Windows\System32\msi.dll`,
	`\Windows\System32\vid.dll`,
}

// WSL1InboxSupportPaths are inbox networking and filesystem dependencies
// present in a full Windows installation with the WSL optional component
// enabled but absent from stock WinPE.
var WSL1InboxSupportPaths = []string{
	`\Windows\System32\drivers\bfs.sys`,
	`\Windows\System32\drivers\afunix.sys`,
	`\Windows\System32\wshunix.dll`,
	`\Windows\System32\wci.dll`,
	`\Windows\System32\drivers\wcifs.sys`,
	`\Windows\System32\drivers\bindflt.sys`,
	`\Windows\System32\drivers\p9rdr.sys`,
	`\Windows\System32\p9np.dll`,
}

// WSL1EnginePatchSet maps an msiextract WSL directory into boot.wim image 2.
// wslDir must point at the extracted PFiles64/WSL directory.
func WSL1EnginePatchSet(wslDir string) (WimPatchSet, error) {
	ps := WimPatchSet{
		ImageNum: 2,
		Files:    make(map[string]string, len(WSL1EngineFiles())),
	}

	for _, name := range WSL1EngineFiles() {
		hostPath := filepath.Join(wslDir, filepath.FromSlash(name))
		info, err := os.Stat(hostPath)
		if err != nil {
			return WimPatchSet{}, fmt.Errorf("WSL1 engine file %s: %w", name, err)
		}
		if !info.Mode().IsRegular() {
			return WimPatchSet{}, fmt.Errorf("WSL1 engine file %s is not a regular file", name)
		}

		wimPath := WSL1EngineDestDir + `\` + strings.ReplaceAll(name, `/`, `\`)
		ps.Files[wimPath] = hostPath
	}

	return ps, nil
}
