package unattend

import (
	"bytes"
	"encoding/xml"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/devcell-sh/go-winkit/isokit"
	"github.com/devcell-sh/go-winkit/winpe"
)

func TestDefaultConfig(t *testing.T) {
	t.Setenv("USER", "") // exercise the fallback, not the ambient host user
	cfg := DefaultConfig()
	assert.Equal(t, DefaultSessionUser, cfg.Username)
	assert.Equal(t, "rdp", cfg.Password)
	assert.Equal(t, "en-US", cfg.Locale)
	assert.Equal(t, "winkit", cfg.Hostname)
	assert.Equal(t, "winkit", cfg.DistroName)
	assert.Equal(t, "UTC", cfg.TimeZone)
	assert.Empty(t, cfg.VirtIODrivers, "inbox NVMe/usbstor drivers cover the default devices")
}

func TestGenerateXML_ValidXML(t *testing.T) {
	cfg := DefaultConfig()
	out := GenerateXML(cfg)

	var parsed struct {
		XMLName xml.Name `xml:"unattend"`
	}
	require.NoError(t, xml.Unmarshal(out, &parsed), "output must be valid XML")
	assert.Equal(t, "unattend", parsed.XMLName.Local)
}

// WSL1 distros cannot register without the Microsoft-Windows-Subsystem-Linux
// optional feature (wsl --import --version 1 exits -1, run 20260903T160300).
// The specialize dism enables it so the reboot into OOBE completes it before
// the bootstrap imports nix.wsl. It must be valid XML (dism's 3010 exit is
// swallowed) and absent when not requested.
func TestGenerateXML_WSL1FeatureEnable(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EnableWSL1Feature = true
	out := string(GenerateXML(cfg))
	assert.Contains(t, out, "/enable-feature /featurename:Microsoft-Windows-Subsystem-Linux /all /norestart")
	assert.Contains(t, out, `exit /b 0"</Path>`, "a 3010 (restart required) exit must not abort Setup")

	var parsed struct {
		XMLName xml.Name `xml:"unattend"`
	}
	require.NoError(t, xml.Unmarshal([]byte(out), &parsed))

	assert.NotContains(t, string(GenerateXML(DefaultConfig())), "enable-feature",
		"no dism command without EnableWSL1Feature")
}

func TestGenerateXML_ContainsLabConfig(t *testing.T) {
	// LabConfig is a registry key, not an unattend element — the bypasses are
	// written in the windowsPE pass. See
	// TestGenerateXML_BypassesHardwareChecksInWinPE.
	out := string(GenerateXML(DefaultConfig()))
	assert.Contains(t, out, `HKLM\SYSTEM\Setup\LabConfig /v BypassTPMCheck`)
	assert.Contains(t, out, `HKLM\SYSTEM\Setup\LabConfig /v BypassSecureBootCheck`)
	assert.Contains(t, out, `HKLM\SYSTEM\Setup\LabConfig /v BypassRAMCheck`)
}

func TestGenerateXML_ContainsVirtIODrivers(t *testing.T) {
	// Drivers are opt-in now; the default config needs none (CELL-359).
	cfg := DefaultConfig()
	cfg.VirtIODrivers = []VirtIODriver{
		{INFRelPath: `viostor\w11\ARM64\viostor.inf`, Description: "VirtIO storage"},
		{INFRelPath: `NetKVM\w11\ARM64\netkvm.inf`, Description: "VirtIO network"},
	}
	out := string(GenerateXML(cfg))
	assert.Contains(t, out, `viostor\w11\ARM64\viostor.inf`)
	assert.Contains(t, out, `NetKVM\w11\ARM64\netkvm.inf`)
	// One probing command per driver, at distinct Order values.
	assert.Equal(t, 2, strings.Count(out, "pnputil /add-driver"))
}

func TestDefaultConfig_NoVirtIODriversNeeded(t *testing.T) {
	// Storage is NVMe + USB CD — both have inbox Windows ARM64 drivers, so
	// the default install needs no driver injection (CELL-359).
	assert.Empty(t, DefaultConfig().VirtIODrivers)
}

func TestGenerateXML_OmitsDriverPathsWhenNoDrivers(t *testing.T) {
	// An empty <DriverPaths> element makes Setup search nothing; omit it
	// entirely rather than emitting a dangling path to a missing drive.
	out := string(GenerateXML(DefaultConfig()))
	assert.NotContains(t, out, "<DriverPaths>")
}

// --- CELL-429: WinPE storage drivers via DriverPaths + %configsetroot% ---

// ARM64 WinPE has no inbox vioscsi, so with CDs on virtio-scsi Setup stops
// at "a media driver your computer needs is missing". The drivers travel
// under \drivers\vioscsi on the answer volume and load through Setup's own
// PnpCustomizationsWinPE/DriverPaths, pointed at %configsetroot% — the
// volume the answer file itself was read from, so the path always resolves.
// Every alternative is disproven by a run: RunSynchronous drvload aborts
// 0x8007000D (20260812T132820); $WinPEDriver$ never loads — no vioscsi
// service, no CD volumes (20260812T144140); DriverPaths at the virtio CD
// aborts 0x80070001 because the CD is exactly what's invisible
// (20260729T172019).
// DriverPaths is dead weight on this media and must not be emitted: run
// 20260812T150644 logged "SetupManager: Drivers Path: []" with the
// component present and a resolvable %configsetroot% path — Modern Setup
// parses it and ignores it. Every element in windowsPE is a potential
// Setup-abort (an unresolved DriverPaths is how run 20260729T172019 died),
// so a proven no-op is pure risk.
func TestGenerateXML_NeverEmitsDriverPaths(t *testing.T) {
	withDrivers := DefaultConfig()
	withDrivers.AnswerDrivers = map[string][]byte{
		"/drivers/vioscsi/vioscsi.inf": []byte("inf"),
		"/drivers/vioscsi/vioscsi.sys": []byte("sys"),
	}
	for name, cfg := range map[string]Config{
		"no drivers":   DefaultConfig(),
		"with drivers": withDrivers,
	} {
		t.Run(name, func(t *testing.T) {
			xmlBytes := GenerateXML(cfg)
			// Markup, not prose: the template comment explains why the
			// component is absent and necessarily names it.
			assert.NotContains(t, string(xmlBytes), `name="Microsoft-Windows-PnpCustomizationsWinPE"`)
			assert.NotContains(t, string(xmlBytes), "<DriverPaths>")
			assert.Empty(t, Validate(xmlBytes))
		})
	}
}

