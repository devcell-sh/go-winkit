//go:build !cgo

package winpe

import "fmt"

var errNoWimlib = fmt.Errorf("wimlib not available: build with CGO_ENABLED=1 and install libwim (brew install wimlib)")

func TransferWSL1Files(installWimPath, bootWimPath string) ([]string, error) {
	return nil, errNoWimlib
}

func PatchDevcellWim(wimPath string, imageNum int, registryPatches ...RegistryPatch) error {
	return errNoWimlib
}

func InjectWinPEPayload(bootWimPath, injectDir string, registryPatches ...RegistryPatch) error {
	return errNoWimlib
}
