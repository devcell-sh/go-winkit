package winpe

import (
	"os"
	"path/filepath"
	"testing"
)

// The installed CLI runs from arbitrary directories, where `go build`
// cannot resolve the module. With the prebuilt embedded, cross-compiling
// must not touch the toolchain at all.
func TestCrossCompileService_EmbeddedWorksOutsideModule(t *testing.T) {
	if _, err := embeddedService.ReadFile("embedded/winkit-service_windows_arm64.exe"); err != nil {
		t.Skip("no embedded winkit-service; run `task build` first")
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
	if err := os.Chdir(os.TempDir()); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "winkit-service.exe")
	if err := CrossCompileService(dst, "arm64"); err != nil {
		t.Fatalf("CrossCompileService outside the module: %v", err)
	}
	if st, err := os.Stat(dst); err != nil || st.Size() == 0 {
		t.Fatalf("expected a non-empty binary at %s: %v", dst, err)
	}
}