// windowsPE RunSynchronousCommand <Order> values must be contiguous from 1.
// Run 20260812T132820 shipped orders 1,2,3,4,5,7 — the agent launcher at 6
// was gated off while the driver loader at 7 was gated on — and Setup died
// with 0x8007000D (ERROR_INVALID_DATA) before executing anything. The
// 20260812T150644 log shows the healthy shape: "Parsing
// RunSynchronousCommand: 6 entries", commands 0..5, all exit 0.
func TestGenerateXML_WindowsPEOrdersAreContiguous(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  Config
	}{
		{"bare", DefaultConfig()},
		{"agent only", func() Config {
			c := DefaultConfig()
			c.WinPEAgent = true
			return c
		}()},
		{"drivers only", func() Config {
			c := DefaultConfig()
			c.AnswerDrivers = map[string][]byte{"/drivers/vioscsi/vioscsi.inf": []byte("inf")}
			return c
		}()},
		{"agent and drivers", func() Config {
			c := DefaultConfig()
			c.WinPEAgent = true
			c.AnswerDrivers = map[string][]byte{"/drivers/vioscsi/vioscsi.inf": []byte("inf")}
			return c
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			winPE, _, found := strings.Cut(string(GenerateXML(tc.cfg)), `<settings pass="specialize">`)
			require.True(t, found, "windowsPE section must be delimited")
			// Scope to RunSynchronous: DiskConfiguration carries its own
			// independent <Order> sequences for partitions.
			_, afterOpen, found := strings.Cut(winPE, "<RunSynchronous>")
			require.True(t, found, "windowsPE must have a RunSynchronous block")
			runSync, _, found := strings.Cut(afterOpen, "</RunSynchronous>")
			require.True(t, found, "RunSynchronous block must be closed")

			orders := regexp.MustCompile(`<Order>(\d+)</Order>`).FindAllStringSubmatch(runSync, -1)
			require.NotEmpty(t, orders, "windowsPE always carries the LabConfig bypass commands")
			for i, m := range orders {
				assert.Equal(t, strconv.Itoa(i+1), m[1],
					"order %d of %d is %q — gaps abort Setup with 0x8007000D (run 20260812T132820)",
					i+1, len(orders), m[1])
			}
		})
	}
}

// The vioscsi drvload must run as a windowsPE RunSynchronous command: that
// is the last hook before Modern Setup's media search
// (WinPEInitialization Leaving Execute → EarlyF6DriverInstall Entering
// Execute, one second apart in run 20260812T150644). The agent's poll loop
// is ~4s too late — its drvload landed after the media search had already
// failed and left Setup at 0x80070103 (run 20260812T143146).
func TestGenerateXML_AnswerDriversDrvloadBeforeMediaSearch(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AnswerDrivers = map[string][]byte{
		"/drivers/vioscsi/vioscsi.inf": []byte("inf"),
		"/drivers/vioscsi/vioscsi.sys": []byte("sys"),
	}
	xmlBytes := GenerateXML(cfg)
	s := string(xmlBytes)

	assert.Contains(t, s, `drvload.exe`,
		"the INF must be drvloaded via cmd.exe")
	assert.Contains(t, s, `drivers\vioscsi\vioscsi.inf`,
		"must reference the vioscsi INF")
	assert.Contains(t, s, "exit /b 0",
		"must force success: a non-zero exit in windowsPE aborts Setup")
	assert.NotContains(t, s, `vioscsi.sys`,
		"only .inf files are drvloaded")
	assert.Empty(t, Validate(xmlBytes))
}

func TestBuildAnswerVolume_ShipsAnswerDrivers(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AnswerDrivers = map[string][]byte{
		"/drivers/vioscsi/vioscsi.inf": []byte("[Version]\r\nSignature=\"$WINDOWS NT$\"\r\n"),
		"/drivers/vioscsi/vioscsi.sys": {0x4D, 0x5A, 0x90, 0x00},
	}
	dest := filepath.Join(t.TempDir(), "answer.img")
	require.NoError(t, BuildAnswerVolume(cfg, dest))

	// Drivers are cluster-padded (not byte-exact): they're only consumed
	// by WinPE's drvload which doesn't verify Authenticode; the installed-
	// OS copy comes from the virtio-win CD via pnputil in specialize.
	inf, err := isokit.ReadFileFromFAT(dest, "/drivers/vioscsi/vioscsi.inf")
	require.NoError(t, err)
	want := cfg.AnswerDrivers["/drivers/vioscsi/vioscsi.inf"]
	assert.True(t, len(inf) >= len(want), "read-back must be at least original size")
	assert.Equal(t, want, inf[:len(want)], "INF prefix must match original content")
	sys, err := isokit.ReadFileFromFAT(dest, "/drivers/vioscsi/vioscsi.sys")
	require.NoError(t, err)
	wantSys := cfg.AnswerDrivers["/drivers/vioscsi/vioscsi.sys"]
	assert.True(t, len(sys) >= len(wantSys), "read-back must be at least original size")
	assert.Equal(t, wantSys, sys[:len(wantSys)], "driver binary prefix must match original content")
}

// The agent can execute one pre-baked command and write its output to
// winkit-out.txt — the only way to see drvload's actual error text and
// diskpart's volume list inside a WinPE that has no network and no QGA.
func TestBuildAnswerVolume_ShipsAgentCommand(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WinPEAgent = true
	cfg.AgentCommand = `& drvload.exe "$WinkitVol\$WinPEDriver$\vioscsi\vioscsi.inf"; Write-Output "DRVLOAD_RC=$LASTEXITCODE"`
	dest := filepath.Join(t.TempDir(), "answer.img")
	require.NoError(t, BuildAnswerVolume(cfg, dest))

	cmdFile, err := isokit.ReadFileFromFAT(dest, "/"+winpe.AgentCommandFile)
	require.NoError(t, err)
	firstLine, _, _ := strings.Cut(string(cmdFile), "\n")
	assert.Equal(t, cfg.AgentCommand, strings.TrimRight(firstLine, "\r "),
		"the agent reads the first line via Get-Content — it must be the command verbatim")
}

