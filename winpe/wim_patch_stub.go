//go:build !cgo

package winpe

import "fmt"

func PatchWIM(wimPath string, ps WimPatchSet) error {
	return fmt.Errorf("wimlib not available: build with CGO_ENABLED=1 and install libwim (brew install wimlib)")
}
