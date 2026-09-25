//go:build cgo

package winpe

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devcell-sh/go-wimlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWIMInjectRoundTrip creates a WIM, adds test files, writes it to disk,
// reopens it, extracts the files, and verifies content hashes match.
func TestWIMInjectRoundTrip(t *testing.T) {
	if !wimlib.Available() {
		t.Skip("wimlib not available")
	}

	tmpDir := t.TempDir()

	testFiles := map[string][]byte{
		`\Windows\System32\drivers\fake.sys`: []byte("fake driver content 0xDEADBEEF"),
		`\Windows\System32\test.exe`:         []byte("test executable payload"),
		`\deep\nested\path\file.txt`:         []byte("deeply nested file"),
	}

	wantHash := make(map[string][32]byte, len(testFiles))
	for wimPath, data := range testFiles {
		wantHash[wimPath] = sha256.Sum256(data)
	}

	wim, err := wimlib.CreateWIM(wimlib.None)
	require.NoError(t, err)

	imgIdx, err := wim.AddEmptyImage("TestImage")
	require.NoError(t, err)
	assert.Equal(t, 1, imgIdx)

	srcDir := filepath.Join(tmpDir, "src")
	require.NoError(t, os.MkdirAll(srcDir, 0o755))

	for wimPath, data := range testFiles {
		hostRel := filepath.FromSlash(strings.ReplaceAll(wimPath, `\`, `/`))
		hostPath := filepath.Join(srcDir, hostRel)
		require.NoError(t, os.MkdirAll(filepath.Dir(hostPath), 0o755))
		require.NoError(t, os.WriteFile(hostPath, data, 0o644))
		require.NoError(t, wim.UpdateImageAdd(imgIdx, hostPath, wimPath))
	}

	wimPath := filepath.Join(tmpDir, "test.wim")
	require.NoError(t, wim.Write(wimPath))
	wim.Close()

	wim2, err := wimlib.OpenWIM(wimPath)
	require.NoError(t, err)
	defer wim2.Close()

	count, err := wim2.ImageCount()
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	extractDir := filepath.Join(tmpDir, "extracted")
	wimPaths := make([]string, 0, len(testFiles))
	for wp := range testFiles {
		wimPaths = append(wimPaths, wp)
	}
	require.NoError(t, wim2.ExtractPaths(1, extractDir, wimPaths))

	for wimP, wantH := range wantHash {
		hostRel := filepath.FromSlash(strings.ReplaceAll(wimP, `\`, `/`))
		hostPath := filepath.Join(extractDir, hostRel)

		data, err := os.ReadFile(hostPath)
		require.NoError(t, err, "reading extracted %s", wimP)

		gotH := sha256.Sum256(data)
		assert.Equal(t, wantH, gotH, "hash mismatch for %s", wimP)
	}
}

// TestWIMInjectRoundTrip_WSL1Paths exercises the exact paths WSL1FilesPaths
// declares plus WinSxS-style deep paths that TransferWSL1Files discovers.
func TestWIMInjectRoundTrip_WSL1Paths(t *testing.T) {
	if !wimlib.Available() {
		t.Skip("wimlib not available")
	}

	tmpDir := t.TempDir()

	// Simulate the files TransferWSL1Files would extract: System32 flat
	// files and WinSxS component directories.
	testFiles := map[string][]byte{
		`\Windows\System32\wsl.exe`:             []byte("wsl-exe-content"),
		`\Windows\System32\wslapi.dll`:          []byte("wslapi-dll-content"),
		`\Windows\System32\lxutil.dll`:          []byte("lxutil-dll-content"),
		`\Windows\System32\computecore.dll`:     []byte("computecore-dll-content"),
		`\Windows\System32\computenetwork.dll`:  []byte("computenetwork-dll-content"),
		`\Windows\System32\msi.dll`:             []byte("msi-dll-content"),
		`\Windows\System32\sc.exe`:              []byte("sc-exe-content"),
		`\Windows\System32\en-US\sc.exe.mui`:    []byte("sc-exe-mui-content"),
		`\Windows\System32\vid.dll`:             []byte("vid-dll-content"),
		`\Windows\System32\drivers\afunix.sys`:  []byte("afunix-driver-content"),
		`\Windows\System32\wshunix.dll`:         []byte("wshunix-helper-content"),
		`\Windows\System32\wci.dll`:             []byte("wci-helper-content"),
		`\Windows\System32\drivers\wcifs.sys`:   []byte("wcifs-driver-content"),
		`\Windows\System32\drivers\p9rdr.sys`:   []byte("p9rdr-driver-content"),
		`\Windows\System32\p9np.dll`:            []byte("p9np-provider-content"),
		`\Windows\System32\lxss\wslsupport.dll`: []byte("wslsupport-dll"),
		`\Windows\WinSxS\arm64_microsoft-windows-lxcore_31bf3856ad364e35_10.0.26100.4202_none_test\lxcore.sys`:                         []byte("lxcore-sys-content-real-driver-binary-padded-to-be-larger"),
		`\Windows\WinSxS\arm64_microsoft-windows-lxcore_31bf3856ad364e35_10.0.26100.4202_none_test\r\lxcore.sys`:                       []byte("stub"),
		`\Windows\WinSxS\arm64_microsoft-windows-lxss-bash_31bf3856ad364e35_10.0.26100.3037_none_test\bash.exe`:                        []byte("bash-exe-content"),
		`\Windows\WinSxS\arm64_microsoft-windows-lxss-manager_31bf3856ad364e35_10.0.26100.1_none_test\bsdtar`:                          []byte("bsdtar-content"),
		`\Windows\WinSxS\arm64_microsoft-windows-lxss-wsl_31bf3856ad364e35_10.0.26100.4202_none_test\wsl.exe`:                          []byte("winsxs-wsl-exe"),
		`\Windows\WinSxS\arm64_microsoft-windows-lxss-wslsupport_31bf3856ad364e35_10.0.26100.1150_none_test\wslsupport-sxs.dll`:        []byte("sxs-wslsupport"),
		`\Windows\WinSxS\arm64_microsoft-windows-lxss-driver_31bf3856ad364e35_10.0.26100.4202_none_test\lxss.sys`:                      []byte("lxss-sys-content"),
		`\Windows\WinSxS\arm64_microsoft-windows-unix-socket-provider_31bf3856ad364e35_10.0.26100.4202_none_test\afunix.sys`:           []byte("afunix-sxs-content"),
		`\Windows\WinSxS\arm64_microsoft-windows-unix-winsock-provider_31bf3856ad364e35_10.0.26100.1882_none_test\wshunix.dll`:         []byte("wshunix-sxs-content"),
		`\Windows\WinSxS\arm64_microsoft-windows-c..olation-file-system_31bf3856ad364e35_10.0.26100.4202_none_test\wcifs.sys`:          []byte("wcifs-sxs-content"),
		`\Windows\WinSxS\Manifests\arm64_microsoft-windows-lxcore_31bf3856ad364e35_10.0.26100.4202_none_test.manifest`:                 []byte("lxcore-manifest"),
		`\Windows\WinSxS\Manifests\arm64_microsoft-windows-unix-socket-provider_31bf3856ad364e35_10.0.26100.4202_none_test.manifest`:   []byte("afunix-manifest"),
		`\Windows\WinSxS\Manifests\arm64_microsoft-windows-c..olation-file-system_31bf3856ad364e35_10.0.26100.4202_none_test.manifest`: []byte("wcifs-manifest"),
		`\Windows\WinSxS\FileMaps\$$_system32_lxss_test.cdf-ms`:                                                                        []byte("lxss-filemap"),
		`\Windows\servicing\Packages\Microsoft-Windows-Lxss-Package~31bf3856ad364e35~arm64~~10.0.26100.4343.mum`:                       []byte("lxss-mum"),
		`\Windows\servicing\Packages\Microsoft-Windows-Lxss-Package~31bf3856ad364e35~arm64~~10.0.26100.4343.cat`:                       []byte("lxss-cat"),
		`\Windows\WinSxS\Catalogs\1111111111111111111111111111111111111111111111111111111111111111.cat`:                                append([]byte("component-catalog:"), catalogMemberUTF16("afunix.sys")...),
		`\Windows\WinSxS\Catalogs\2222222222222222222222222222222222222222222222222222222222222222.cat`:                                []byte("unrelated-component-catalog-must-also-transfer"),
		`\Windows\WinSxS\Catalogs\3333333333333333333333333333333333333333333333333333333333333333.cat`:                                append([]byte("component-catalog:"), catalogMemberUTF16("p9rdr.sys")...),
	}

	wantHash := make(map[string][32]byte, len(testFiles))
	for wp, data := range testFiles {
		wantHash[wp] = sha256.Sum256(data)
	}

	// Create source WIM (simulates install.wim).
	srcWIM, err := wimlib.CreateWIM(wimlib.None)
	require.NoError(t, err)
	_, err = srcWIM.AddEmptyImage("SourceImage")
	require.NoError(t, err)

	srcDir := filepath.Join(tmpDir, "src")
	for wp, data := range testFiles {
		hostRel := filepath.FromSlash(strings.ReplaceAll(wp, `\`, `/`))
		hostPath := filepath.Join(srcDir, hostRel)
		require.NoError(t, os.MkdirAll(filepath.Dir(hostPath), 0o755))
		require.NoError(t, os.WriteFile(hostPath, data, 0o644))
		require.NoError(t, srcWIM.UpdateImageAdd(1, hostPath, wp))
	}

	srcWimPath := filepath.Join(tmpDir, "install.wim")
	require.NoError(t, srcWIM.Write(srcWimPath))
	srcWIM.Close()

	// Create destination WIM with 2 images (simulates boot.wim).
	dstWIM, err := wimlib.CreateWIM(wimlib.None)
	require.NoError(t, err)
	_, err = dstWIM.AddEmptyImage("Image1-Setup")
	require.NoError(t, err)
	_, err = dstWIM.AddEmptyImage("Image2-WinPE")
	require.NoError(t, err)

	dstWimPath := filepath.Join(tmpDir, "boot.wim")
	require.NoError(t, dstWIM.Write(dstWimPath))
	dstWIM.Close()

	// Run TransferWSL1Files.
	transferred, terr := TransferWSL1Files(srcWimPath, dstWimPath)
	require.NoError(t, terr)
	t.Logf("transferred %d files", len(transferred))
	for _, p := range transferred {
		t.Logf("  %s", p)
	}

	// Reopen boot.wim and verify all files are in image 2.
	verifyWIM, err := wimlib.OpenWIM(dstWimPath)
	require.NoError(t, err)
	defer verifyWIM.Close()

	count, err := verifyWIM.ImageCount()
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	extractDir := filepath.Join(tmpDir, "verify")
	allPaths := make([]string, 0, len(testFiles))
	for wp := range testFiles {
		allPaths = append(allPaths, wp)
	}
	require.NoError(t, verifyWIM.ExtractPaths(2, extractDir, allPaths))

	for wp, wantH := range wantHash {
		hostRel := filepath.FromSlash(strings.ReplaceAll(wp, `\`, `/`))
		hostPath := filepath.Join(extractDir, hostRel)

		data, err := os.ReadFile(hostPath)
		require.NoError(t, err, "reading extracted %s", wp)

		gotH := sha256.Sum256(data)
		assert.Equal(t, wantH, gotH, "hash mismatch for %s", wp)
	}

	// Only catalogs that mention a transplanted driver are staged in the
	// fast runtime-registration directory.
	runtimeCatalog := WSL1RuntimeCatalogPath + `\1111111111111111111111111111111111111111111111111111111111111111.cat`
	runtimeDir := filepath.Join(tmpDir, "verify-runtime-catalog")
	require.NoError(t, verifyWIM.ExtractPaths(2, runtimeDir, []string{runtimeCatalog}))
	runtimeHostPath := filepath.Join(runtimeDir,
		filepath.FromSlash(strings.TrimPrefix(strings.ReplaceAll(runtimeCatalog, `\`, `/`), "/")))
	runtimeData, err := os.ReadFile(runtimeHostPath)
	require.NoError(t, err)
	assert.Equal(t,
		wantHash[`\Windows\WinSxS\Catalogs\1111111111111111111111111111111111111111111111111111111111111111.cat`],
		sha256.Sum256(runtimeData))
	runtimeEntries, err := verifyWIM.ListChildren(2, WSL1RuntimeCatalogPath)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"1111111111111111111111111111111111111111111111111111111111111111.cat",
		"3333333333333333333333333333333333333333333333333333333333333333.cat",
	}, runtimeEntries)

	// Component catalogs must also exist in the system CatRoot before boot.
	// Kernel Code Integrity does not load them directly from WinSxS\Catalogs.
	for wp, wantH := range wantHash {
		if !strings.HasPrefix(wp, `\Windows\WinSxS\Catalogs\`) {
			continue
		}
		catalogName := filepath.Base(strings.ReplaceAll(wp, `\`, `/`))
		catalogPath := `\Windows\System32\CatRoot\{F750E6C3-38EE-11D1-85E5-00C04FC295EE}\` + catalogName
		catalogDir := filepath.Join(tmpDir, "verify-catroot-"+catalogName)
		require.NoError(t, verifyWIM.ExtractPaths(2, catalogDir, []string{catalogPath}))
		hostPath := filepath.Join(catalogDir,
			filepath.FromSlash(strings.TrimPrefix(strings.ReplaceAll(catalogPath, `\`, `/`), "/")))
		data, err := os.ReadFile(hostPath)
		require.NoError(t, err, "reading staged catalog %s", catalogPath)
		assert.Equal(t, wantH, sha256.Sum256(data), "staged catalog hash mismatch for %s", catalogPath)
		assert.Contains(t, transferred, catalogPath)
	}

	// Verify driver files are also copied to System32\drivers\ so that
	// the service ImagePath values (e.g. "System32\drivers\lxcore.sys")
	// resolve at boot.
	driverCopies := map[string][]byte{
		`\Windows\System32\drivers\lxcore.sys`: testFiles[`\Windows\WinSxS\arm64_microsoft-windows-lxcore_31bf3856ad364e35_10.0.26100.4202_none_test\lxcore.sys`], // must be the large one, not the \r\ stub
		`\Windows\System32\drivers\lxss.sys`:   testFiles[`\Windows\WinSxS\arm64_microsoft-windows-lxss-driver_31bf3856ad364e35_10.0.26100.4202_none_test\lxss.sys`],
	}
	driverExtractDir := filepath.Join(tmpDir, "verify-drivers")
	driverPaths := make([]string, 0, len(driverCopies))
	for dp := range driverCopies {
		driverPaths = append(driverPaths, dp)
	}
	require.NoError(t, verifyWIM.ExtractPaths(2, driverExtractDir, driverPaths))
	for dp, wantData := range driverCopies {
		hostRel := filepath.FromSlash(strings.ReplaceAll(dp, `\`, `/`))
		hostPath := filepath.Join(driverExtractDir, hostRel)
		data, err := os.ReadFile(hostPath)
		require.NoError(t, err, "reading driver copy %s", dp)
		assert.Equal(t, sha256.Sum256(wantData), sha256.Sum256(data), "driver copy hash mismatch for %s", dp)
	}

	// Verify transferred list includes both WinSxS originals and driver copies.
	assert.Contains(t, transferred, `\Windows\System32\drivers\lxcore.sys`)
	assert.Contains(t, transferred, `\Windows\System32\drivers\lxss.sys`)
}

func TestTransferWSL1FilesRejectsCorruptDCS(t *testing.T) {
	if !wimlib.Available() {
		t.Skip("wimlib not available")
	}

	tmpDir := t.TempDir()
	srcWimPath := filepath.Join(tmpDir, "install.wim")
	dstWimPath := filepath.Join(tmpDir, "boot.wim")

	srcWIM, err := wimlib.CreateWIM(wimlib.None)
	require.NoError(t, err)
	_, err = srcWIM.AddEmptyImage("SourceImage")
	require.NoError(t, err)

	componentPath := `\Windows\WinSxS\arm64_microsoft-windows-lxcore_31bf3856ad364e35_10.0.26100.4202_none_test\lxcore.sys`
	hostPath := filepath.Join(tmpDir, "lxcore.sys")
	require.NoError(t, os.WriteFile(hostPath, []byte("DCS\x01corrupt"), 0o644))
	require.NoError(t, srcWIM.UpdateImageAdd(1, hostPath, componentPath))

	// TransferWSL1Files also inventories component-store metadata. Seed the
	// metadata directories so this fixture reaches the corrupt DCS
	// payload that the test is intended to exercise.
	metadata := map[string]string{
		`\Windows\WinSxS\Manifests\arm64_microsoft-windows-lxcore_test.manifest`:                        "manifest",
		`\Windows\WinSxS\FileMaps\$$_system32_lxss_test.cdf-ms`:                                         "filemap",
		`\Windows\servicing\Packages\Microsoft-Windows-Lxss-Package-test.mum`:                           "package",
		`\Windows\WinSxS\Catalogs\1111111111111111111111111111111111111111111111111111111111111111.cat`: "catalog",
	}
	for wimPath, contents := range metadata {
		path := filepath.Join(tmpDir, filepath.Base(wimPath))
		require.NoError(t, os.WriteFile(path, []byte(contents), 0o644))
		require.NoError(t, srcWIM.UpdateImageAdd(1, path, wimPath))
	}
	require.NoError(t, srcWIM.Write(srcWimPath))
	srcWIM.Close()

	dstWIM, err := wimlib.CreateWIM(wimlib.None)
	require.NoError(t, err)
	_, err = dstWIM.AddEmptyImage("Setup")
	require.NoError(t, err)
	_, err = dstWIM.AddEmptyImage("WinPE")
	require.NoError(t, err)
	require.NoError(t, dstWIM.Write(dstWimPath))
	dstWIM.Close()

	_, err = TransferWSL1Files(srcWimPath, dstWimPath)
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "dcs")
}