func TestBuildAnswerVolume_NoAgentCommandFileWithoutCommand(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WinPEAgent = true
	dest := filepath.Join(t.TempDir(), "answer.img")
	require.NoError(t, BuildAnswerVolume(cfg, dest))
	_, err := isokit.ReadFileFromFAT(dest, "/"+winpe.AgentCommandFile)
	assert.Error(t, err, "no command file unless a command was configured")
}

func TestGenerateXML_ContainsUserCreation(t *testing.T) {
	t.Setenv("USER", "")
	out := string(GenerateXML(DefaultConfig()))
	assert.Contains(t, out, "<Name>winkit</Name>")
	assert.Contains(t, out, "<Group>Administrators</Group>")
	assert.Contains(t, out, "<Username>winkit</Username>")
}

// SSH setup, key injection, power settings and diagnostics all moved out of
// the XML into the generated bootstrap script — see bootstrap_test.go. The
// XML keeps a single launcher (TestGenerateXML_SingleBootstrapFirstLogonCommand).

func TestGenerateXML_CustomConfig(t *testing.T) {
	cfg := Config{
		Username: "admin",
		Password: "s3cret",
		Locale:   "de-DE",
		Hostname: "custom-host",
		TimeZone: "CET",
		VirtIODrivers: []VirtIODriver{
			{INFRelPath: `custom\driver\custom.inf`, Description: "custom driver"},
		},
	}
	out := string(GenerateXML(cfg))
	assert.Contains(t, out, "<Name>admin</Name>")
	assert.Contains(t, out, "<Value>s3cret</Value>")
	assert.Contains(t, out, "<UILanguage>de-DE</UILanguage>")
	assert.Contains(t, out, "<ComputerName>custom-host</ComputerName>")
	assert.Contains(t, out, "<TimeZone>CET</TimeZone>")
	assert.Contains(t, out, `custom\driver\custom.inf`)
	assert.Equal(t, 1, strings.Count(out, "pnputil /add-driver"))
}

func TestGenerateXML_ARM64Architecture(t *testing.T) {
	out := string(GenerateXML(DefaultConfig()))
	assert.Contains(t, out, `processorArchitecture="arm64"`)
}

func TestGenerateXML_DiskPartitioning(t *testing.T) {
	out := string(GenerateXML(DefaultConfig()))
	assert.Contains(t, out, "<Type>EFI</Type>")
	assert.Contains(t, out, "<Type>MSR</Type>")
	assert.Contains(t, out, "<Type>Primary</Type>")
	assert.Contains(t, out, "<Format>FAT32</Format>")
	assert.Contains(t, out, "<Format>NTFS</Format>")
}

func TestWriteImage_CreatesFATImage(t *testing.T) {
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "autounattend.img")
	xmlBytes := GenerateXML(DefaultConfig())

	err := WriteImage(xmlBytes, imgPath)
	require.NoError(t, err)

	info, err := os.Stat(imgPath)
	require.NoError(t, err)
	assert.Greater(t, info.Size(), int64(0))
}

func TestWriteImage_ContainsXML(t *testing.T) {
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "autounattend.img")
	xmlBytes := GenerateXML(DefaultConfig())

	err := WriteImage(xmlBytes, imgPath)
	require.NoError(t, err)

	data, err := isokit.ReadFileFromFAT(imgPath, "/autounattend.xml")
	require.NoError(t, err)
	// May carry trailing newline padding — see PadForFAT.
	assert.True(t, bytes.HasPrefix(data, xmlBytes))
}

func TestWriteImage_ContainsStartupNSH(t *testing.T) {
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "autounattend.img")
	xmlBytes := GenerateXML(DefaultConfig())

	err := WriteImage(xmlBytes, imgPath)
	require.NoError(t, err)

	data, err := isokit.ReadFileFromFAT(imgPath, "/startup.nsh")
	require.NoError(t, err)
	nsh := string(data)
	assert.Contains(t, nsh, "BOOTAA64.EFI", "startup.nsh must reference ARM64 EFI boot loader")
	assert.Contains(t, nsh, "FS0:", "startup.nsh must check FS0")
	assert.Contains(t, nsh, "FS4:", "startup.nsh must check up to FS4")
}

func TestWriteISO_CreatesValidISO(t *testing.T) {
	tmpDir := t.TempDir()
	isoPath := filepath.Join(tmpDir, "autounattend.iso")
	xmlBytes := GenerateXML(DefaultConfig())

	err := WriteISO(xmlBytes, isoPath)
	require.NoError(t, err)

	info, err := os.Stat(isoPath)
	require.NoError(t, err)
	assert.Greater(t, info.Size(), int64(0))

	f, err := os.Open(isoPath)
	require.NoError(t, err)
	defer f.Close()
	magic := make([]byte, 5)
	_, err = f.ReadAt(magic, 0x8001)
	require.NoError(t, err)
	assert.Equal(t, "CD001", string(magic))
}

func TestWriteISO_ContainsXML(t *testing.T) {
	tmpDir := t.TempDir()
	isoPath := filepath.Join(tmpDir, "autounattend.iso")
	xmlBytes := GenerateXML(DefaultConfig())

	err := WriteISO(xmlBytes, isoPath)
	require.NoError(t, err)

	data, err := isokit.ReadFileFromISO(isoPath, "/autounattend.xml")
	require.NoError(t, err)
	assert.Equal(t, xmlBytes, data)
}

func TestPadForFAT_KeepsPayloadsOutOfTheCorruptingWindow(t *testing.T) {
	// go-diskfs mis-records the size of files that end near a 2048-byte
	// cluster boundary. The window was first measured as the last 63 bytes
	// (size 6129, 15 short of the boundary), but a 14270-byte answer file —
	// 66 bytes short — corrupted too (run 20260729T174705). The measured
	// windows are unreliable, so PadForFAT now aligns every payload to a full
	// cluster, the one size class that has never mis-recorded.
	// Exercise PadForFAT directly: WriteImage now also validates
	// the answer file, which synthetic payloads would fail for unrelated
	// reasons.
	for _, size := range []int{1982, 6081, 6100, 6129, 6143, 14270, 14336} {
		payload := make([]byte, size)
		for i := range payload {
			payload[i] = 'x'
		}

		padded := winpe.PadForFAT(payload)
		imgPath := filepath.Join(t.TempDir(), "pad.img")
		require.NoError(t, isokit.CreateFATImage(imgPath, map[string][]byte{"/f.xml": padded}), "size %d", size)

		got, err := isokit.ReadFileFromFAT(imgPath, "/f.xml")
		require.NoError(t, err, "size %d", size)
		assert.True(t, bytes.HasPrefix(got, payload), "size %d: original content must survive", size)
	}
}

