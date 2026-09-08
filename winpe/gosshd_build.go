package winpe

import (
	"embed"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// GosshdPackage is the guest SSH server cross-compiled into WinPE payloads.
// Win32-OpenSSH cannot serve sessions in WinPE at all (no user logons), so
// the tree's own server is the only SSH that works there — see package gosshd.
const GosshdPackage = "github.com/devcell-sh/go-winkit/gosshd/cmd/gosshd"

// embeddedGosshd holds prebuilt gosshd_windows_<arch>.exe binaries that
// `task build` places in embedded/ before compiling winkit, so an
// installed CLI works from any directory. The pattern also matches the
// directory's README, so a checkout without the build artifacts still
// compiles.
//
//go:embed embedded
var embeddedGosshd embed.FS

// CrossCompileGosshd produces the gosshd guest binary for windows/<arch>
// at dst: from the embedded prebuilt when this winkit was built with
// `task build`, else by `go build` with the local toolchain (which only
// works inside the go-winkit source tree). CGO is off so the result is a
// single static binary — WinPE's reduced System32 cannot be assumed to
// carry any particular runtime.
func CrossCompileGosshd(dst, arch string) error {
	if data, err := embeddedGosshd.ReadFile("embedded/gosshd_windows_" + arch + ".exe"); err == nil {
		return os.WriteFile(dst, data, 0o755)
	}
	build := exec.Command("go", "build", "-o", dst, GosshdPackage)
	build.Env = append(os.Environ(), "GOOS=windows", "GOARCH="+arch, "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("go build %s: %w\n%s\n"+
			"(this winkit binary has no embedded gosshd and the working directory "+
			"is not the go-winkit source tree — rebuild winkit with `task build`, "+
			"which embeds gosshd, or run winkit from the checkout)",
			GosshdPackage, err, strings.TrimSpace(string(out)))
	}
	return nil
}
