package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/devcell-sh/go-winkit/build/buildopts"
)

// resolveLocalMedia detects a local-media `from:` source (./path.iso).
// For an ISO it returns the absolute path and ok=true; a catalog spec
// ("windows/11-pro-arm64") returns ok=false so the fetch pipeline runs.
// WIM sources are rejected until the build pipeline can master an ISO
// around them — point at the ISO path or the `winkit wim` commands.
func resolveLocalMedia(from string) (string, bool, error) {
	switch buildopts.DetectFrom(from) {
	case buildopts.FromISO:
		abs, err := filepath.Abs(from)
		if err != nil {
			return "", false, err
		}
		if _, err := os.Stat(abs); err != nil {
			return "", false, fmt.Errorf("local media source: %w", err)
		}
		return abs, true, nil
	case buildopts.FromWIM:
		return "", false, fmt.Errorf(
			"from: %s — WIM sources are not supported by the build pipeline yet; "+
				"pass the installer ISO instead (or service the WIM via `winkit wim`)", from)
	default:
		return "", false, nil
	}
}
