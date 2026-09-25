//go:build cgo

package winpe

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/devcell-sh/go-regedit"
	"github.com/devcell-sh/go-wimlib"
)

// PatchWIM opens a WIM file, applies all modifications from the patch set,
// and overwrites the file in place.
func PatchWIM(wimPath string, ps WimPatchSet) error {
	if !wimlib.Available() {
		return fmt.Errorf("wimlib not available: build with CGO_ENABLED=1 and install libwim (brew install wimlib)")
	}

	wim, err := wimlib.OpenWIM(wimPath)
	if err != nil {
		return fmt.Errorf("opening WIM: %w", err)
	}
	defer wim.Close()

	// Cleanups remove temp dirs holding extracted registry hives. They must
	// run AFTER Overwrite() because wimlib reads the staged files at that
	// point.
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
		return fmt.Errorf("overwriting WIM: %w", err)
	}
	return nil
}

// applyPatchSet stages all modifications into the open WIM. It returns
// cleanup functions that remove temp directories; the caller must defer
// them and call Overwrite() first.
func applyPatchSet(wim *wimlib.WIM, ps WimPatchSet) (cleanups []func(), _ error) {
	for wimTarget, hostSource := range ps.Files {
		if err := wim.UpdateImageAdd(ps.ImageNum, hostSource, wimTarget); err != nil {
			return cleanups, fmt.Errorf("adding file %s: %w", wimTarget, err)
		}
	}

	for wimTarget, hostSource := range ps.Trees {
		if err := wim.UpdateImageAddTree(ps.ImageNum, hostSource, wimTarget); err != nil {
			return cleanups, fmt.Errorf("adding tree %s: %w", wimTarget, err)
		}
	}

	for _, group := range groupRegistryWrites(ps) {
		cleanup, err := writeRegistryHive(wim, ps.ImageNum, group)
		if err != nil {
			return cleanups, fmt.Errorf("patching registry hive %s: %w", group.hivePath, err)
		}
		cleanups = append(cleanups, cleanup)
	}

	return cleanups, nil
}

type registryWriteGroup struct {
	hivePath     string
	dwordPatches []RegistryPatch
	keyWrites    []RegistryKeyWrite
}

func groupRegistryWrites(ps WimPatchSet) []registryWriteGroup {
	var groups []registryWriteGroup
	index := make(map[string]int)
	ensure := func(hivePath string) int {
		if i, ok := index[hivePath]; ok {
			return i
		}
		index[hivePath] = len(groups)
		groups = append(groups, registryWriteGroup{hivePath: hivePath})
		return len(groups) - 1
	}

	for _, patch := range ps.DWordPatches {
		i := ensure(patch.HivePath)
		groups[i].dwordPatches = append(groups[i].dwordPatches, patch)
	}
	for _, write := range ps.KeyWrites {
		i := ensure(write.HivePath)
		groups[i].keyWrites = append(groups[i].keyWrites, write)
	}
	return groups
}

// writeRegistryHive extracts a hive once, applies every requested mutation to
// that one local copy, and stages it once. Extracting separately for each key
// loses earlier mutations because every extraction sees the original WIM.
func writeRegistryHive(wim *wimlib.WIM, imageNum int, group registryWriteGroup) (func(), error) {
	noop := func() {}

	tmpDir, err := os.MkdirTemp("", "regedit-hive-*")
	if err != nil {
		return noop, fmt.Errorf("creating temp dir: %w", err)
	}
	rm := func() { os.RemoveAll(tmpDir) }

	if err := wim.ExtractPaths(imageNum, tmpDir, []string{group.hivePath}); err != nil {
		rm()
		return noop, fmt.Errorf("extracting %s: %w", group.hivePath, err)
	}

	localPath := filepath.Join(tmpDir, filepath.FromSlash(strings.ReplaceAll(group.hivePath, `\`, `/`)))
	for _, patch := range group.dwordPatches {
		if err := regedit.ApplyDWordPatches(localPath, patch.Patches); err != nil {
			rm()
			return noop, fmt.Errorf("applying DWORD patches: %w", err)
		}
	}
	for _, write := range group.keyWrites {
		if err := regedit.WriteKey(localPath, write.KeyPath, write.Spec); err != nil {
			rm()
			return noop, fmt.Errorf("writing key %s: %w", write.KeyPath, err)
		}
	}

	if err := wim.UpdateImageAdd(imageNum, localPath, group.hivePath); err != nil {
		rm()
		return noop, fmt.Errorf("writing back %s: %w", group.hivePath, err)
	}

	return rm, nil
}
