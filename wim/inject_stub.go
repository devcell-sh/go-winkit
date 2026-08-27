//go:build !wimlib

package wim

import "fmt"

func PatchDevcellWim(wimPath string, imageNum int, registryPatches ...RegistryPatch) error {
	return fmt.Errorf("wimlib not available — build with -tags wimlib")
}

func InjectWinPEPayload(bootWimPath, injectDir string, registryPatches ...RegistryPatch) error {
	return fmt.Errorf("wimlib not available — build with -tags wimlib")
}
