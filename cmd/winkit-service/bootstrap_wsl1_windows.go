//go:build windows

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// runCmdOutput runs a command and returns its combined output as a string.
func runCmdOutput(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if text != "" {
			return text, fmt.Errorf("%s failed: %w\n%s", name, err, text)
		}
		return "", fmt.Errorf("%s failed: %w", name, err)
	}
	return text, nil
}

// ensureWritableVolume makes sure the E: (or configured) volume is available.
// It checks for a pre-formatted WSLROOT volume and reassigns its letter, or
// runs diskpart to create a new one.
func ensureWritableVolume(out io.Writer, cfg wsl1BootstrapConfig) error {
	drive := fmt.Sprintf("%c:\\", cfg.DriveLetter)
	if _, err := os.Stat(drive); err == nil {
		return nil
	}

	// Check for pre-formatted WSLROOT volume at a different letter.
	found, foundLetter := findVolumeByLabel("WSLROOT")
	if found {
		bootstrapLog(out, "wsl1-volume", fmt.Sprintf("pre-formatted WSLROOT found at %c: — reassigning to %c:", foundLetter, cfg.DriveLetter))
		dpScript := fmt.Sprintf("select volume %c\nassign letter=%c", foundLetter, cfg.DriveLetter)
		cmd := exec.Command("diskpart.exe")
		cmd.Stdin = strings.NewReader(dpScript)
		dpOut, err := cmd.CombinedOutput()
		if dpOut != nil {
			bootstrapLog(out, "wsl1-volume", strings.TrimSpace(string(dpOut)))
		}
		if err != nil {
			return fmt.Errorf("diskpart reassign: %w", err)
		}
		return nil
	}

	bootstrapLog(out, "wsl1-volume", "preparing the writable WSL1 disk")
	return runChecked(out, "diskpart.exe", "/s", cfg.DiskpartFile)
}

// findVolumeByLabel checks mounted drives for a volume with the given label.
func findVolumeByLabel(label string) (bool, byte) {
	for d := byte('C'); d <= byte('Z'); d++ {
		root := fmt.Sprintf("%c:\\", d)
		rootPtr, _ := windows.UTF16PtrFromString(root)
		var volumeName [256]uint16
		err := windows.GetVolumeInformation(
			rootPtr, &volumeName[0], uint32(len(volumeName)),
			nil, nil, nil, nil, 0,
		)
		if err != nil {
			continue
		}
		if windows.UTF16ToString(volumeName[:]) == label {
			return true, d
		}
	}
	return false, 0
}

