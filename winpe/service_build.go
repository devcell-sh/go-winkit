package winpe

import (
	"embed"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ServicePackage is the winkit-service wrapper cross-compiled for the guest.
const ServicePackage = "github.com/devcell-sh/go-winkit/cmd/winkit-service"

// embeddedService holds prebuilt winkit-service_windows_<arch>.exe
// binaries that `task build` places in embedded/ before compiling winkit,
// so an installed CLI works from any directory. Same arrangement as
// embeddedGosshd; the pattern also matches the directory's README, so a
// checkout without the build artifacts still compiles.
//
//go:embed embedded
var embeddedService embed.FS

// CrossCompileService produces the winkit-service guest binary for
// windows/<arch> at dst: from the embedded prebuilt when this winkit was
// built with `task build`, else by `go build` with the local toolchain
// (which only works inside the go-winkit source tree). CGO is off so the
// result is a single static binary.
func CrossCompileService(dst, arch string) error {
	if data, err := embeddedService.ReadFile("embedded/winkit-service_windows_" + arch + ".exe"); err == nil {
		return os.WriteFile(dst, data, 0o755)
	}
	build := exec.Command("go", "build", "-o", dst, ServicePackage)
	build.Env = append(os.Environ(), "GOOS=windows", "GOARCH="+arch, "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("go build %s: %w\n%s\n"+
			"(this winkit binary has no embedded winkit-service and the working directory "+
			"is not the go-winkit source tree — rebuild winkit with `task build`, "+
			"which embeds winkit-service, or run winkit from the checkout)",
			ServicePackage, err, strings.TrimSpace(string(out)))
	}
	return nil
}