func TestWriteImage_RealConfigRoundTrips(t *testing.T) {
	// Both the bare default and the fully-loaded install config: the latter is
	// what the install test ships, and its size is what landed in the
	// go-diskfs corruption window on run 20260729T174705.
	full := DefaultConfig()
	full.SSHPubKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPlaceholderPlaceholderPlaceholderPlaceh test@devcell"
	full.EnableRDP = true
	full.VirtIODrivers = NetKVMDriverPaths()

	for name, cfg := range map[string]Config{
		"default": DefaultConfig(),
		"full":    full,
	} {
		xml := GenerateXML(cfg)
		imgPath := filepath.Join(t.TempDir(), "autounattend.img")
		require.NoError(t, WriteImage(xml, imgPath), "%s config", name)

		got, err := isokit.ReadFileFromFAT(imgPath, "/autounattend.xml")
		require.NoError(t, err, "%s config", name)
		assert.True(t, bytes.HasPrefix(got, xml), "%s config: generated XML must round-trip intact", name)
	}
}

// Skip*OOBE is required in THIS environment, contradicting Microsoft's general
// guidance ("Don't use the SkipMachineOOBE setting to automate OOBE. Instead,
// use the above unattend settings." —
// https://learn.microsoft.com/en-us/windows-hardware/customize/desktop/automate-oobe).
//
// Empirical basis, ARM64 Windows 11 under QEMU/TCG:
//   - test/results/20260729T145842 shipped Skip*OOBE and reached the desktop
//     with the local account created and auto-logged in (install-088.png).
//   - test/results/20260729T190505 dropped Skip*OOBE for the documented Hide*
//     screens only, installed fine, then died in OOBE with "Something went
//     wrong ... OOBEZDP" (install-079.png) and rebooted back to firmware.
//
// Hide* hides individual screens; it does not skip OOBE. Zero Day Patch is not
// one of the hideable screens, and it needs a network the guest does not have:
// command.go attaches virtio-net-pci and Windows 11 ARM64 ships no inbox
// virtio-net driver. BypassNRO (specialize) covers the online-account gate,
// which is a different gate.
//
// Revisit if a NIC driver is ever injected (see DriverPaths / NetKVM) — with
// working networking the documented Hide*-only path may become viable.
func TestGenerateXML_SkipsOOBEEntirely(t *testing.T) {
	out := string(GenerateXML(DefaultConfig()))
	assert.Contains(t, out, "<SkipMachineOOBE>true</SkipMachineOOBE>",
		"without this OOBE runs and stalls on the Zero Day Patch step (OOBEZDP)")
	assert.Contains(t, out, "<SkipUserOOBE>true</SkipUserOOBE>")
}

func TestGenerateXML_HidesEveryDocumentedOOBEScreen(t *testing.T) {
	// The documented set for a fully automated OOBE.
	out := string(GenerateXML(DefaultConfig()))
	for _, setting := range []string{
		"<HideEULAPage>true</HideEULAPage>",
		"<HideOEMRegistrationScreen>true</HideOEMRegistrationScreen>",
		"<HideOnlineAccountScreens>true</HideOnlineAccountScreens>",
		"<HideWirelessSetupInOOBE>true</HideWirelessSetupInOOBE>",
		"<HideLocalAccountScreen>true</HideLocalAccountScreen>",
	} {
		assert.Contains(t, out, setting)
	}
}

func TestGenerateXML_SetsOOBERegionDefaults(t *testing.T) {
	// Region defaults belong in oobeSystem via Microsoft-Windows-International-Core;
	// the WinPE component only covers Setup itself. Without it OOBE can still
	// stop on a region/keyboard page.
	out := string(GenerateXML(DefaultConfig()))
	oobeIdx := strings.Index(out, `pass="oobeSystem"`)
	require.Positive(t, oobeIdx)
	oobeSection := out[oobeIdx:]
	assert.Contains(t, oobeSection, `name="Microsoft-Windows-International-Core"`)
}

func TestGenerateXML_BypassesNetworkRequirement(t *testing.T) {
	// Microsoft removed the oobe\bypassnro script in 2025 builds; the
	// underlying registry value still short-circuits the "must be online +
	// Microsoft account" gate, so set it before OOBE runs (specialize pass).
	out := string(GenerateXML(DefaultConfig()))
	assert.Contains(t, out, "BypassNRO")
	specializeIdx := strings.Index(out, `pass="specialize"`)
	oobeIdx := strings.Index(out, `pass="oobeSystem"`)
	bypassIdx := strings.Index(out, "BypassNRO")
	require.Positive(t, specializeIdx)
	assert.Greater(t, bypassIdx, specializeIdx, "BypassNRO must be set in specialize")
	assert.Less(t, bypassIdx, oobeIdx, "BypassNRO must be set before oobeSystem")
}

func TestGenerateXML_SelectsImageToInstall(t *testing.T) {
	// install.wim on the Windows 11 ARM64 media carries three images (Home,
	// Home Single Language, Pro). Without an explicit choice Setup stops on
	// "Select the operating system you want to install" and the unattended
	// run stalls there.
	out := string(GenerateXML(DefaultConfig()))
	assert.Contains(t, out, "<InstallFrom>")
	assert.Contains(t, out, "<Key>/IMAGE/NAME</Key>")
	assert.Contains(t, out, "<Value>Windows 11 Pro</Value>")
}

func TestGenerateXML_ImageNameIsConfigurable(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ImageName = "Windows 11 Home"
	out := string(GenerateXML(cfg))
	assert.Contains(t, out, "<Value>Windows 11 Home</Value>")
	assert.NotContains(t, out, "<Value>Windows 11 Pro</Value>")
}

func TestGenerateXML_InstallWimPath(t *testing.T) {
	cfg := DefaultConfig()
	cfg.InstallWimPath = `X:\winkit-install.wim`
	out := string(GenerateXML(cfg))
	assert.Contains(t, out, `<Path>X:\winkit-install.wim</Path>`)
	assert.Contains(t, out, "<InstallFrom>")
}

