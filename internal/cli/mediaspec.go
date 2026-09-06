package cli

import (
	"github.com/spf13/cobra"

	"github.com/devcell-sh/go-winkit/uupdump"
)

// addMediaSpecFlags registers the orthogonal media-selection flags,
// mapped 1:1 onto uupdump.MediaSpec. Every flag is optional; the empty
// values resolve to Windows 11 24H2 arm64 en-us PROFESSIONAL, so a bare
// invocation means exactly what it always has.
func addMediaSpecFlags(cmd *cobra.Command, spec *uupdump.MediaSpec) {
	f := cmd.Flags()
	f.StringVar(&spec.OS, "os", "windows", "operating system (only windows)")
	f.StringVar(&spec.Product, "product", "", "Windows product line: 10 or 11 (default: derived, else 11)")
	f.StringVar(&spec.Arch, "arch", "arm64", "architecture (only arm64 today)")
	f.StringVar(&spec.Version, "version", "",
		"release name (24h2, latest) — or a build number, kept for back-compat (default: 24h2)")
	f.StringVar(&spec.Build, "build", "",
		"build pin: series (26100) or exact (26100.9278); authoritative, bypasses the GA skip")
	f.StringVar(&spec.Edition, "edition", "", "Windows edition (default: PROFESSIONAL)")
	f.StringVar(&spec.Language, "lang", "", "language code (default: en-us)")
}
