package winpe

import (
	"os"
)

const ImplorerVolumeName = "implorer.exe"

// CrossCompileImplorer produces the implorer desktop shell for
// windows/<arch> at dst: from the prebuilt binary in embedded/ when this
// winkit was built with `task build`, else returns an error. Unlike
// gosshd and winkit-service, implorer is a separate Go module (Wails v3
// dependency) and cannot be built by `go build` from this tree.
func CrossCompileImplorer(dst, arch string) error {
	data, err := embeddedService.ReadFile("embedded/implorer_windows_" + arch + ".exe")
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o755)
}
