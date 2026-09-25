package winpe

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/devcell-sh/go-regedit"
)

func TestEncodeUTF16LE(t *testing.T) {
	// "AB" → U+0041 U+0042 U+0000 (NUL terminator) = 6 bytes
	got := encodeUTF16LE("AB")
	want := []byte{0x41, 0x00, 0x42, 0x00, 0x00, 0x00}
	assert.Equal(t, want, got)
}

func TestEncodeUTF16LE_Empty(t *testing.T) {
	got := encodeUTF16LE("")
	// Just NUL terminator: U+0000 = 2 bytes
	want := []byte{0x00, 0x00}
	assert.Equal(t, want, got)
}

func TestEncodeDWordLE(t *testing.T) {
	got := encodeDWordLE(32)
	want := []byte{32, 0, 0, 0}
	assert.Equal(t, want, got)
}

func TestWSL1ServicePatchSet_Structure(t *testing.T) {
	ps := WSL1ServicePatchSet()

	assert.Equal(t, 2, ps.ImageNum, "should target boot.wim image 2")
	require.Len(t, ps.KeyWrites, 17,
		"need WSL1 and support drivers, WSLService, COM registrations, and MSI location")

	bfs := findKeyWrite(t, ps, "bfs")
	assertDWord(t, bfs.Spec, "Type", 2)
	assertDWord(t, bfs.Spec, "Start", 3)
	assertDWord(t, bfs.Spec, "SupportedFeatures", 7)
	assertStringValue(t, bfs.Spec, "Group", regedit.TypeString, "FSFilter Virtualization")
	assertStringValue(t, bfs.Spec, "ImagePath", regedit.TypeExpandString,
		`\SystemRoot\system32\drivers\bfs.sys`)
	assertStringValue(t, bfs.Spec.Subkeys["Instances"].Subkeys["bfs"],
		"Altitude", regedit.TypeString, "150000")
	assertStringValue(t, bfs.Spec.Subkeys["Parameters"], "ProgramData",
		regedit.TypeString, `X:\ProgramData`)

	bindflt := findKeyWrite(t, ps, "bindflt")
	assertDWord(t, bindflt.Spec, "Type", 2)
	assertDWord(t, bindflt.Spec, "Start", 3)
	assertStringValue(t, bindflt.Spec, "Group", regedit.TypeString, "FSFilter Top")
	assertStringValue(t, bindflt.Spec, "ImagePath", regedit.TypeExpandString,
		`\SystemRoot\system32\drivers\bindflt.sys`)
	require.Contains(t, bindflt.Spec.Subkeys, "Instances")
	assertStringValue(t, bindflt.Spec.Subkeys["Instances"].Subkeys["bindflt Instance"],
		"Altitude", regedit.TypeString, "409800")

	// Verify AF_UNIX networking support copied from the full Windows service.
	afunix := findKeyWrite(t, ps, "afunix")
	assertDWord(t, afunix.Spec, "Type", 1)
	assertDWord(t, afunix.Spec, "Start", 3)
	assertStringValue(t, afunix.Spec, "Group", regedit.TypeString, "PNP_TDI")
	assertStringValue(t, afunix.Spec, "ImagePath", regedit.TypeExpandString,
		`\SystemRoot\system32\drivers\afunix.sys`)
	require.Contains(t, afunix.Spec.Subkeys, "Parameters")
	winsock := afunix.Spec.Subkeys["Parameters"].Subkeys["Winsock"]
	assertStringValue(t, winsock, "HelperDllName", regedit.TypeExpandString,
		`%SystemRoot%\system32\wshunix.dll`)
	require.Contains(t, winsock.Subkeys, "0")
	assertDWord(t, winsock.Subkeys["0"], "AddressFamily", 1)

	// Verify the Windows container isolation filesystem filter used by WSL.
	wcifs := findKeyWrite(t, ps, "wcifs")
	assertDWord(t, wcifs.Spec, "Type", 2)
	assertDWord(t, wcifs.Spec, "Start", 3)
	assertStringValue(t, wcifs.Spec, "Group", regedit.TypeString, "FSFilter Virtualization")
	assertStringValue(t, wcifs.Spec, "ImagePath", regedit.TypeExpandString,
		`\SystemRoot\system32\drivers\wcifs.sys`)
	require.Contains(t, wcifs.Spec.Subkeys, "Parameters")
	instances := wcifs.Spec.Subkeys["Parameters"].Subkeys["Instances"]
	assertStringValue(t, instances.Subkeys["wcifs Instance"], "Altitude",
		regedit.TypeString, "189900")

	// The packaged service opens \\Device\\P9Rdr while creating a WSL1
	// instance. WinPE does not carry this redirector or provider by default.
	p9rdr := findKeyWrite(t, ps, "P9Rdr")
	assertDWord(t, p9rdr.Spec, "Type", 1)
	assertDWord(t, p9rdr.Spec, "Start", 3)
	require.Equal(t, regedit.TypeMultiString, p9rdr.Spec.Values["DependOnService"].Type)
	assert.Equal(t, EncodeMultiSz([]string{"RDBSS"}),
		p9rdr.Spec.Values["DependOnService"].Data)
	assertStringValue(t, p9rdr.Spec, "ImagePath", regedit.TypeExpandString,
		`System32\drivers\p9rdr.sys`)

	p9np := findKeyWrite(t, ps, "P9NP")
	provider := p9np.Spec.Subkeys["NetworkProvider"]
	assertStringValue(t, provider, "DeviceName", regedit.TypeString, `\Device\P9Rdr`)
	assertStringValue(t, provider, "ProviderPath", regedit.TypeExpandString,
		`%SystemRoot%\System32\p9np.dll`)
	require.Equal(t, regedit.TypeMultiString, provider.Values["TriggerStartPrefix"].Type)
	assert.Equal(t, EncodeMultiSz([]string{"wsl.localhost", "wsl$"}),
		provider.Values["TriggerStartPrefix"].Data)

	providerOrder := findKeyWritePath(t, ps,
		`ControlSet001\Control\NetworkProvider\Order`)
	assertStringValue(t, providerOrder.Spec, "ProviderOrder", regedit.TypeString,
		"P9NP,LanmanWorkstation")

	// Verify lxss.
	lxss := findKeyWrite(t, ps, "lxss")
	assertDWord(t, lxss.Spec, "Type", 1)
	assertDWord(t, lxss.Spec, "Start", 0)
	assertDWord(t, lxss.Spec, "ErrorControl", 1)
	assertStringValue(t, lxss.Spec, "ImagePath", regedit.TypeExpandString,
		`system32\drivers\lxss.sys`)
	assert.NotContains(t, lxss.Spec.Values, "Group")
	assert.NotContains(t, lxss.Spec.Values, "DependOnService")

	// The lifted WSL package uses its own-process WSLService for both WSL1
	// and WSL2. LxssManager is not present in the package.
	svc := findKeyWritePath(t, ps, `ControlSet001\Services\WSLService`)
	assert.Equal(t, systemHivePath, svc.HivePath)
	assertDWord(t, svc.Spec, "Type", 16) // SERVICE_WIN32_OWN_PROCESS
	assertDWord(t, svc.Spec, "Start", 3) // demand-start after WinPE catalog registration
	assertDWord(t, svc.Spec, "ErrorControl", 1)
	assertStringValue(t, svc.Spec, "ObjectName", regedit.TypeString, "LocalSystem")
	assertStringValue(t, svc.Spec, "ImagePath", regedit.TypeExpandString,
		`"%SystemDrive%\Program Files\WSL\wslservice.exe"`)

	supportIface := findKeyWritePath(t, ps,
		`Classes\Interface\{46f3c96d-ffa3-42f0-b052-52f5e7ecbb08}`)
	assertStringValue(t, supportIface.Spec, "", regedit.TypeString, "IWslSupport")
	assertStringValue(t, supportIface.Spec.Subkeys["ProxyStubClsid32"], "", regedit.TypeString,
		`{e66b0f30-e7b4-4f8c-acfd-d100c46c6278}`)

	supportProxy := findKeyWritePath(t, ps,
		`Classes\CLSID\{e66b0f30-e7b4-4f8c-acfd-d100c46c6278}`)
	assertStringValue(t, supportProxy.Spec, "", regedit.TypeString, "PSFactoryBuffer")
	assertStringValue(t, supportProxy.Spec.Subkeys["InProcServer32"], "", regedit.TypeString,
		`X:\Windows\System32\lxss\wslsupport.dll`)
	assertStringValue(t, supportProxy.Spec.Subkeys["InProcServer32"], "ThreadingModel",
		regedit.TypeString, "Both")

	iface := findKeyWritePath(t, ps,
		`Classes\Interface\{38541BDC-F54F-4CEB-85D0-37F0F3D2617E}`)
	assert.Equal(t, softwareHivePath, iface.HivePath)
	assertStringValue(t, iface.Spec, "", regedit.TypeString, "ILxssUserSession")
	require.Contains(t, iface.Spec.Subkeys, "ProxyStubClsid32")
	assertStringValue(t, iface.Spec.Subkeys["ProxyStubClsid32"], "", regedit.TypeString,
		`{4EA0C6DD-E9FF-48E7-994E-13A31D10DC60}`)

	proxy := findKeyWritePath(t, ps,
		`Classes\CLSID\{4EA0C6DD-E9FF-48E7-994E-13A31D10DC60}`)
	assertStringValue(t, proxy.Spec, "", regedit.TypeString, "PSFactoryBuffer")
	require.Contains(t, proxy.Spec.Subkeys, "InProcServer32")
	assertStringValue(t, proxy.Spec.Subkeys["InProcServer32"], "", regedit.TypeString,
		`X:\Program Files\WSL\wslserviceproxystub.dll`)
	assertStringValue(t, proxy.Spec.Subkeys["InProcServer32"], "ThreadingModel",
		regedit.TypeString, "Both")

	appID := findKeyWritePath(t, ps,
		`Classes\AppID\{370121D2-AA7E-4608-A86D-0BBAB9DA1A60}`)
	assertStringValue(t, appID.Spec, "LocalService", regedit.TypeString, "WSLService")
	for _, name := range []string{"AccessPermission", "LaunchPermission"} {
		require.Contains(t, appID.Spec.Values, name)
		assert.Equal(t, regedit.TypeBinary, appID.Spec.Values[name].Type)
		assert.NotEmpty(t, appID.Spec.Values[name].Data)
	}

	session := findKeyWritePath(t, ps,
		`Classes\CLSID\{a9b7a1b9-0671-405c-95f1-e0612cb4ce7e}`)
	assertStringValue(t, session.Spec, "", regedit.TypeString, "LxssUserSession")
	assertStringValue(t, session.Spec, "AppId", regedit.TypeString,
		`{370121D2-AA7E-4608-A86D-0BBAB9DA1A60}`)

	notify := findKeyWritePath(t, ps,
		`Classes\CLSID\{2B9C59C3-98F1-45C8-B87B-12AE3C7927E8}`)
	require.Contains(t, notify.Spec.Subkeys, "LocalServer32")
	assertStringValue(t, notify.Spec.Subkeys["LocalServer32"], "", regedit.TypeString,
		`"X:\Program Files\WSL\wslhost.exe"`)

	lxssSoftware := findKeyWritePath(t, ps,
		`Microsoft\Windows\CurrentVersion\Lxss`)
	assert.Equal(t, softwareHivePath, lxssSoftware.HivePath)
	require.Contains(t, lxssSoftware.Spec.Subkeys, "MSI")
	msi := lxssSoftware.Spec.Subkeys["MSI"]
	assertStringValue(t, msi, "InstallLocation", regedit.TypeString,
		`X:\Program Files\WSL\`)
	assertStringValue(t, msi, "ProductCode", regedit.TypeString,
		`{D16124C0-602B-4D6D-945F-2D34B9FB4957}`)
	assertStringValue(t, msi, "Version", regedit.TypeString, "2.7.12.0")
}

// --- helpers ---

func findKeyWrite(t *testing.T, ps WimPatchSet, serviceName string) RegistryKeyWrite {
	t.Helper()
	return findKeyWritePath(t, ps, `ControlSet001\Services\`+serviceName)
}

func findKeyWritePath(t *testing.T, ps WimPatchSet, keyPath string) RegistryKeyWrite {
	t.Helper()
	for _, kw := range ps.KeyWrites {
		if kw.KeyPath == keyPath {
			return kw
		}
	}
	t.Fatalf("KeyWrite for %s not found", keyPath)
	return RegistryKeyWrite{}
}

func assertDWord(t *testing.T, key *regedit.Key, name string, expected uint32) {
	t.Helper()
	require.Contains(t, key.Values, name)
	v := key.Values[name]
	assert.Equal(t, regedit.TypeDWord, v.Type, "%s type", name)
	require.Len(t, v.Data, 4, "%s data length", name)
	got := binary.LittleEndian.Uint32(v.Data)
	assert.Equal(t, expected, got, "%s value", name)
}

func assertStringValue(t *testing.T, key *regedit.Key, name string, typ uint32, expected string) {
	t.Helper()
	require.Contains(t, key.Values, name)
	v := key.Values[name]
	assert.Equal(t, typ, v.Type, "%s type", name)
	assert.Equal(t, encodeUTF16LE(expected), v.Data, "%s data", name)
}
