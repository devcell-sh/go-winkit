//go:build !wimlib

package winpe

import "fmt"

// BuildHPVImageFiles requires libwim (VMP transplant into boot.wim). The CLI
// gates the build command behind requireWimlib() before reaching here, so this
// stub only keeps non-wimlib builds compiling.
func BuildHPVImageFiles(cfg HPVImageConfig) (map[string][]byte, error) {
	return nil, fmt.Errorf("hpv stack requires a wimlib-enabled build (-tags wimlib)")
}
