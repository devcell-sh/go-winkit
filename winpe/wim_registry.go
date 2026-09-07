package winpe

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/devcell-sh/go-regedit"
	"github.com/devcell-sh/go-wimlib"
)

// RegistryPatch describes a set of DWORD modifications to apply to a
// registry hive inside a WIM image. The hive is extracted to a temp file,
// patched, and written back.
type RegistryPatch struct {
	// HivePath is the path inside the WIM image
	// (e.g. `\Windows\System32\config\SYSTEM`).
	HivePath string
	// Patches are the DWORD values to overwrite.
	Patches []regedit.DWordPatch
}

// PatchRegistry extracts a registry hive from a WIM image, applies
// DWORD patches, and writes the modified hive back. The WIM is NOT
// overwritten — call Overwrite() after all modifications are done.
// The returned cleanup function removes the temp directory holding the
// patched hive; call it AFTER Overwrite() completes since wimlib
// needs the file to exist at overwrite time.
func PatchRegistry(wim *wimlib.WIM, imageNum int, rp RegistryPatch) (cleanup func(), err error) {
	noop := func() {}
	if len(rp.Patches) == 0 {
		return noop, nil
	}

	tmpDir, err := os.MkdirTemp("", "regedit-*")
	if err != nil {
		return noop, fmt.Errorf("creating temp dir: %w", err)
	}

	rm := func() { os.RemoveAll(tmpDir) }

	if err := wim.ExtractPaths(imageNum, tmpDir, []string{rp.HivePath}); err != nil {
		rm()
		return noop, fmt.Errorf("extracting %s: %w", rp.HivePath, err)
	}

	localPath := filepath.Join(tmpDir, filepath.FromSlash(strings.ReplaceAll(rp.HivePath, `\`, `/`)))
	if err := regedit.ApplyDWordPatches(localPath, rp.Patches); err != nil {
		rm()
		return noop, fmt.Errorf("patching %s: %w", rp.HivePath, err)
	}

	if err := wim.UpdateImageAdd(imageNum, localPath, rp.HivePath); err != nil {
		rm()
		return noop, fmt.Errorf("writing back %s: %w", rp.HivePath, err)
	}

	return rm, nil
}

// DWordCheck describes a single registry DWORD expectation for
// VerifyRegistry.
type DWordCheck struct {
	KeyPath   string
	ValueName string
	Expected  uint32
	Optional  bool
}

// VerifyRegistry extracts a registry hive from a WIM image and verifies
// that every check's DWORD value matches the expectation. Returns the first
// non-optional mismatch as an error. Optional checks that fail (key/value
// missing) are silently skipped.
func VerifyRegistry(wim *wimlib.WIM, imageNum int, hivePath string, checks []DWordCheck) error {
	tmpDir, err := os.MkdirTemp("", "regedit-verify-*")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := wim.ExtractPaths(imageNum, tmpDir, []string{hivePath}); err != nil {
		return fmt.Errorf("extracting %s: %w", hivePath, err)
	}

	localPath := filepath.Join(tmpDir, filepath.FromSlash(strings.ReplaceAll(hivePath, `\`, `/`)))
	for _, c := range checks {
		val, err := regedit.ReadDWord(localPath, c.KeyPath, c.ValueName)
		if err != nil {
			if c.Optional {
				continue
			}
			return fmt.Errorf("reading %s\\%s: %w", c.KeyPath, c.ValueName, err)
		}
		if val != c.Expected {
			return fmt.Errorf("%s\\%s: got %d, want %d", c.KeyPath, c.ValueName, val, c.Expected)
		}
	}
	return nil
}
