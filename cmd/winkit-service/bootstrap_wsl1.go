package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// wsl1BootstrapConfig holds the parameters for the bootstrap-wsl1 verb.
// It replaces the template variables that bootstrap-wsl1.ps1 received.
type wsl1BootstrapConfig struct {
	UserName     string
	Password     string
	DistroName   string
	RuntimeRoot  string // directory containing winkit-service.exe and helpers
	ServicePath  string // path to winkit-service.exe
	WSLSource    string // packaged WSL runtime (e.g. X:\Program Files\WSL)
	WSLDest      string // relocated WSL runtime (e.g. E:\Program Files\WSL)
	DistroPath   string // distro rootfs target (e.g. E:\wsl-winkit)
	ProfileRoot  string // user profile base (e.g. E:\Users)
	CatalogDir   string // WSL1 component catalogs directory
	DiskpartFile string // diskpart script for preparing the writable disk
	DriveLetter  byte   // writable volume letter (default 'E')
}

func defaultWSL1BootstrapConfig(runtimeRoot string) wsl1BootstrapConfig {
	return wsl1BootstrapConfig{
		UserName:     "winkit",
		Password:     "Winkit1234",
		DistroName:   "winkit",
		RuntimeRoot:  runtimeRoot,
		ServicePath:  filepath.Join(runtimeRoot, "winkit-service.exe"),
		WSLSource:    `X:\Program Files\WSL`,
		WSLDest:      `E:\Program Files\WSL`,
		DistroPath:   `E:\wsl-winkit`,
		ProfileRoot:  `E:\Users`,
		CatalogDir:   `X:\Windows\winkit\WSL1Catalogs`,
		DiskpartFile: filepath.Join(runtimeRoot, "prepare-wsl1-disk.txt"),
		DriveLetter:  'E',
	}
}

const (
	wsl1BootstrapOKMarker     = "WSL1_BOOTSTRAP_OK"
	wsl1BootstrapFailedMarker = "WSL1_BOOTSTRAP_FAILED"
	wsl1ProbeOKMarker         = "WSL1_PROBE_OK"
)

func bootstrapLog(out io.Writer, event, msg string) {
	b := jsonlLine("ts", time.Now().UTC().Format(time.RFC3339Nano),
		"dir", "out", "event", event, "msg", msg)
	out.Write(b)
}

// runBootstrapWSL1 is the Go replacement for bootstrap-wsl1.ps1.
// It runs the full WSL1 setup sequence: BPB patch, disk preparation,
// pagefile, runtime relocation, user profile, DNS export, distro
// import, and probe.
func runBootstrapWSL1(out io.Writer, cfg wsl1BootstrapConfig) error {
	logPath := filepath.Join(cfg.RuntimeRoot, "wsl1-bootstrap.log")
	okMarker := filepath.Join(cfg.RuntimeRoot, "wsl1-bootstrap.ok")
	failedMarker := filepath.Join(cfg.RuntimeRoot, "wsl1-bootstrap.failed")

	os.Remove(logPath)
	os.Remove(okMarker)
	os.Remove(failedMarker)

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("creating bootstrap log: %w", err)
	}
	defer logFile.Close()
	log := io.MultiWriter(out, logFile)

	if err := runBootstrapWSL1Steps(log, cfg); err != nil {
		failure := fmt.Sprintf("%s: %v", wsl1BootstrapFailedMarker, err)
		bootstrapLog(log, "wsl1-bootstrap-failed", failure)
		os.WriteFile(failedMarker, []byte(failure), 0o644)
		return err
	}

	os.WriteFile(okMarker, []byte(wsl1BootstrapOKMarker), 0o644)
	bootstrapLog(log, "wsl1-bootstrap-ok", wsl1BootstrapOKMarker)
	return nil
}