func TestGenerateXML_NoInstallWimPath(t *testing.T) {
	out := string(GenerateXML(DefaultConfig()))
	assert.NotContains(t, out, "winkit-install.wim")
}

func TestWriteImage_PreservesLongFilename(t *testing.T) {
	// Windows Setup looks for a file literally named "autounattend.xml".
	// The FAT writer stores a Long File Name entry, so the name survives —
	// unlike CreateSimpleISO, which writes ISO 9660 Level 1 8.3 names
	// ("AUTOUNAT.XML") that Windows never matches. This is why the answer
	// file ships as a FAT image and not as an ISO.
	imgPath := filepath.Join(t.TempDir(), "autounattend.img")
	require.NoError(t, WriteImage([]byte("<unattend/>"), imgPath))

	raw, err := os.ReadFile(imgPath)
	require.NoError(t, err)
	// LFN entries hold the name UTF-16LE in non-contiguous fields, so match a
	// short run that fits inside one field.
	assert.True(t, bytes.Contains(raw, []byte{'a', 0, 'u', 0, 't', 0, 'o', 0}),
		"FAT image must carry a long-filename entry for autounattend.xml")

	got, err := isokit.ReadFileFromFAT(imgPath, "/autounattend.xml")
	require.NoError(t, err)
	assert.True(t, bytes.HasPrefix(got, []byte("<unattend/>")))
}

func TestWriteISO_TruncatesName(t *testing.T) {
	// Documents the limitation that rules ISO delivery out: the generated
	// image cannot present the name Windows searches for.
	isoPath := filepath.Join(t.TempDir(), "autounattend.iso")
	require.NoError(t, WriteISO([]byte("<unattend/>"), isoPath))

	// go-diskfs reads its own Rock Ridge extension, so its reader resolves the
	// long name; Windows does not. What Windows sees is the raw ISO 9660
	// directory record, which carries the truncated name.
	raw, err := os.ReadFile(isoPath)
	require.NoError(t, err)
	assert.True(t, bytes.Contains(raw, []byte("AUTOUNAT.XML")),
		"ISO 9660 record holds the 8.3 name Windows would look for and miss")
}

func TestGenerateXML_BypassesHardwareChecksInWinPE(t *testing.T) {
	// Setup evaluates the Windows 11 requirements during the windowsPE pass,
	// so the LabConfig bypass keys must exist in the WinPE registry before it
	// runs. Setting them later (specialize) is too late — Setup stops on
	// "This PC doesn't currently meet Windows 11 system requirements".
	out := string(GenerateXML(DefaultConfig()))
	winPEIdx := strings.Index(out, `pass="windowsPE"`)
	specializeIdx := strings.Index(out, `pass="specialize"`)
	require.Positive(t, winPEIdx)
	require.Positive(t, specializeIdx)
	winPE := out[winPEIdx:specializeIdx]

	for _, key := range []string{
		"BypassTPMCheck",
		"BypassSecureBootCheck",
		"BypassRAMCheck",
		"BypassStorageCheck",
		"BypassCPUCheck",
	} {
		assert.Contains(t, winPE, `HKLM\SYSTEM\Setup\LabConfig /v `+key,
			"%s must be set in the windowsPE pass", key)
	}
}

func TestGenerateXML_NoUnschemaedLabConfigElement(t *testing.T) {
	// <LabConfig> is a registry key, not part of the Microsoft-Windows-Shell-Setup
	// schema. Emitting it as an element risks the answer file being rejected.
	out := string(GenerateXML(DefaultConfig()))
	assert.NotContains(t, out, "<LabConfig>")
}

func TestSessionUsername_UsesHostUser(t *testing.T) {
	// winkit's HOST_USER model: the guest account matches the host's $USER,
	// the same way tart derives its session user. A hardcoded name would give
	// a Windows VM a different account from every other engine.
	t.Setenv("USER", "dmitry")
	assert.Equal(t, "dmitry", SessionUsername())
}

func TestSessionUsername_FallsBackWhenUnset(t *testing.T) {
	t.Setenv("USER", "")
	assert.Equal(t, "winkit", SessionUsername())
}

func TestDefaultConfig_UsesHostUser(t *testing.T) {
	t.Setenv("USER", "dmitry")
	cfg := DefaultConfig()
	assert.Equal(t, "dmitry", cfg.Username)

	out := string(GenerateXML(cfg))
	assert.Contains(t, out, "<Name>dmitry</Name>", "local account must be the host user")
	assert.Contains(t, out, "<Username>dmitry</Username>", "autologon must use the host user")
}
func TestGenerateXML_SpecializeRunSynchronousUsesDeploymentComponent(t *testing.T) {
	// RunSynchronous has exactly two documented parents: Microsoft-Windows-Setup
	// (windowsPE) and Microsoft-Windows-Deployment (specialize, auditUser).
	// Microsoft-Windows-Shell-Setup does not define it, so commands placed
	// there may be silently ignored.
	// https://learn.microsoft.com/en-us/windows-hardware/customize/desktop/unattend/microsoft-windows-deployment-runsynchronous
	out := string(GenerateXML(DefaultConfig()))

	specializeIdx := strings.Index(out, `pass="specialize"`)
	oobeIdx := strings.Index(out, `pass="oobeSystem"`)
	require.Positive(t, specializeIdx)
	require.Positive(t, oobeIdx)
	specialize := out[specializeIdx:oobeIdx]

	deployIdx := strings.Index(specialize, `name="Microsoft-Windows-Deployment"`)
	require.Positive(t, deployIdx, "specialize must declare the Deployment component")

	runSyncIdx := strings.Index(specialize, "<RunSynchronous>")
	require.Positive(t, runSyncIdx)
	assert.Greater(t, runSyncIdx, deployIdx,
		"RunSynchronous must sit inside Microsoft-Windows-Deployment, not Shell-Setup")

	shellIdx := strings.Index(specialize, `name="Microsoft-Windows-Shell-Setup"`)
	if shellIdx >= 0 && shellIdx < runSyncIdx {
		t.Errorf("RunSynchronous appears after Shell-Setup opens — wrong parent component")
	}
}

