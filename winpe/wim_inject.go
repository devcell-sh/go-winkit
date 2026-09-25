//go:build cgo

package winpe

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/devcell-sh/go-wimlib"
)

// TransferWSL1Files extracts WSL1 component files from install.wim (image 1)
// and injects them into boot.wim (image 2). This bypasses the DISM
// Enable-Feature path, which fails on boot.wim with CBS 0x800f080c.
//
// On Windows 11 ARM64, WSL1 files are split between System32 (wsl.exe,
// wslapi.dll, lxutil.dll, lxss/) and WinSxS (lxcore.sys, bash.exe).
// This function extracts System32 files directly and discovers WinSxS
// component directories by prefix matching.
//
// The returned list contains every WIM path injected into boot.wim,
// so callers can log or verify exactly what was transferred.
func TransferWSL1Files(installWimPath, bootWimPath string) ([]string, error) {
	if !wimlib.Available() {
		return nil, fmt.Errorf("wimlib not available: build with CGO_ENABLED=1 and install libwim")
	}

	tmpDir, err := os.MkdirTemp("", "wsl1-extract-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	src, err := wimlib.OpenWIM(installWimPath)
	if err != nil {
		return nil, fmt.Errorf("opening install.wim: %w", err)
	}
	defer src.Close()

	// Extract System32 files that exist directly.
	system32Paths := append([]string{}, WSL1FilesPaths...)
	system32Paths = append(system32Paths, WSL1ServiceDependencyPaths...)
	system32Paths = append(system32Paths, WSL1InboxSupportPaths...)
	for _, wp := range system32Paths {
		if err := src.ExtractPaths(1, tmpDir, []string{wp}); err != nil {
			continue
		}
	}

	// Discover and extract WSL1 WinSxS component directories.
	winsxsEntries, err := src.ListChildren(1, `\Windows\WinSxS`)
	if err != nil {
		return nil, fmt.Errorf("listing WinSxS: %w", err)
	}
	for _, entry := range winsxsEntries {
		el := strings.ToLower(entry)
		match := false
		for _, kw := range wsl1WinSxSKeywords {
			if strings.Contains(el, kw) {
				match = true
				break
			}
		}
		if !match {
			continue
		}
		wimDir := `\Windows\WinSxS\` + entry
		if err := src.ExtractPaths(1, tmpDir, []string{wimDir}); err != nil {
			continue
		}
	}

	// A component-store transplant also needs the identities and servicing
	// metadata that describe the payload. Without these files WinPE has the
	// binaries but cannot resolve the WSL optional component through CBS.
	metadataSets := []struct {
		dir      string
		keywords []string
		all      bool
	}{
		{dir: `\Windows\WinSxS\Manifests`, keywords: wsl1WinSxSKeywords},
		{dir: `\Windows\WinSxS\FileMaps`, keywords: []string{"lxss"}},
		{dir: `\Windows\servicing\Packages`, keywords: wsl1ServicingPackageKeywords},
		// WinSxS catalog names are content hashes, so their filenames cannot be
		// associated with a component by keyword. Copy the complete, compact
		// catalog set and register it in the live WinPE catalog database before
		// starting transplanted inbox drivers.
		{dir: `\Windows\WinSxS\Catalogs`, all: true},
	}
	for _, set := range metadataSets {
		entries, listErr := src.ListChildren(1, set.dir)
		if listErr != nil {
			return nil, fmt.Errorf("listing %s: %w", set.dir, listErr)
		}
		if set.all {
			if extractErr := src.ExtractPaths(1, tmpDir, []string{set.dir}); extractErr != nil {
				return nil, fmt.Errorf("extracting metadata tree %s: %w", set.dir, extractErr)
			}
			continue
		}
		for _, entry := range entries {
			lower := strings.ToLower(entry)
			if !containsAny(lower, set.keywords) {
				continue
			}
			if extractErr := src.ExtractPaths(1, tmpDir, []string{set.dir + `\` + entry}); extractErr != nil {
				return nil, fmt.Errorf("extracting WSL1 metadata %s: %w", entry, extractErr)
			}
		}
	}

	// Files in a disabled optional component can be stored as DCS containers
	// rather than directly loadable PE images. ExtractPaths preserves those
	// bytes, so materialize them before selecting driver candidates or adding
	// anything to boot.wim. This is the same boundary used by the former VMP
	// transplant path: SCM and the kernel need MZ/PE files, not CBS storage
	// containers.
	if err := materializeDCSFiles(tmpDir); err != nil {
		return nil, fmt.Errorf("materializing WSL1 component payloads: %w", err)
	}

	// Kernel Code Integrity does not consume catalogs directly from the
	// WinSxS catalog archive. Stage them in the system catalog directory as
	// part of the offline image, before CI initializes during WinPE boot.
	// Keep these as hard links so WIM resource deduplication stores only one
	// copy of each catalog payload.
	if err := stageCodeIntegrityCatalogs(tmpDir); err != nil {
		return nil, fmt.Errorf("staging component catalogs for Code Integrity: %w", err)
	}

	src.Close()

	// Collect every extracted file and inject into boot.wim.
	type injection struct {
		hostPath string
		wimPath  string
	}
	var injections []injection
	err = filepath.Walk(tmpDir, func(path string, info os.FileInfo, werr error) error {
		if werr != nil || info.IsDir() {
			return werr
		}
		rel, _ := filepath.Rel(tmpDir, path)
		wimTarget := `\` + strings.ReplaceAll(filepath.ToSlash(rel), `/`, `\`)
		injections = append(injections, injection{hostPath: path, wimPath: wimTarget})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking extracted files: %w", err)
	}
	// Copy lxcore.sys and lxss.sys to System32\drivers\ so the service
	// ImagePath values resolve at boot time. The originals live deep in
	// WinSxS; NT won't find them there without a component store lookup.
	// WinSxS has reverse-delta stubs in \r\ subdirs (tiny files); pick
	// the largest match per driver name to get the real binary.
	driverNames := map[string]bool{"lxcore.sys": true, "lxss.sys": true}
	bestDriver := make(map[string]injection) // base name -> largest file
	for _, inj := range injections {
		base := filepath.Base(inj.hostPath)
		if !driverNames[base] {
			continue
		}
		fi, err := os.Stat(inj.hostPath)
		if err != nil {
			continue
		}
		if prev, ok := bestDriver[base]; ok {
			prevFi, _ := os.Stat(prev.hostPath)
			if prevFi != nil && fi.Size() <= prevFi.Size() {
				continue
			}
		}
		bestDriver[base] = inj
	}
	for base, inj := range bestDriver {
		injections = append(injections, injection{
			hostPath: inj.hostPath,
			wimPath:  `\Windows\System32\drivers\` + base,
		})
	}

	if len(injections) == 0 {
		return nil, fmt.Errorf("no WSL1 files found in install.wim")
	}

	dst, err := wimlib.OpenWIM(bootWimPath)
	if err != nil {
		return nil, fmt.Errorf("opening boot.wim: %w", err)
	}
	defer dst.Close()

	// Adding every file with a separate wimlib_update_image call becomes
	// prohibitively slow once the component catalogs are included. Everything
	// extracted above lives under Windows, so add that tree in one operation.
	windowsTree := filepath.Join(tmpDir, "Windows")
	if err := dst.UpdateImageAddTree(2, windowsTree, `\Windows`); err != nil {
		return nil, fmt.Errorf("injecting WSL1 Windows tree into boot.wim: %w", err)
	}

	// The two WinSxS drivers also need System32 aliases. Keep these as targeted
	// updates so the larger real binaries replace any reverse-delta stubs.
	for _, inj := range bestDriver {
		if err := dst.UpdateImageAdd(2, inj.hostPath,
			`\Windows\System32\drivers\`+filepath.Base(inj.hostPath)); err != nil {
			return nil, fmt.Errorf("injecting %s driver alias into boot.wim: %w",
				filepath.Base(inj.hostPath), err)
		}
	}

	transferred := make([]string, 0, len(injections))
	for _, inj := range injections {
		transferred = append(transferred, inj.wimPath)
	}

	if err := dst.Overwrite(); err != nil {
		return nil, fmt.Errorf("overwriting boot.wim: %w", err)
	}
	return transferred, nil
}

const codeIntegrityCatalogDir = `Windows/System32/CatRoot/{F750E6C3-38EE-11D1-85E5-00C04FC295EE}`

// WSL1RuntimeCatalogPath contains the small subset of component catalogs that
// mention the transplanted WSL1 drivers. Registering only this directory in a
// live WinPE guest avoids processing the complete WinSxS catalog archive.
const WSL1RuntimeCatalogPath = `\Windows\winkit\WSL1Catalogs`

var wsl1CatalogMemberNames = []string{
	"afunix.sys",
	"bfs.sys",
	"bindflt.sys",
	"lxcore.sys",
	"lxss.sys",
	"p9rdr.sys",
	"wcifs.sys",
}

func stageCodeIntegrityCatalogs(root string) error {
	sourceDir := filepath.Join(root, "Windows", "WinSxS", "Catalogs")
	targetDir := filepath.Join(root, filepath.FromSlash(codeIntegrityCatalogDir))
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", targetDir, err)
	}
	runtimeDir := filepath.Join(root,
		filepath.FromSlash(strings.TrimPrefix(strings.ReplaceAll(WSL1RuntimeCatalogPath, `\`, `/`), "/")))
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", runtimeDir, err)
	}

	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		return fmt.Errorf("reading %s: %w", sourceDir, err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		sourcePath := filepath.Join(sourceDir, entry.Name())
		targetPath := filepath.Join(targetDir, entry.Name())
		if err := os.Link(sourcePath, targetPath); err != nil {
			return fmt.Errorf("linking catalog %s: %w", entry.Name(), err)
		}

		data, err := os.ReadFile(sourcePath)
		if err != nil {
			return fmt.Errorf("reading catalog %s: %w", entry.Name(), err)
		}
		if catalogContainsAnyMember(data, wsl1CatalogMemberNames) {
			if err := os.Link(sourcePath, filepath.Join(runtimeDir, entry.Name())); err != nil {
				return fmt.Errorf("linking WSL1 runtime catalog %s: %w", entry.Name(), err)
			}
		}
	}
	return nil
}

func catalogContainsAnyMember(catalog []byte, names []string) bool {
	for _, name := range names {
		if bytes.Contains(catalog, catalogMemberUTF16(name)) ||
			bytes.Contains(catalog, catalogMemberUTF16(strings.ToUpper(name))) {
			return true
		}
	}
	return false
}

func catalogMemberUTF16(value string) []byte {
	out := make([]byte, 0, len(value)*2)
	for _, b := range []byte(value) {
		out = append(out, b, 0)
	}
	return out
}

func materializeDCSFiles(root string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}
		if !wimlib.IsDCS(data) {
			return nil
		}

		materialized, err := wimlib.DecompressDCS(data)
		if err != nil {
			return fmt.Errorf("decompressing DCS file %s: %w", path, err)
		}
		if err := os.WriteFile(path, materialized, info.Mode().Perm()); err != nil {
			return fmt.Errorf("writing materialized file %s: %w", path, err)
		}
		return nil
	})
}

// wsl1WinSxSKeywords are lowercased substrings matched against WinSxS
// directory names. Any entry whose lowercase name contains one of these
// keywords is extracted. This is architecture-independent and adapts
// to whatever edition the install.wim came from.
var wsl1WinSxSKeywords = []string{
	"microsoft-windows-lxcore",
	"microsoft-windows-lxss",
	"microsoft-windows-unix-socket-provider",
	"microsoft-windows-unix-winsock-provider",
	"microsoft-windows-bindflt",
	"brokeringfilesystem",
	"olation-file-system", // wimlib abbreviates the long wcifs component name
}

var wsl1ServicingPackageKeywords = []string{
	"microsoft-windows-lxss",
}

func containsAny(value string, keywords []string) bool {
	for _, keyword := range keywords {
		if strings.Contains(value, keyword) {
			return true
		}
	}
	return false
}

// PatchDevcellWim applies registry patches to an on-disk WIM file (typically
// winkit.wim after DISM offline servicing). This is the host-side post-step
// that sets correct Start values for services created or updated by DISM.
func PatchDevcellWim(wimPath string, imageNum int, registryPatches ...RegistryPatch) error {
	return PatchWIM(wimPath, WimPatchSet{
		ImageNum:     imageNum,
		DWordPatches: registryPatches,
	})
}

// InjectWinPEPayload uses wimlib to inject WinPE agent files into boot.wim
// image 2. The injectDir must contain winpeshl.ini, bootstrap.cmd,
// bootstrap.ps1, agent.ps1, and optionally vioserial drivers under drivers/.
// The WIM is modified in-place.
func InjectWinPEPayload(bootWimPath, injectDir string, registryPatches ...RegistryPatch) error {
	if !wimlib.Available() {
		return fmt.Errorf("wimlib not available — build with CGO_ENABLED=1 and install libwim")
	}

	wim, err := wimlib.OpenWIM(bootWimPath)
	if err != nil {
		return fmt.Errorf("opening boot.wim: %w", err)
	}
	defer wim.Close()

	count, err := wim.ImageCount()
	if err != nil {
		return fmt.Errorf("getting image count: %w", err)
	}
	if count < 2 {
		return fmt.Errorf("boot.wim has %d images, need at least 2", count)
	}

	ps := WimPatchSet{
		ImageNum: 2,
		Files: map[string]string{
			`\Windows\System32\winpeshl.ini`: filepath.Join(injectDir, "winpeshl.ini"),
		},
		Trees: map[string]string{
			`\winkit`: injectDir,
		},
		DWordPatches: registryPatches,
	}

	cleanups, err := applyPatchSet(wim, ps)
	defer func() {
		for _, fn := range cleanups {
			fn()
		}
	}()
	if err != nil {
		return err
	}

	if err := wim.Overwrite(); err != nil {
		return fmt.Errorf("overwriting boot.wim: %w", err)
	}
	return nil
}
