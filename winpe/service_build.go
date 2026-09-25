package winpe

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ServicePackage is the winkit-service wrapper cross-compiled for the guest.
const ServicePackage = "github.com/devcell-sh/go-winkit/cmd/winkit-service"

// CrossCompileService produces the winkit-service guest binary for
// windows/<arch> at dst. CGO is off so the result is a single static
// binary.
func CrossCompileService(dst, arch string) error {
	build := exec.Command("go", "build", "-o", dst, ServicePackage)
	build.Env = append(os.Environ(), "GOOS=windows", "GOARCH="+arch, "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("go build %s: %w\n%s",
			ServicePackage, err, strings.TrimSpace(string(out)))
	}
	return nil
}
