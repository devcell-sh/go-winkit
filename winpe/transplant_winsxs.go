//go:build wimlib

package winpe

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/devcell-sh/go-wimlib"
)

// A stock install.wim ships VirtualMachinePlatform DISABLED, so the VMP
// service binaries are not materialized at their System32 paths — they live
// only in the WinSxS component store (e.g.
// \Windows\WinSxS\arm64_dual_wvmbusr.inf_..._none_.../vmbusr.sys), sometimes
// as DCS-compressed stubs. winSxSIndex maps a lowercased basename to the WIM
// path of that file in the newest matching component directory, so a service
// binary can be sourced without prior DISM enablement.
type winSxSIndex map[string]string

// isVMPFamilyDir reports whether a WinSxS component directory belongs to a
// VMP/Hyper-V family that can hold a transplant binary. Localized
// ".resources" satellites carry no binaries and are skipped.
func isVMPFamilyDir(d string) bool {
	if strings.Contains(d, ".resources") {
		return false
	}
	return strings.HasPrefix(d, "arm64_dual_w") ||
		strings.HasPrefix(d, "arm64_hyperv-") ||
		strings.HasPrefix(d, "arm64_microsoft-hyper-v-")
}

// buildVMPWinSxSIndex scans the VMP/Hyper-V WinSxS component directories of an
// image and indexes every file by basename, keeping the newest component
// version. The naming scheme arm64_<name>_<pubkey>_<version>_none_<hash> sorts
// so that the lexically-largest directory is the newest version.
func buildVMPWinSxSIndex(wim *wimlib.WIM, imageNum int) (winSxSIndex, error) {
	dirs, err := wim.ListChildren(imageNum, `\Windows\WinSxS`)
	if err != nil {
		return nil, fmt.Errorf("listing WinSxS: %w", err)
	}
	idx := winSxSIndex{}
	fromDir := map[string]string{} // basename -> winning component dir
	for _, d := range dirs {
		if !isVMPFamilyDir(d) {
			continue
		}
		children, err := wim.ListChildren(imageNum, `\Windows\WinSxS\`+d)
		if err != nil {
			continue
		}
		for _, c := range children {
			key := strings.ToLower(c)
			if prev, ok := fromDir[key]; !ok || d > prev {
				fromDir[key] = d
				idx[key] = `\Windows\WinSxS\` + d + `\` + c
			}
		}
	}
	return idx, nil
}

// extractImageFile returns the bytes of one image-relative file (forward
// slashes, e.g. "Windows/System32/drivers/vmbusr.sys"). It first tries the
// literal path; if that is absent — the VMP-disabled case — it falls back to
// the file's newest WinSxS component copy via idx, building idx lazily on the
// first miss. DCS-compressed stubs are decompressed natively. A file present
// at neither location returns os.ErrNotExist so callers can apply a skip
// policy.
func extractImageFile(wim *wimlib.WIM, imageNum int, systemFile string, idx *winSxSIndex) ([]byte, error) {
	base := filepath.Base(filepath.FromSlash(systemFile))

	if data, err := extractOne(wim, imageNum, `\`+strings.ReplaceAll(systemFile, "/", `\`), base); err == nil {
		return maybeDCS(data)
	}

	if *idx == nil {
		built, err := buildVMPWinSxSIndex(wim, imageNum)
		if err != nil {
			return nil, err
		}
		*idx = built
	}
	wimPath, ok := (*idx)[strings.ToLower(base)]
	if !ok {
		return nil, fmt.Errorf("%s: %w (absent from System32 and VMP WinSxS)", systemFile, os.ErrNotExist)
	}
	data, err := extractOne(wim, imageNum, wimPath, base)
	if err != nil {
		return nil, fmt.Errorf("%s: extracting %s: %w", systemFile, wimPath, err)
	}
	return maybeDCS(data)
}

// extractOne extracts a single WIM path into a fresh staging dir and returns
// the bytes of the file whose basename matches want (wimlib preserves the full
// path structure under the staging root).
func extractOne(wim *wimlib.WIM, imageNum int, wimPath, want string) ([]byte, error) {
	staging, err := os.MkdirTemp("", "winkit-winsxs-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)

	if err := wim.ExtractPaths(imageNum, staging, []string{wimPath}); err != nil {
		return nil, err
	}

	var found string
	err = filepath.Walk(staging, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.EqualFold(info.Name(), want) {
			found = path
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if found == "" {
		return nil, os.ErrNotExist
	}
	return os.ReadFile(found)
}

// maybeDCS decompresses a DCS stub; other content passes through unchanged.
func maybeDCS(data []byte) ([]byte, error) {
	if wimlib.IsDCS(data) {
		return wimlib.DecompressDCS(data)
	}
	return data, nil
}

// existsInImage reports whether an image-absolute WIM path (backslashes, e.g.
// `\Windows\System32\hvaa64.exe`) is present, by listing its parent. Used to
// prefer an inbox System32 copy over a WinSxS component: the hypervisor
// binaries (hvaa64.exe, hvloader.dll) ship in System32 even with VMP disabled,
// and no WinSxS component name matches them.
func existsInImage(wim *wimlib.WIM, imageNum int, wimPath string) bool {
	slash := strings.LastIndex(wimPath, `\`)
	if slash <= 0 {
		return false
	}
	parent, base := wimPath[:slash], wimPath[slash+1:]
	children, err := wim.ListChildren(imageNum, parent)
	if err != nil {
		return false
	}
	for _, c := range children {
		if strings.EqualFold(c, base) {
			return true
		}
	}
	return false
}