func runBootstrapWSL1Steps(out io.Writer, cfg wsl1BootstrapConfig) error {
	drive := fmt.Sprintf("%c:\\", cfg.DriveLetter)

	// Step 1: Patch the X: ramdisk BPB for WSL1 drvfs compatibility.
	bootstrapLog(out, "wsl1-bpb", "patching X: ramdisk BPB for WSL1 drvfs compatibility")
	if err := patchRamdiskBPB('X'); err != nil {
		bootstrapLog(out, "wsl1-bpb-warn", fmt.Sprintf("BPB patch failed (non-fatal): %v", err))
	} else {
		bootstrapLog(out, "wsl1-bpb-ok", "BPB patch applied")
	}

	// Step 2: Register WSL1 catalogs (non-fatal, may be handled by init).
	bootstrapLog(out, "wsl1-catalogs", "registering WSL1 component catalogs")
	count, err := addCatalogs(cfg.CatalogDir)
	if err != nil {
		bootstrapLog(out, "wsl1-catalogs-warn", fmt.Sprintf("add-catalogs skipped: %v", err))
	} else {
		bootstrapLog(out, "wsl1-catalogs-ok", fmt.Sprintf("registered %d catalogs", count))
	}

	// Step 3: Ensure kernel drivers are running.
	for _, svc := range []string{"bfs", "bindflt", "afunix", "wcifs", "P9Rdr"} {
		bootstrapLog(out, "wsl1-svc", fmt.Sprintf("ensuring service %s", svc))
		if err := ensureService(svc, 30*time.Second); err != nil {
			bootstrapLog(out, "wsl1-svc-warn", fmt.Sprintf("ensure-service %s skipped: %v", svc, err))
		}
	}

	// Step 4: Prepare the writable E: volume.
	if err := ensureWritableVolume(out, cfg); err != nil {
		return fmt.Errorf("preparing writable volume: %w", err)
	}
	if _, err := os.Stat(drive); err != nil {
		return fmt.Errorf("writable WSL1 volume %s is unavailable after disk preparation", drive)
	}

	// Step 5: Create pagefile.
	bootstrapLog(out, "wsl1-pagefile", "creating pagefile")
	if err := runChecked(out, "wpeutil.exe", "CreatePageFile",
		fmt.Sprintf("/path=%c:\\pagefile.sys", cfg.DriveLetter), "/size=4096"); err != nil {
		return fmt.Errorf("creating pagefile: %w", err)
	}

	// Step 6: Relocate WSL runtime from ramdisk to NTFS.
	bootstrapLog(out, "wsl1-relocate", "relocating WSL runtime to NTFS")
	if err := relocateWSLRuntime(out, cfg.WSLSource, cfg.WSLDest); err != nil {
		return fmt.Errorf("relocating WSL runtime: %w", err)
	}

	// Step 7: Ensure WSLService is running.
	bootstrapLog(out, "wsl1-wslservice", "ensuring WSLService is running")
	if err := ensureService("WSLService", 30*time.Second); err != nil {
		return fmt.Errorf("ensuring WSLService: %w", err)
	}

	// Step 8: Ensure local user exists.
	bootstrapLog(out, "wsl1-user", fmt.Sprintf("ensuring user %s exists", cfg.UserName))
	if err := ensureLocalUser(cfg.UserName, cfg.Password); err != nil {
		return fmt.Errorf("ensuring user %s: %w", cfg.UserName, err)
	}
	if err := addToAdministrators(cfg.UserName); err != nil {
		return fmt.Errorf("adding %s to Administrators: %w", cfg.UserName, err)
	}

	// Step 9: Set up user profile and WSL registry.
	bootstrapLog(out, "wsl1-profile", "preparing WSL user profile and registry")
	if err := setupWSLUser(out, cfg); err != nil {
		return fmt.Errorf("setting up WSL user: %w", err)
	}

	// Step 10: Export DNS servers for the distro.
	bootstrapLog(out, "wsl1-dns", "exporting Windows DNS servers for the distro")
	if err := exportDNSServers(out, cfg.DriveLetter); err != nil {
		bootstrapLog(out, "wsl1-dns-warn", fmt.Sprintf("DNS export failed (non-fatal): %v", err))
	}

	// Step 11: Import and probe the WSL1 distro.
	bootstrapLog(out, "wsl1-probe", "importing and probing WSL1 distro")
	if err := importAndProbeDistro(out, cfg); err != nil {
		return fmt.Errorf("importing/probing distro: %w", err)
	}

	probeOK := filepath.Join(fmt.Sprintf("%c:\\winkit", cfg.DriveLetter), "probe.ok")
	if _, err := os.Stat(probeOK); err != nil {
		return fmt.Errorf("WSL1 probe completed without its success marker")
	}

	// Flush the user's registry hive to disk so the Lxss distro
	// registration survives QEMU shutdown.
	bootstrapLog(out, "wsl1-hive-flush", "flushing user registry hive to disk")
	if err := flushUserHive(out, cfg.UserName); err != nil {
		bootstrapLog(out, "wsl1-hive-flush-warn", fmt.Sprintf("hive flush failed (non-fatal): %v", err))
	} else {
		bootstrapLog(out, "wsl1-hive-flush-ok", "user registry hive flushed")
	}

	bootstrapOK := filepath.Join(fmt.Sprintf("%c:\\winkit", cfg.DriveLetter), "bootstrap.ok")
	os.MkdirAll(filepath.Dir(bootstrapOK), 0o755)
	if err := writeAndSync(bootstrapOK, []byte(wsl1BootstrapOKMarker)); err != nil {
		return fmt.Errorf("writing bootstrap marker: %w", err)
	}
	bootstrapLog(out, "wsl1-bootstrap-marker", fmt.Sprintf("persistent bootstrap marker written to %s", bootstrapOK))

	return nil
}

// writeAndSync writes data to a file and calls Sync (FlushFileBuffers on
// Windows) to ensure the write reaches the physical disk before QEMU shutdown.
func writeAndSync(path string, data []byte) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func joinArgs(args []string) string {
	var s string
	for i, a := range args {
		if i > 0 {
			s += " "
		}
		s += a
	}
	return s
}

// runChecked runs a command, logs its output, and returns an error if it
// exits non-zero. This replaces the PS1 Invoke-NativeChecked pattern.
func runChecked(out io.Writer, name string, args ...string) error {
	bootstrapLog(out, "wsl1-exec", fmt.Sprintf("> %s %s", name, joinArgs(args)))
	output, err := runCmdOutput(name, args...)
	if output != "" {
		bootstrapLog(out, "wsl1-exec", output)
	}
	return err
}