// relocateWSLRuntime copies the WSL runtime from the ramdisk to NTFS and
// patches the service registry to point at the new location.
func relocateWSLRuntime(out io.Writer, src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	if err := copyDirContents(src, dst); err != nil {
		return fmt.Errorf("copying WSL runtime: %w", err)
	}

	type regEntry struct {
		key, valueName, data string
		expand               bool
	}
	entries := []regEntry{
		{`SYSTEM\CurrentControlSet\Services\WSLService`, "ImagePath",
			fmt.Sprintf(`"%s\wslservice.exe"`, dst), true},
		{`SOFTWARE\Classes\CLSID\{4EA0C6DD-E9FF-48E7-994E-13A31D10DC60}\InProcServer32`, "",
			fmt.Sprintf(`%s\wslserviceproxystub.dll`, dst), false},
		{`SOFTWARE\Classes\CLSID\{2B9C59C3-98F1-45C8-B87B-12AE3C7927E8}\LocalServer32`, "",
			fmt.Sprintf(`"%s\wslhost.exe"`, dst), false},
		{`SOFTWARE\Microsoft\Windows\CurrentVersion\Lxss\MSI`, "InstallLocation",
			dst + `\`, false},
	}
	for _, e := range entries {
		k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, e.key, registry.SET_VALUE)
		if err != nil {
			return fmt.Errorf("opening registry key %s: %w", e.key, err)
		}
		var setErr error
		if e.expand {
			setErr = k.SetExpandStringValue(e.valueName, e.data)
		} else {
			setErr = k.SetStringValue(e.valueName, e.data)
		}
		k.Close()
		if setErr != nil {
			return fmt.Errorf("writing registry %s\\%s: %w", e.key, e.valueName, setErr)
		}
	}

	var fileCount int
	var totalBytes int64
	filepath.Walk(dst, func(_ string, info os.FileInfo, _ error) error {
		if info != nil && !info.IsDir() {
			fileCount++
			totalBytes += info.Size()
		}
		return nil
	})
	bootstrapLog(out, "wsl1-relocate-ok", fmt.Sprintf("WSL_RELOCATED files=%d bytes=%d", fileCount, totalBytes))
	return nil
}

func copyDirContents(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}

// setupWSLUser creates the user profile directory, sets ACLs, loads the
// registry hive, and writes the Lxss and Environment keys.
func setupWSLUser(out io.Writer, cfg wsl1BootstrapConfig) error {
	sid, err := lookupSID(cfg.UserName)
	if err != nil {
		return err
	}

	profilePath := filepath.Join(cfg.ProfileRoot, cfg.UserName)
	homeDrive := filepath.VolumeName(profilePath)
	homePath := strings.TrimPrefix(profilePath, homeDrive)

	// Create profile directory from Default template if needed.
	if _, err := os.Stat(profilePath); os.IsNotExist(err) {
		if copyErr := copyDirContents(`X:\Users\Default`, profilePath); copyErr != nil {
			return fmt.Errorf("copying default profile: %w", copyErr)
		}
	}
	os.MkdirAll(cfg.DistroPath, 0o755)

	// Set ACLs via icacls.
	sidStr := sid.String()
	aclOps := []struct {
		path, perm string
	}{
		{profilePath, fmt.Sprintf("*%s:(OI)(CI)F", sidStr)},
		{cfg.WSLDest, fmt.Sprintf("*%s:(OI)(CI)RX", sidStr)},
		{cfg.DistroPath, fmt.Sprintf("*%s:(OI)(CI)F", sidStr)},
	}
	for _, op := range aclOps {
		if err := runChecked(out, "icacls.exe", op.path, "/grant", op.perm, "/T", "/C"); err != nil {
			return fmt.Errorf("icacls %s: %w", op.path, err)
		}
	}

	// Write ProfileList registry entries.
	profileKey := fmt.Sprintf(`SOFTWARE\Microsoft\Windows NT\CurrentVersion\ProfileList\%s`, sidStr)
	pk, _, err := registry.CreateKey(registry.LOCAL_MACHINE, profileKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("creating profile registry key: %w", err)
	}
	pk.SetExpandStringValue("ProfileImagePath", profilePath)
	pk.SetDWordValue("Flags", 0)
	pk.SetDWordValue("State", 0)
	pk.SetDWordValue("RefCount", 0)
	pk.Close()

	// Load user hive if not already loaded.
	userHiveKey := sidStr
	if !isHiveLoaded(userHiveKey) {
		ntuser := filepath.Join(profilePath, "NTUSER.DAT")
		if err := runChecked(out, "reg.exe", "load", `HKU\`+userHiveKey, ntuser); err != nil {
			return fmt.Errorf("loading user hive: %w", err)
		}
	}

	// Write Environment keys.
	envKey, _, err := registry.CreateKey(registry.USERS, userHiveKey+`\Environment`, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("creating Environment key: %w", err)
	}
	envKey.SetExpandStringValue("USERPROFILE", profilePath)
	envKey.SetStringValue("HOMEDRIVE", homeDrive)
	envKey.SetStringValue("HOMEPATH", homePath)
	envKey.SetExpandStringValue("TEMP", filepath.Join(profilePath, `AppData\Local\Temp`))
	envKey.SetExpandStringValue("TMP", filepath.Join(profilePath, `AppData\Local\Temp`))
	envKey.SetStringValue("WSL_UTF8", "1")
	envKey.Close()
	os.MkdirAll(filepath.Join(profilePath, `AppData\Local\Temp`), 0o755)

	// Write Lxss keys.
	lxssKey, _, err := registry.CreateKey(registry.USERS, userHiveKey+`\Software\Microsoft\Windows\CurrentVersion\Lxss`, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("creating Lxss key: %w", err)
	}
	lxssKey.SetDWordValue("DefaultVersion", 1)
	lxssKey.SetDWordValue("NewDistributionLxFs", 0)
	lxssKey.Close()

	bootstrapLog(out, "wsl1-profile-ok",
		fmt.Sprintf("PROFILE_READY SID=%s PATH=%s RUNTIME=%s DISTRO=%s",
			sidStr, profilePath, cfg.WSLDest, cfg.DistroPath))
	return nil
}

// lookupSID resolves a local account name to its SID.
func lookupSID(account string) (*windows.SID, error) {
	sid, _, _, err := windows.LookupSID("", account)
	if err != nil {
		return nil, fmt.Errorf("LookupAccountName(%s): %w", account, err)
	}
	return sid, nil
}

// flushUserHive forces the user's loaded registry hive to be written to
// its backing NTUSER.DAT file. This is critical before QEMU shutdown so
// the Lxss distro registration persists across boots.
func flushUserHive(out io.Writer, userName string) error {
	sid, err := lookupSID(userName)
	if err != nil {
		return err
	}
	k, err := registry.OpenKey(registry.USERS, sid.String(), registry.QUERY_VALUE)
	if err != nil {
		return fmt.Errorf("opening user hive root: %w", err)
	}
	defer k.Close()

	advapi32 := windows.NewLazySystemDLL("advapi32.dll")
	regFlushKey := advapi32.NewProc("RegFlushKey")
	r, _, callErr := regFlushKey.Call(uintptr(k))
	if r != 0 {
		return fmt.Errorf("RegFlushKey: %v", callErr)
	}
	bootstrapLog(out, "wsl1-hive-flush", fmt.Sprintf("flushed HKU\\%s", sid.String()))
	return nil
}

// isHiveLoaded checks if a user registry hive is already mounted.
func isHiveLoaded(sidStr string) bool {
	k, err := registry.OpenKey(registry.USERS, sidStr, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	k.Close()
	return true
}

// exportDNSServers reads DNS servers from the Windows network interfaces
// registry and writes a resolv.conf to E:\winkit\resolv.conf.
func exportDNSServers(out io.Writer, driveLetter byte) error {
	ifacesKey, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SYSTEM\CurrentControlSet\Services\Tcpip\Parameters\Interfaces`,
		registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return fmt.Errorf("opening interfaces key: %w", err)
	}
	defer ifacesKey.Close()

	names, err := ifacesKey.ReadSubKeyNames(-1)
	if err != nil {
		return fmt.Errorf("listing interfaces: %w", err)
	}

	seen := map[string]bool{}
	var servers []string
	for _, name := range names {
		sub, err := registry.OpenKey(ifacesKey, name, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		for _, valName := range []string{"NameServer", "DhcpNameServer"} {
			val, _, err := sub.GetStringValue(valName)
			if err != nil || val == "" {
				continue
			}
			for _, part := range splitDNS(val) {
				if part != "" && !seen[part] {
					seen[part] = true
					servers = append(servers, part)
				}
			}
		}
		sub.Close()
	}

	if len(servers) == 0 {
		bootstrapLog(out, "wsl1-dns-ok", "no DNS servers found on any interface; the distro will use public resolvers")
		return nil
	}

	winkitDir := fmt.Sprintf("%c:\\winkit", driveLetter)
	os.MkdirAll(winkitDir, 0o755)

	var lines []string
	for _, s := range servers {
		lines = append(lines, "nameserver "+s)
	}
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(winkitDir, "resolv.conf"), []byte(content), 0o644); err != nil {
		return err
	}
	bootstrapLog(out, "wsl1-dns-ok", fmt.Sprintf("DNS servers: %s", strings.Join(servers, ", ")))
	return nil
}

func splitDNS(s string) []string {
	var result []string
	for _, sep := range []string{",", " "} {
		if strings.Contains(s, sep) {
			for _, part := range strings.Split(s, sep) {
				part = strings.TrimSpace(part)
				if part != "" {
					result = append(result, part)
				}
			}
			return result
		}
	}
	s = strings.TrimSpace(s)
	if s != "" {
		return []string{s}
	}
	return nil
}

// importAndProbeDistro finds the distro.wsl file, imports it via wsl.exe,
// and runs diagnostic probes.
func importAndProbeDistro(out io.Writer, cfg wsl1BootstrapConfig) error {
	work := fmt.Sprintf("%c:\\winkit", cfg.DriveLetter)
	os.MkdirAll(work, 0o755)
	wslExe := filepath.Join(cfg.WSLDest, "wsl.exe")

	// Find distro.wsl on any mounted drive.
	distroFile := ""
	for d := byte('C'); d <= byte('Z'); d++ {
		candidate := fmt.Sprintf("%c:\\distro.wsl", d)
		if _, err := os.Stat(candidate); err == nil {
			distroFile = candidate
			break
		}
	}
	if distroFile == "" {
		return fmt.Errorf("distro.wsl was not found on any filesystem drive")
	}
	bootstrapLog(out, "wsl1-probe", fmt.Sprintf("found distro at %s", distroFile))

	// List existing distros.
	distros, _ := invokeWSL(cfg, wslExe, "-l", "-q")
	writeProbeFile(work, "distros.out", distros)

	// Import if not already present.
	if !containsDistro(distros, cfg.DistroName) {
		bootstrapLog(out, "wsl1-import", fmt.Sprintf("importing distro %s", cfg.DistroName))
		importOut, err := invokeWSL(cfg, wslExe, "--import", cfg.DistroName,
			cfg.DistroPath, distroFile, "--version", "1")
		writeProbeFile(work, "import.out", importOut)
		if err != nil {
			return fmt.Errorf("wsl --import: %w", err)
		}
	}

	rootfs := filepath.Join(cfg.DistroPath, "rootfs")
	if _, err := os.Stat(rootfs); err != nil {
		return fmt.Errorf("WSL1 rootfs was not created at %s", cfg.DistroPath)
	}

	// List installed distros with version info.
	listOut, _ := invokeWSL(cfg, wslExe, "-l", "-v")
	writeProbeFile(work, "list.out", listOut)

	// Probe: architecture.
	archOut, err := invokeWSL(cfg, wslExe, "-d", cfg.DistroName, "--exec", "/bin/uname", "-m")
	if err != nil {
		archOut, _ = invokeWSL(cfg, wslExe, "-d", cfg.DistroName, "--exec",
			"/nix/var/nix/profiles/default/bin/uname", "-m")
	}
	writeProbeFile(work, "arch.out", archOut)

	// Probe: OS release.
	osRelease, _ := invokeWSL(cfg, wslExe, "-d", cfg.DistroName, "--exec", "/bin/cat", "/etc/os-release")
	writeProbeFile(work, "os-release.out", osRelease)

	alpineRelease, _ := invokeWSL(cfg, wslExe, "-d", cfg.DistroName, "--exec", "/bin/cat", "/etc/alpine-release")
	writeProbeFile(work, "alpine-release.out", alpineRelease)

	// Probe: nix version (no-op for non-nix distros).
	nixVer, _ := invokeWSL(cfg, wslExe, "-d", cfg.DistroName, "--exec",
		"/nix/var/nix/profiles/default/bin/nix", "--version")
	writeProbeFile(work, "nix-version.out", nixVer)

	os.WriteFile(filepath.Join(work, "probe.ok"), []byte(wsl1ProbeOKMarker), 0o644)
	bootstrapLog(out, "wsl1-probe-ok", wsl1ProbeOKMarker)
	return nil
}

// activateWSL1 sets up the volatile state that doesn't survive reboot but is
// required for WSL to work: kernel drivers, WSL registry entries, WSLService,
// and the user's registry hive (which contains the Lxss distro registration).
func activateWSL1(out io.Writer, cfg wsl1BootstrapConfig) error {
	// The boot.wim SYSTEM hive has PagingFiles cleared so Session Manager
	// does not auto-create a pagefile. Create one on the persistent data
	// disk via wpeutil. The file may already exist from a previous boot
	// but that's fine: wpeutil configures the pagefile in memory.
	pagePath := fmt.Sprintf("%c:\\pagefile.sys", cfg.DriveLetter)
	bootstrapLog(out, "activate-pagefile", fmt.Sprintf("creating pagefile at %s via wpeutil", pagePath))
	if err := runChecked(out, "wpeutil", "CreatePageFile", "/path="+pagePath, "/size=4096"); err != nil {
		bootstrapLog(out, "activate-pagefile-warn", fmt.Sprintf("wpeutil CreatePageFile: %v (WSL may fail)", err))
	} else {
		bootstrapLog(out, "activate-pagefile-ok", fmt.Sprintf("4 GB pagefile active at %s", pagePath))
	}

	// Kernel services needed for WSL1.
	for _, svc := range []string{"bfs", "bindflt", "afunix", "wcifs", "P9Rdr"} {
		bootstrapLog(out, "activate-svc", fmt.Sprintf("ensuring service %s", svc))
		if err := ensureService(svc, 30*time.Second); err != nil {
			bootstrapLog(out, "activate-svc-warn", fmt.Sprintf("ensure-service %s skipped: %v", svc, err))
		}
	}

	// Point the WSL service registry entries at the persistent runtime
	// on E:\. The files were copied during bootstrap; here we only fix
	// the in-memory registry which is rebuilt from the ramdisk each boot.
	bootstrapLog(out, "activate-wsl-registry", "patching WSL service registry for persistent runtime")
	if err := patchWSLServiceRegistry(cfg.WSLDest); err != nil {
		return fmt.Errorf("patching WSL service registry: %w", err)
	}

	bootstrapLog(out, "activate-wslservice", "starting WSLService")
	if err := ensureService("WSLService", 30*time.Second); err != nil {
		return fmt.Errorf("ensuring WSLService: %w", err)
	}

	// Load the user's registry hive so WSL can find the Lxss distro
	// registration written during bootstrap.
	bootstrapLog(out, "activate-profile", "loading user registry hive")
	sid, err := lookupSID(cfg.UserName)
	if err != nil {
		return fmt.Errorf("looking up SID for %s: %w", cfg.UserName, err)
	}
	userHiveKey := sid.String()
	if !isHiveLoaded(userHiveKey) {
		profilePath := filepath.Join(cfg.ProfileRoot, cfg.UserName)
		ntuser := filepath.Join(profilePath, "NTUSER.DAT")
		if err := runChecked(out, "reg.exe", "load", `HKU\`+userHiveKey, ntuser); err != nil {
			return fmt.Errorf("loading user hive: %w", err)
		}
		bootstrapLog(out, "activate-profile-ok", fmt.Sprintf("user hive loaded from %s", ntuser))
	} else {
		bootstrapLog(out, "activate-profile-ok", "user hive already loaded")
	}

	return nil
}

// patchWSLServiceRegistry writes the WSL service registry entries without
// copying files (they're already on E:\ from the bootstrap).
func patchWSLServiceRegistry(wslDest string) error {
	type regEntry struct {
		key, valueName, data string
		expand               bool
	}
	entries := []regEntry{
		{`SYSTEM\CurrentControlSet\Services\WSLService`, "ImagePath",
			fmt.Sprintf(`"%s\wslservice.exe"`, wslDest), true},
		{`SOFTWARE\Classes\CLSID\{4EA0C6DD-E9FF-48E7-994E-13A31D10DC60}\InProcServer32`, "",
			fmt.Sprintf(`%s\wslserviceproxystub.dll`, wslDest), false},
		{`SOFTWARE\Classes\CLSID\{2B9C59C3-98F1-45C8-B87B-12AE3C7927E8}\LocalServer32`, "",
			fmt.Sprintf(`"%s\wslhost.exe"`, wslDest), false},
		{`SOFTWARE\Microsoft\Windows\CurrentVersion\Lxss\MSI`, "InstallLocation",
			wslDest + `\`, false},
	}
	for _, e := range entries {
		k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, e.key, registry.SET_VALUE)
		if err != nil {
			return fmt.Errorf("opening registry key %s: %w", e.key, err)
		}
		var setErr error
		if e.expand {
			setErr = k.SetExpandStringValue(e.valueName, e.data)
		} else {
			setErr = k.SetStringValue(e.valueName, e.data)
		}
		k.Close()
		if setErr != nil {
			return fmt.Errorf("writing registry %s\\%s: %w", e.key, e.valueName, setErr)
		}
	}
	return nil
}

// invokeWSL runs wsl.exe under the configured user via winkit-service run-user.
func invokeWSL(cfg wsl1BootstrapConfig, wslExe string, args ...string) (string, error) {
	cmdArgs := []string{wslExe}
	cmdArgs = append(cmdArgs, args...)
	exitCode, output, err := runAsUserCapture(cfg.UserName, cfg.Password, fmt.Sprintf("%c:\\", cfg.DriveLetter), cmdArgs)
	if err != nil {
		return output, err
	}
	if exitCode != 0 {
		return output, fmt.Errorf("wsl.exe %s exited with code %d: %s",
			strings.Join(args, " "), exitCode, output)
	}
	return output, nil
}

func containsDistro(listing, name string) bool {
	for _, line := range strings.Split(listing, "\n") {
		cleaned := strings.ReplaceAll(line, "\x00", "")
		cleaned = strings.TrimSpace(cleaned)
		if cleaned == name {
			return true
		}
	}
	return false
}

func writeProbeFile(dir, name, content string) {
	os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644)
}

// patchRamdiskBPB patches the NTFS BPB on the given drive letter so that
// lxcore/drvfs accepts the volume. The stock boot.sdi carries a tiny
// total_sectors value; after WIM overlay inflates the ramdisk, the BPB
// is stale and drvfs returns EOVERFLOW.
func patchRamdiskBPB(driveLetter byte) error {
	volumePath, err := windows.UTF16PtrFromString(fmt.Sprintf(`\\.\%c:`, driveLetter))
	if err != nil {
		return err
	}

	handle, err := windows.CreateFile(
		volumePath,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil,
		windows.OPEN_EXISTING,
		0, 0,
	)
	if err != nil {
		return fmt.Errorf("CreateFile: %w", err)
	}
	defer windows.CloseHandle(handle)

	var bytesReturned uint32

	// Lock the volume.
	const (
		fsctlLockVolume        = 0x00090018
		fsctlUnlockVolume      = 0x0009001C
		fsctlDismountVolume    = 0x00090020
		fsctlAllowExtendedDasd = 0x00090083
	)
	err = windows.DeviceIoControl(handle, fsctlLockVolume,
		nil, 0, nil, 0, &bytesReturned, nil)
	if err != nil {
		// Try dismount if lock fails.
		windows.DeviceIoControl(handle, fsctlDismountVolume,
			nil, 0, nil, 0, &bytesReturned, nil)
	}

	// Allow writes past declared volume size.
	windows.DeviceIoControl(handle, fsctlAllowExtendedDasd,
		nil, 0, nil, 0, &bytesReturned, nil)

	// Read first sector (BPB).
	sector := make([]byte, 512)
	var bytesRead uint32
	err = windows.ReadFile(handle, sector, &bytesRead, nil)
	if err != nil {
		return fmt.Errorf("ReadFile: %w", err)
	}
	if bytesRead < 512 {
		return fmt.Errorf("short read (%d bytes)", bytesRead)
	}

	bytesPerSector := uint16(sector[0x0B]) | uint16(sector[0x0C])<<8
	currentSectors := int64(sector[0x28]) | int64(sector[0x29])<<8 |
		int64(sector[0x2A])<<16 | int64(sector[0x2B])<<24 |
		int64(sector[0x2C])<<32 | int64(sector[0x2D])<<40 |
		int64(sector[0x2E])<<48 | int64(sector[0x2F])<<56

	// Get actual volume size via GetDiskFreeSpaceEx.
	driveRoot, _ := windows.UTF16PtrFromString(fmt.Sprintf("%c:\\", driveLetter))
	var totalBytes uint64
	if err := windows.GetDiskFreeSpaceEx(driveRoot, nil, (*uint64)(unsafe.Pointer(&totalBytes)), nil); err != nil {
		return fmt.Errorf("GetDiskFreeSpaceEx: %w", err)
	}

	newSectors := int64(totalBytes/uint64(bytesPerSector)) - 1
	if newSectors <= currentSectors {
		return nil // geometry already sufficient
	}

	// Patch total_sectors at offset 0x28 (8 bytes, little-endian).
	for i := 0; i < 8; i++ {
		sector[0x28+i] = byte(newSectors >> (i * 8))
	}

	// Seek back to sector 0 and write.
	if _, err := windows.Seek(handle, 0, 0); err != nil {
		return fmt.Errorf("Seek: %w", err)
	}
	var bytesWritten uint32
	if err := windows.WriteFile(handle, sector, &bytesWritten, nil); err != nil {
		return fmt.Errorf("WriteFile: %w", err)
	}

	// Unlock.
	windows.DeviceIoControl(handle, fsctlUnlockVolume,
		nil, 0, nil, 0, &bytesReturned, nil)

	return nil
}

// runAsUserCapture runs a command as the specified user and captures its output.
func runAsUserCapture(user, password, currentDir string, command []string) (uint32, string, error) {
	// Use winkit-service run-user via self-invocation to get the full
	// user context (logon, profile load, environment block).
	self, err := os.Executable()
	if err != nil {
		return 0, "", err
	}
	args := []string{"run-user", "--user", user, "--password", password}
	if currentDir != "" {
		args = append(args, "--current-dir", currentDir)
	}
	args = append(args, "--")
	args = append(args, command...)

	cmd := exec.Command(self, args...)
	out, err := cmd.CombinedOutput()
	text := string(out)
	if cmd.ProcessState != nil {
		return uint32(cmd.ProcessState.ExitCode()), text, nil
	}
	if err != nil {
		return 1, text, err
	}
	return 0, text, nil
}