func TestNetKVMDriverPaths_LetterlessINFPath(t *testing.T) {
	// Drive letters are probed at runtime with `if exist`, so the config
	// carries only the INF path relative to whatever volume holds the drivers.
	// Hardcoded letters are what made run 20260729T172019 fail: a DriverPaths
	// entry whose path does not resolve aborts Setup (0x80070001 - 0x40030).
	paths := NetKVMDriverPaths()
	require.Len(t, paths, 1, "one driver, one entry — the letter fan is gone")
	assert.Equal(t, `NetKVM\w11\ARM64\netkvm.inf`, paths[0].INFRelPath)
	assert.NotContains(t, paths[0].INFRelPath, ":", "no drive letter — probed at runtime")
}

func TestDefaultConfig_RDPCredentials(t *testing.T) {
	t.Setenv("USER", "dmitry")
	cfg := DefaultConfig()
	assert.Equal(t, "dmitry", cfg.Username, "account is the host user")
	assert.Equal(t, "rdp", cfg.Password, "password used for RDP/autologon")
}

func TestGenerateXML_EnablesRDP(t *testing.T) {
	// cell rdp allocates, forwards, records and discovers the port already;
	// the only missing link is Windows accepting the connection. RDP is off
	// by default and firewalled, so the forward lands on a closed port.
	cfg := DefaultConfig()
	cfg.EnableRDP = true
	out := string(GenerateXML(cfg))

	assert.Contains(t, out, "Microsoft-Windows-TerminalServices-LocalSessionManager")
	assert.Contains(t, out, "<fDenyTSConnections>false</fDenyTSConnections>")
	// NLA off: fresh non-domain hosts commonly reject clients otherwise.
	assert.Contains(t, out, "<UserAuthentication>1</UserAuthentication>")
	assert.Contains(t, out, "advfirewall set allprofiles state off")
}

func TestGenerateXML_NoRDPWhenDisabled(t *testing.T) {
	out := string(GenerateXML(DefaultConfig()))
	assert.NotContains(t, out, "fDenyTSConnections")
	assert.NotContains(t, out, "advfirewall set allprofiles")
}

func TestGenerateXML_ExtendsOSPartition(t *testing.T) {
	// The partition is sized at install time; without this a later qcow2
	// resize leaves the extra space invisible to the guest.
	out := string(GenerateXML(DefaultConfig()))
	assert.Contains(t, out, "<ExtendOSPartition>")
	assert.Contains(t, out, "<Extend>true</Extend>")
}

func TestGenerateXML_NoWinPEDriverInjection(t *testing.T) {
	// A PnpCustomizationsWinPE DriverPaths entry whose path does not resolve
	// ABORTS Setup — proven by run 20260729T172019 (0x80070001 - 0x40030 at
	// "Searching for disks"), and the docs agree. With WinPE letters being
	// unpredictable, no letter-based path is safe there. NetKVM is not
	// boot-critical, so it is installed in specialize instead.
	cfg := DefaultConfig()
	cfg.VirtIODrivers = NetKVMDriverPaths()
	out := string(GenerateXML(cfg))

	// Assert on markup: the template comment explaining the component's
	// absence necessarily names it.
	assert.NotContains(t, out, `name="Microsoft-Windows-PnpCustomizationsWinPE"`)
	assert.NotContains(t, out, "<DriverPaths>")
}

func TestGenerateXML_StagesDriversInSpecialize(t *testing.T) {
	// Drivers are staged (pnputil /add-driver, no /install) in specialize so
	// PnP auto-installs them on the OOBE boot. /install during specialize
	// triggers PnP enumeration during Setup's own device-setup phase, causing
	// a reboot loop (run 20260831T202847). Staging only adds to the store.
	cfg := DefaultConfig()
	cfg.VirtIODrivers = NetKVMDriverPaths()
	out := string(GenerateXML(cfg))

	specialize := out[strings.Index(out, `pass="specialize"`):strings.Index(out, `pass="oobeSystem"`)]

	// Staged in specialize with pnputil /add-driver (no /install).
	assert.Contains(t, specialize, "pnputil /add-driver",
		"driver must be staged in specialize")
	assert.Contains(t, specialize, `\NetKVM\w11\ARM64\netkvm.inf`)
	assert.NotContains(t, specialize, "pnputil /add-driver"+` %d:\NetKVM\w11\ARM64\netkvm.inf /install`,
		"specialize must NOT use /install (causes reboot loop)")
	assert.Contains(t, specialize, "if exist",
		"driver stage is guarded by `if exist`, a no-op if the CD letter misses")

	// Driver staging must precede the bootstrap copy and gosshd.
	assert.Less(t, strings.Index(specialize, "pnputil"), strings.Index(specialize, "winkit-bootstrap.ps1"),
		"driver stage must precede the bootstrap copy")

	// No driver install commands in FirstLogonCommands: PnP handles it.
	flc := out[strings.Index(out, "<FirstLogonCommands>"):strings.Index(out, "</FirstLogonCommands>")]
	assert.NotContains(t, flc, "<CommandLine>cmd /c",
		"no pnputil commands in FirstLogonCommands; drivers are staged in specialize")
}

// TestValidateRejectsOverlongCommand proves the generator validator (which
// writeAnswerImage runs before writing the volume) fails a too-long command, so
// a regression is caught at build time, not after a multi-hour install.
func TestValidateRejectsOverlongCommand(t *testing.T) {
	long := strings.Repeat("x", 300)
	xmlDoc := `<?xml version="1.0"?><unattend xmlns="urn:schemas-microsoft-com:unattend">
  <settings pass="specialize">
    <component name="Microsoft-Windows-Deployment">
      <RunSynchronous>
        <RunSynchronousCommand><Order>1</Order><Path>` + long + `</Path></RunSynchronousCommand>
      </RunSynchronous>
    </component>
  </settings>
</unattend>`
	errs := Validate([]byte(xmlDoc))
	var found bool
	for _, e := range errs {
		if strings.Contains(e.Error(), "limit 259") {
			found = true
		}
	}
	if !found {
		t.Errorf("Validate must reject a 300-char <Path>; got errors: %v", errs)
	}

	// And a short command validates clean on that axis.
	short := strings.ReplaceAll(xmlDoc, long, `cmd /c "echo hi"`)
	for _, e := range Validate([]byte(short)) {
		if strings.Contains(e.Error(), "limit 259") {
			t.Errorf("short command wrongly flagged: %v", e)
		}
	}
}

