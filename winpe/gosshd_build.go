package winpe

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// GosshdPackage is the guest SSH server cross-compiled into WinPE payloads.
// Win32-OpenSSH cannot serve sessions in WinPE at all (no user logons), so
// the tree's own server is the only SSH that works there — see package gosshd.
const GosshdPackage = "github.com/devcell-sh/go-winkit/gosshd/cmd/gosshd"

// CrossCompileGosshd builds the gosshd guest binary for windows/<arch> at
// dst using the local Go toolchain. CGO is off so the result is a single
// static binary — WinPE's reduced System32 cannot be assumed to carry any
// particular runtime.
func CrossCompileGosshd(dst, arch string) error {
	build := exec.Command("go", "build", "-o", dst, GosshdPackage)
	build.Env = append(os.Environ(), "GOOS=windows", "GOARCH="+arch, "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("go build %s: %w\n%s", GosshdPackage, err, strings.TrimSpace(string(out)))
	}
	return nil
}
