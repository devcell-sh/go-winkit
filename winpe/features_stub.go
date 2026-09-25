//go:build !cgo

package winpe

import "fmt"

// InspectFeatures requires libwim to read boot.wim. The CLI gates the
// list-features command behind requireWimlib() before reaching here.
func InspectFeatures(wimPath string, imageNum int) ([]FeatureGroup, error) {
	return nil, fmt.Errorf("list-features requires wimlib: build with CGO_ENABLED=1 and install libwim (brew install wimlib)")
}

// ListImageDir requires libwim to read the WIM.
func ListImageDir(wimPath string, imageNum int, dir string) ([]string, error) {
	return nil, fmt.Errorf("list-features requires wimlib: build with CGO_ENABLED=1 and install libwim (brew install wimlib)")
}