// TestUnattendCommandsUnderPathLimit guards the 259-char cap Windows Setup
// enforces on every RunSynchronousCommand/Path and SynchronousCommand/CommandLine.
// Exceeding it makes Setup reject the whole answer file (0x80220005) and abort
// specialize — a silent "computer restarted unexpectedly" loop (run
// 20260831T205732). This test fails loudly if a command grows too long.
func TestUnattendCommandsUnderPathLimit(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EnableRDP = true
	cfg.VirtIODrivers = append(NetKVMDriverPaths(), VioserialDriverPaths()...)
	out := string(GenerateXML(cfg))

	const limit = 259
	for _, tag := range []string{"Path", "CommandLine"} {
		open, close := "<"+tag+">", "</"+tag+">"
		for rest := out; ; {
			i := strings.Index(rest, open)
			if i < 0 {
				break
			}
			j := strings.Index(rest[i:], close)
			if j < 0 {
				break
			}
			val := rest[i+len(open) : i+j]
			if len(val) > limit {
				t.Errorf("<%s> is %d chars (>%d), Setup will reject the answer file:\n%s",
					tag, len(val), limit, val)
			}
			rest = rest[i+j+len(close):]
		}
	}
}

func TestGenerateXML_SpecializeCopiesBootstrapToC(t *testing.T) {
	cfg := DefaultConfig()
	cfg.VirtIODrivers = NetKVMDriverPaths()
	out := string(GenerateXML(cfg))

	specialize := out[strings.Index(out, `pass="specialize"`):strings.Index(out, `pass="oobeSystem"`)]
	assert.Contains(t, specialize, `winkit-bootstrap.ps1`)
	assert.Contains(t, specialize, `copy /y`)
	assert.Contains(t, specialize, `C:\winkit-bootstrap.ps1`)
	assert.Contains(t, specialize, "if exist",
		"bootstrap copy is guarded by `if exist`, a no-op if the source is missing")
}

func TestGenerateXML_SMBIOSHostnameTask(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SMBIOSHostname = true
	out := string(GenerateXML(cfg))
	specialize := out[strings.Index(out, `pass="specialize"`):strings.Index(out, `pass="oobeSystem"`)]
	assert.Contains(t, specialize, "winkit-hostname",
		"SMBIOSHostname must register the hostname-sync scheduled task")
	assert.Contains(t, specialize, "Win32_BIOS",
		"hostname task must read SMBIOS via WMI")
	assert.Contains(t, specialize, "Rename-Computer",
		"hostname task must call Rename-Computer")

	cfg2 := DefaultConfig()
	cfg2.SMBIOSHostname = false
	out2 := string(GenerateXML(cfg2))
	assert.NotContains(t, out2, "winkit-hostname",
		"without SMBIOSHostname, no hostname task")
}

func TestGenerateXML_FirstLogonLaunchesBootstrapFromC(t *testing.T) {
	cfg := DefaultConfig()
	out := string(GenerateXML(cfg))

	oobe := out[strings.Index(out, `pass="oobeSystem"`):]
	// specialize copies the bootstrap to C:\, so first logon launches it from
	// the fixed path with a short command (a volume-scan fallback would blow the
	// 259-char command cap).
	assert.Contains(t, oobe, `-File C:\winkit-bootstrap.ps1`,
		"FirstLogonCommands launches the bootstrap from the fixed C:\\ path")
}

func TestGenerateXML_WinPERunsOnlyRegCommands(t *testing.T) {
	// Three separate multi-hour runs died on windowsPE content that fails
	// silently or fatally (misplaced elements, unresolved DriverPaths, wmic
	// which current WinPE no longer ships). Guard the whole class: windowsPE
	// RunSynchronous may only write registry keys — plus the one vetted
	// exception, the agent launcher, which probes with `if exist` and
	// force-exits 0 so it cannot abort Setup.
	cfg := DefaultConfig()
	cfg.VirtIODrivers = NetKVMDriverPaths()
	cfg.EnableRDP = true
	cfg.WinPEAgent = true
	out := string(GenerateXML(cfg))

	launcher := strings.ReplaceAll(winpe.AgentLauncherCommand(), "&", "&amp;")
	winPE := out[strings.Index(out, `pass="windowsPE"`):strings.Index(out, `pass="specialize"`)]
	for _, part := range strings.Split(winPE, "<Path>")[1:] {
		cmd := part[:strings.Index(part, "</Path>")]
		assert.Truef(t, strings.HasPrefix(cmd, "reg add ") || cmd == launcher,
			"windowsPE RunSynchronous must only contain `reg add` commands or the agent launcher, got: %s", cmd)
	}
	assert.NotContains(t, strings.ToLower(winPE), "wmic ",
		"wmic was removed from current WinPE; invoking it aborts Setup")
}

func TestGenerateBootstrapScript_DisablesPowerSaving(t *testing.T) {
	// A VM must never sleep, blank its display, or spin down disks: the
	// display blanking after ~8 idle minutes made every later screendump
	// read as an all-black frame, indistinguishable from a hung guest.
	ps1 := string(GenerateBootstrapScript(DefaultConfig()))

	for _, cmd := range []string{
		"powercfg /setactive",  // high performance scheme
		"monitor-timeout-ac 0", // never blank the display
		"monitor-timeout-dc 0",
		"standby-timeout-ac 0", // never sleep
		"standby-timeout-dc 0",
		"disk-timeout-ac 0", // never spin down disks
		"disk-timeout-dc 0",
		"hibernate-timeout-ac 0",
		"powercfg /hibernate off",
	} {
		assert.Contains(t, ps1, cmd, "missing power setting: %s", cmd)
	}
}

func TestGenerateXML_NoWinPEDriveInventory(t *testing.T) {
	// The windowsPE drive-inventory dump depended on wmic, which current
	// WinPE no longer ships — and a failing windowsPE command aborts Setup.
	// Drive-letter visibility comes from the first-logon diagnostics script
	// (Get-Volume) instead, which runs in the full OS.
	out := string(GenerateXML(DefaultConfig()))

	assert.NotContains(t, out, "winkit-drives.txt")
	// "wmic " with the trailing space: the WMIConfig xmlns is fine, invoking
	// the removed wmic.exe is not.
	assert.NotContains(t, strings.ToLower(out), "wmic ")
	assert.Contains(t, out, BootstrapScriptName,
		"the bootstrap (which runs the diagnostics) remains the visibility channel")
}

func TestGenerateGuestDiagnosticsScript_CollectsWhatWeCannotSeeFromTheHost(t *testing.T) {
	// Everything here is invisible from outside the guest: whether the NIC
	// driver bound, which letter each volume got, whether sshd/RDP/WSL are on.
	ps1 := string(GenerateGuestDiagnosticsScript())

	for _, probe := range []string{
		"Get-NetAdapter",     // did NetKVM bind?
		"Get-Volume",         // which letter did each device get?
		"fDenyTSConnections", // is RDP actually enabled?
		"OpenSSH",            // did the capability install?
		"sshd",               // is the service running?
		"Subsystem-Linux",    // is WSL enabled?
		"Get-NetIPAddress",   // did we get an IP?
	} {
		assert.Contains(t, ps1, probe, "diagnostics must probe %s", probe)
	}
	assert.Contains(t, ps1, "Start-Transcript", "must capture output, including failures")
}

func TestGenerateGuestDiagnosticsScript_NetworkDiagnosticsAreComprehensive(t *testing.T) {
	ps1 := string(GenerateGuestDiagnosticsScript())

	for _, probe := range []string{
		"Get-NetIPConfiguration", // full adapter+IP+DNS+gateway picture
		"Get-NetRoute",           // routing table — is there a default route?
		"Resolve-DnsName",        // does DNS resolution work?
		"10.0.2.2",               // QEMU user-mode host — proves NAT works
		"Test-NetConnection",     // TCP connectivity test
	} {
		assert.Contains(t, ps1, probe, "diagnostics must include network probe: %s", probe)
	}
}

func TestWriteImage_ShipsTheDiagnosticsScript(t *testing.T) {
	// The script rides on the same writable volume as the answer file, so the
	// guest can run it and the host can read the log back out.
	imgPath := filepath.Join(t.TempDir(), "autounattend.img")
	require.NoError(t, WriteImage(GenerateXML(DefaultConfig()), imgPath))

	got, err := isokit.ReadFileFromFAT(imgPath, "/"+GuestDiagnosticsScriptName)
	require.NoError(t, err)
	assert.Contains(t, string(got), "Get-NetAdapter")
}

func TestBootstrapRunsDiagnosticsLast(t *testing.T) {
	// The diagnostics report must record the outcome of every bootstrap step,
	// so its invocation sits after all of them in the generated script.
	ps1 := string(GenerateBootstrapScript(DefaultConfig()))

	diagIdx := strings.Index(ps1, GuestDiagnosticsScriptName)
	require.Positive(t, diagIdx)
	for _, step := range []string{"OpenSSH.Server", "Start-Service sshd", "powercfg /hibernate off"} {
		assert.Greater(t, diagIdx, strings.Index(ps1, step),
			"diagnostics must run after: %s", step)
	}
	// The log path is chosen by the script, which locates its own volume.
	assert.Contains(t, string(GenerateGuestDiagnosticsScript()), GuestDiagnosticsLogName)
}

func TestReadGuestDiagnostics_ReturnsTheLog(t *testing.T) {
	imgPath := filepath.Join(t.TempDir(), "a.img")
	require.NoError(t, isokit.CreateFATImage(imgPath, map[string][]byte{
		"/autounattend.xml":           []byte("<unattend/>"),
		"/" + GuestDiagnosticsLogName: []byte("NIC: Red Hat VirtIO Ethernet Adapter\n"),
	}))

	log, err := ReadGuestDiagnostics(imgPath)
	require.NoError(t, err)
	assert.Contains(t, log, "VirtIO Ethernet")
}

func TestReadGuestDiagnostics_MissingLogIsAnError(t *testing.T) {
	imgPath := filepath.Join(t.TempDir(), "a.img")
	require.NoError(t, isokit.CreateFATImage(imgPath, map[string][]byte{"/autounattend.xml": []byte("<unattend/>")}))

	_, err := ReadGuestDiagnostics(imgPath)
	assert.Error(t, err, "a missing log means the guest never ran the script — say so")
}

// RDP must authenticate during connection setup (NLA/CredSSP), not by
// pre-filling an interactive logon form. With UserAuthentication=0 the
// server hands FreeRDP's credentials to the Windows logon UI and waits for
// a human to press Enter — run 20260802T112212 spent an entire run
// screenshotting that prompt.
func TestBuildAnswerVolume_EmbedsEFIBootloader(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EFIBootLoader = peARM64BootloaderStub()

	imgPath := filepath.Join(t.TempDir(), "autounattend.img")
	require.NoError(t, BuildAnswerVolume(cfg, imgPath))

	got, err := isokit.ReadFileFromFAT(imgPath, "/EFI/BOOT/BOOTAA64.EFI")
	require.NoError(t, err, "BOOTAA64.EFI must be on the answer volume")
	assert.True(t, bytes.HasPrefix(got, []byte("MZ")), "must start with MZ PE header")
}

func TestBuildAnswerVolume_NoBootloaderWhenNotSet(t *testing.T) {
	cfg := DefaultConfig()

	imgPath := filepath.Join(t.TempDir(), "autounattend.img")
	require.NoError(t, BuildAnswerVolume(cfg, imgPath))

	_, err := isokit.ReadFileFromFAT(imgPath, "/EFI/BOOT/BOOTAA64.EFI")
	assert.Error(t, err, "BOOTAA64.EFI should not be on the volume when EFIBootLoader is empty")
}

// peARM64BootloaderStub returns a minimal PE binary with aarch64 machine type,
// used by tests that need a realistic bootloader on the answer volume.
func peARM64BootloaderStub() []byte {
	pe := make([]byte, 256)
	pe[0], pe[1] = 'M', 'Z'
	pe[0x3C] = 0x80
	pe[0x80], pe[0x81], pe[0x82], pe[0x83] = 'P', 'E', 0, 0
	pe[0x84] = 0x64 // 0xAA64 little-endian
	pe[0x85] = 0xAA
	return pe
}

func TestAutounattend_RDPUsesNetworkLevelAuthentication(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EnableRDP = true
	xml := string(GenerateXML(cfg))

	assert.Contains(t, xml, "<UserAuthentication>1</UserAuthentication>",
		"NLA on: credentials are validated before the session, so clients land on the desktop")
	assert.NotContains(t, xml, "<UserAuthentication>0</UserAuthentication>")
}
