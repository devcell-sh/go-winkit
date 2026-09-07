package build

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/devcell-sh/go-winkit/gosshd"
	"github.com/devcell-sh/go-winkit/media/isokit"
	"github.com/devcell-sh/go-winkit/s6"
	"github.com/devcell-sh/go-winkit/sftpshare"
	"github.com/devcell-sh/go-winkit/unattend"
	"github.com/devcell-sh/go-winkit/vm"
	"github.com/devcell-sh/go-winkit/vm/qemu"
	"github.com/devcell-sh/go-winkit/winpe"
	"github.com/devcell-sh/go-winkit/wsl"
)

// wsl stage tunables. The install uses the fastest available accelerator
// (HVF/KVM); under TCG it is slow, so the SSH wait is generous.
const (
	wslDiskSizeGB = 64
	wslMemoryGB   = 6
	wslCPUs       = 4
	// wslSSHPort (host) forwards to the gosshd provisioning server on
	// wslGosshdGuestPort. gosshd is brought up by a specialize onstart SYSTEM
	// task — independent of the fragile first-logon bootstrap — so the host has
	// a reachable, fully-elevated channel to provision over even if bootstrap
	// fails. wslOpenSSHHostPort forwards to the Windows OpenSSH the image
	// ships on :22 (installed by the bootstrap), for separate verification.
	wslSSHPort         = 20022
	wslGosshdGuestPort = 2222
	wslOpenSSHHostPort = 20122
	wslRDPPort         = 23389
	wslInstallWait     = 4 * time.Hour
	wslContinueWait    = 10 * time.Minute
	wslSSHPollEvery    = 30 * time.Second
	// wsl2FeatureEnableWait bounds how long feature-enable is retried. gosshd
	// answers during OOBE ("Getting things ready for you"), where
	// Enable-WindowsOptionalFeature fails until the servicing stack is free, so
	// the first attempts are expected to fail and are retried until the OS
	// settles past OOBE.
	wsl2FeatureEnableWait = 45 * time.Minute
	// wslBootVolumeCapacity is the virtual size of the FAT Setup boot qcow2.
	// It holds the retail Setup boot.wim (~700MB) plus the boot chain; the
	// qcow2 is sparse, so this is a ceiling, not an allocation.
	wslBootVolumeCapacity = 4 * 1024 * 1024 * 1024
)

// wslGosshdHostPort is the host-side port forwarded to the gosshd provisioning
// channel (guest wslGosshdGuestPort). It defaults to wslSSHPort but can be
// overridden with WINKIT_E2E_SSH_PORT when 20022 is already bound on the host
// (e.g. a leftover QEMU still holding it), which otherwise makes QEMU abort at
// launch with "Could not set up host forwarding rule".
func wslGosshdHostPort() uint16 {
	if v := os.Getenv("WINKIT_E2E_SSH_PORT"); v != "" {
		if p, err := strconv.ParseUint(v, 10, 16); err == nil && p > 0 {
			return uint16(p)
		}
	}
	return wslSSHPort
}

func vzVNCPort() uint16 {
	if v := os.Getenv("WINKIT_E2E_VNC_PORT"); v != "" {
		if p, err := strconv.ParseUint(v, 10, 16); err == nil && p > 0 {
			return uint16(p)
		}
	}
	return vzDefaultVNCPort()
}

// wsl2Features are the Windows optional features the wsl2 stage stages and
// verifies. WSL2 needs all three: VirtualMachinePlatform (the lightweight
// utility VM), the WSL subsystem, and the Hyper-V platform the hypervisor
// engages on a secure/EL3 boot. Shared by enable and verify so they can't drift.
var wsl2Features = []string{
	"VirtualMachinePlatform",
	"Microsoft-Windows-Subsystem-Linux",
	"Microsoft-Hyper-V-All",
}

// wslAnswerConfig builds the autounattend config for the wsl stage: a full
// installed OS reachable over SSH + RDP, with the network + serial virtio
// drivers registered in specialize. Feature-enable is deliberately NOT here —
// it is host-driven over SSH after first logon (CELL-495).
func wslAnswerConfig(pwshFiles map[string][]byte, opensshName string, opensshData []byte, gosshdName string, gosshdData []byte) unattend.Config {
	cfg := unattend.DefaultConfig()
	cfg.EnableRDP = true
	cfg.PwshFiles = pwshFiles
	cfg.OpenSSHPayload = opensshName
	cfg.OpenSSHPayloadData = opensshData
	cfg.OpenSSHPayloadSize = len(opensshData)
	// netkvm (network) and vioserial (structured logs) are installed by
	// pnputil in the specialize pass, sourced from the virtio CD attached
	// during install. Host shared folders use virtio-9p, which is handled by
	// the inbox p9rdr.sys Plan 9 redirector (no extra driver needed).
	cfg.VirtIODrivers = append(unattend.NetKVMDriverPaths(), unattend.VioserialDriverPaths()...)
	// WSL1 needs the Microsoft-Windows-Subsystem-Linux optional feature; the
	// specialize→OOBE reboot completes it before the bootstrap imports
	// distro.wsl. Without it wsl --import --version 1 exits -1 (run
	// 20260903T160300). Not gated by WINKIT_WSL2: that gates only the
	// hypervisor stack.
	cfg.EnableWSL1Feature = true
	// gosshd is the provisioning SSH: specialize copies it to C: and registers
	// an onstart SYSTEM task on wslGosshdGuestPort, so a reachable, elevated
	// channel exists before first logon regardless of whether the bootstrap
	// (which installs the Windows OpenSSH on :22) succeeds.
	cfg.GosshdBinaryName = gosshdName
	cfg.GosshdBinaryData = gosshdData
	cfg.GosshdListenAddr = fmt.Sprintf(":%d", wslGosshdGuestPort)
	// Keep the console visible for the whole install from specialize on, not
	// just after first logon (the bootstrap's powercfg step never ran when the
	// bootstrap died early, leaving screendumps black).
	cfg.KeepDisplayAwake = true
	cfg.SMBIOSHostname = true
	return cfg
}

// wslWinPEAgentConfig enables the WinPE control agent for the wsl install:
// it tees Setup's Panther logs (setupact/setuperr) to the structured port as
// JSON, giving build.jsonl coverage of the windowsPE phase — gosshd, the
// other producer, only starts in specialize. The vioserial driver must be
// drvloaded in windowsPE for the ports to exist (retail WinPE has none);
// missing drivers degrade to a blind-but-working agent, so a virtio ISO
// without ARM64 vioserial only costs the live tee, never the build.
func wslWinPEAgentConfig(cfg *unattend.Config, virtioISO string, logger interface{ Warn(string, ...any) }) {
	cfg.WinPEAgent = true
	drivers, err := winpe.LoadWinPEVioserialDrivers(virtioISO)
	if err != nil {
		logger.Warn("no ARM64 vioserial driver for WinPE — Panther logs will not stream to build.jsonl during Setup", "err", err)
		return
	}
	if cfg.AnswerDrivers == nil {
		cfg.AnswerDrivers = make(map[string][]byte, len(drivers))
	}
	for p, data := range drivers {
		cfg.AnswerDrivers[p] = data
	}
}

// resolveWSLBackend picks the VM backend for the Windows install. Windows
// guests cannot boot under Virtualization.framework — Apple's firmware
// provides no ACPI and no vTPM, and there is no API to add them (CELL-523,
// proven empirically 2026-09-02) — so the platform default of vz-on-darwin
// does NOT apply here: the install defaults to qemu everywhere, and an
// explicit vz request fails fast instead of hanging on a black screen.
func ResolveBackend() (vm.VMBackend, string, error) {
	name := os.Getenv("WINKIT_VM_BACKEND")
	switch name {
	case "":
		name = "qemu"
	case "vz":
		return nil, "", fmt.Errorf("the vz backend cannot boot Windows guests (Apple's firmware has no ACPI/vTPM; see CELL-523) — unset WINKIT_VM_BACKEND or set it to qemu")
	}
	registry := map[string]vm.VMBackend{
		"qemu": &qemu.Backend{},
	}
	if b, ok := vzRegistryBackend(); ok {
		registry["vz"] = b
	}
	return vm.ResolveBackend(name, registry)
}

// buildWSLImage performs a full unattended Windows-on-ARM install to dest,
// with the WSL1 feature enabled in specialize and the nix distro imported at first
// logon. With WINKIT_WSL2=true it also enables the WSL2/Hyper-V feature stack
// online over SSH; verifying the hypervisor actually
// engages requires booting it on a TCG secure/EL3 machine (see CELL-495),
// which this build does not do: it uses the fastest available accelerator
// (HVF on Mac, KVM on Linux) for the install.
func wslImage(ctx context.Context, dest, cacheDir, winISO, virtioISO, workDir string, logger *slog.Logger, noCache bool, accel, wslImageName, nixHome string, services []s6.Service, displayType string) error {
	// --accel flag wins; then WINKIT_E2E_ACCEL env; then the best available
	// accelerator for the host (HVF on Mac, KVM on Linux, TCG fallback).
	// The install does not need secure/EL3: features are staged with
	// -NoRestart and engage on the first secure boot of the produced disk.
	if accel == "" {
		accel = os.Getenv("WINKIT_E2E_ACCEL")
	}
	if accel == "" {
		accel = qemu.DefaultAccel()
	}

	backend, backendName, err := ResolveBackend()
	if err != nil {
		return err
	}

	// Continue mode: boot an already-installed disk instead of installing from
	// scratch, for iterating on post-boot config / manual tests. Env-driven so
	// `WINKIT_E2E_DISK=<base> task test:wsl` selects it without a new entry point.
	if base := os.Getenv("WINKIT_E2E_DISK"); base != "" {
		return continueWSLImage(ctx, base, dest, virtioISO, workDir, logger, accel, backendName, backend)
	}

	// --- Phase 0: offline host build (files only) ---
	pwshFiles, err := winpe.FetchPwshFiles(cacheDir, func(f string, a ...any) {
		logger.Info(fmt.Sprintf(f, a...))
	})
	if err != nil {
		return fmt.Errorf("fetching pwsh: %w", err)
	}

	logger.Info("fetching OpenSSH server payload")
	opensshZip, err := unattend.DownloadOpenSSH(ctx, cacheDir, noCache)
	if err != nil {
		return fmt.Errorf("fetching OpenSSH: %w", err)
	}
	opensshData, err := os.ReadFile(opensshZip)
	if err != nil {
		return fmt.Errorf("reading OpenSSH payload: %w", err)
	}

	// rclone + WinFsp for the guest's fixed-disk SFTP mount (CELL-536). They
	// ship on the answer volume so the guest needs no internet for them.
	logger.Info("fetching rclone payload")
	rcloneZip, err := unattend.DownloadRclone(ctx, cacheDir, noCache)
	if err != nil {
		return fmt.Errorf("fetching rclone: %w", err)
	}
	rcloneData, err := os.ReadFile(rcloneZip)
	if err != nil {
		return fmt.Errorf("reading rclone payload: %w", err)
	}
	logger.Info("fetching WinFsp payload")
	winfspMsi, err := unattend.DownloadWinFsp(ctx, cacheDir, noCache)
	if err != nil {
		return fmt.Errorf("fetching WinFsp: %w", err)
	}
	winfspData, err := os.ReadFile(winfspMsi)
	if err != nil {
		return fmt.Errorf("reading WinFsp payload: %w", err)
	}

	// WSL1 rootfs (distro.wsl): built via docker (wsl package), shipped on
	// the answer volume, imported by the first-logon bootstrap. WSL1
	// needs no nested virtualization, so this is part of every image; the
	// hypervisor feature stack is the WSL2-only extra behind WINKIT_WSL2.
	// A host without docker still produces a working image, just without
	// the preloaded distro (the bootstrap step logs "no distro.wsl" and skips).
	distro, err := wsl.DistroFor(wslImageName, unattend.SessionUsername(), unattend.DefaultConfig().DistroName, nixHome)
	if err != nil {
		return err
	}
	// Extra s6 services bake into the rootfs; PutService so a user service
	// named like a built-in (sshd) overrides it instead of erroring.
	for _, svc := range services {
		if err := distro.PutService(svc); err != nil {
			return fmt.Errorf("adding s6 service %s: %w", svc.Name, err)
		}
	}
	dockerMissing := false
	if distro.NeedsDocker() {
		_, derr := exec.LookPath("docker")
		dockerMissing = derr != nil
	}
	var wslData []byte
	if dockerMissing {
		logger.Warn("docker not on PATH; skipping the preloaded WSL1 distro")
	} else {
		tarball, nerr := distro.Materialize(ctx, cacheDir, noCache,
			func(f string, a ...any) { logger.Info(fmt.Sprintf(f, a...)) })
		if nerr != nil {
			return fmt.Errorf("materializing distro.wsl (%s): %w", distro.Image, nerr)
		} else if wslData, nerr = os.ReadFile(tarball); nerr != nil {
			return fmt.Errorf("reading distro.wsl: %w", nerr)
		}
	}

	// Cross-compile the gosshd provisioning server for the guest (windows/arm64).
	// It is a single static binary; specialize copies it to C: and runs it as an
	// onstart SYSTEM task (see wslAnswerConfig).
	logger.Info("cross-compiling gosshd provisioning server", "arch", "arm64")
	gosshdExe := filepath.Join(workDir, "gosshd.exe")
	if err := winpe.CrossCompileGosshd(gosshdExe, "arm64"); err != nil {
		return fmt.Errorf("cross-compiling gosshd: %w", err)
	}
	gosshdData, err := os.ReadFile(gosshdExe)
	if err != nil {
		return fmt.Errorf("reading gosshd binary: %w", err)
	}

	cfg := wslAnswerConfig(pwshFiles, filepath.Base(opensshZip), opensshData, "gosshd.exe", gosshdData)
	cfg.WSLPayloadData = wslData
	wslWinPEAgentConfig(&cfg, virtioISO, logger)
	if backendName == "vz" {
		cfg.GosshdVsockPort = vzGosshdVsockPort()
	}

	// Host directory sharing: serve the shared dir over loopback SFTP and
	// let the first-logon bootstrap mount it as a fixed disk via WinFsp +
	// rclone (see sftpshare; CELL-532/536). qemu-backend only: the guest
	// reaches the host loopback at 10.0.2.2 via slirp; the port is rendered
	// into the bootstrap's boot-time mount task, so start before the answer
	// volume is built.
	sharedDir, err := wslSharedDir()
	if err != nil {
		return err
	}
	var sftpSrv *sftpshare.Server
	if sharedDir != "" && backendName == "qemu" {
		var serr error
		sftpSrv, serr = startSFTPShare(sharedDir, logger)
		if serr != nil {
			return fmt.Errorf("starting SFTP share: %w", serr)
		}
		defer sftpSrv.Close()
		cfg.SFTPPort = sftpSrv.Port()
		cfg.SFTPUser = sftpSrv.User()
		cfg.SFTPPassword = sftpSrv.Password()
		cfg.RclonePayload = unattend.RclonePayloadName
		cfg.RclonePayloadData = rcloneData
		cfg.WinFspPayload = unattend.WinFspPayloadName
		cfg.WinFspPayloadData = winfspData
		logger.Info("sharing host directory over SFTP", "dir", sharedDir, "port", sftpSrv.Port())
	}

	answerImg := filepath.Join(workDir, "autounattend.img")
	logger.Info("building answer volume", "user", cfg.Username, "rdp", cfg.EnableRDP)
	if err := unattend.BuildAnswerVolume(cfg, answerImg); err != nil {
		return fmt.Errorf("building answer volume: %w", err)
	}

	logger.Info("creating target disk", "path", dest, "gb", wslDiskSizeGB)
	if err := backend.CreateDisk(dest, wslDiskSizeGB, vm.DiskFormatDefault); err != nil {
		return fmt.Errorf("creating target disk: %w", err)
	}

	// Booting the Windows ISO's El Torito image ASSERTs on Linux-hosted EDK2,
	// so — like the base/WIM-builder paths — we FAT-boot the retail Setup boot
	// chain from a qcow2 (bootindex=1). The ISO stays attached only as a file
	// source for install.wim.
	logger.Info("building Setup boot volume from Windows ISO")
	bootFiles, err := winpe.BuildSetupBootVolumeFiles(winISO, workDir)
	if err != nil {
		return fmt.Errorf("building Setup boot volume: %w", err)
	}
	var bootVolume string
	switch backendName {
	case "qemu":
		bootVolume = filepath.Join(workDir, "wsl-boot.qcow2")
		if err := qemu.CreateFATQcow2(bootVolume, bootFiles, wslBootVolumeCapacity); err != nil {
			return fmt.Errorf("writing Setup boot volume: %w", err)
		}
	default:
		bootVolume = filepath.Join(workDir, "wsl-boot.img")
		// Apple's vz EFI never boots a partitionless FAT superfloppy (vCPUs
		// stay parked, screen stays black), so wrap the boot files in
		// GPT + ESP. WINKIT_VZ_BOOT_GPT=0 falls back to the raw FAT image.
		create := isokit.CreateGPTFATImageSized
		if os.Getenv("WINKIT_VZ_BOOT_GPT") == "0" {
			create = isokit.CreateFATImageSized
		}
		if err := create(bootVolume, bootFiles, wslBootVolumeCapacity); err != nil {
			return fmt.Errorf("writing Setup boot volume: %w", err)
		}
	}

	// --- Phase 1-3: boot Setup, install, first-logon bootstrap ---
	// build.jsonl is a deliverable, not an intermediate: it lands next to the
	// produced disk (results root), matching the TestWimBuilder convention.
	buildJSONL := filepath.Join(filepath.Dir(dest), "build.jsonl")
	installOut := filepath.Join(workDir, "install")
	secure := strings.HasPrefix(accel, "tcg")
	logger.Info("starting Windows install VM",
		"accel", accel, "secure", secure, "backend", backendName)

	installCfg := vm.VMInstallConfig{
		WindowsISO:      winISO,
		VirtIOISO:       virtioISO,
		AnswerVolume:    answerImg,
		DiskPath:        dest,
		CPUs:            wslCPUs,
		MemoryGB:        wslMemoryGB,
		OutputDir:       installOut,
		SSHPort:         wslGosshdHostPort(),
		SSHGuestPort:    wslGosshdGuestPort,
		OpenSSHHostPort: wslOpenSSHHostPort,
		RDPPort:         wslRDPPort,
		Accel:           accel,
	}
	switch backendName {
	case "qemu":
		installCfg.BackendExtra = &qemu.InstallOptions{
			StructuredLogPath: buildJSONL,
			BootVolume:        bootVolume,
			Secure:            secure,
			DisplayType:       displayType,
		}
	case "vz":
		installCfg.BackendExtra = vzInstallExtra(vzVNCPort(), logger, bootVolume)
	}
	machine, err := backend.StartInstall(ctx, installCfg)
	if err != nil {
		return fmt.Errorf("starting install: %w", err)
	}
	defer machine.Stop()

	// Stream guest progress to the UI while Setup runs (build.jsonl needs
	// vioserial, up only from specialize; the plain progress log covers earlier).
	stopTail := make(chan struct{})
	go tailProgress(filepath.Join(machine.OutputDir(), "guest-progress.log"), logger, stopTail)
	qmpSock := qemu.QMPSocketFromVM(machine)

	// --- handoff: wait for the gosshd provisioning channel ---
	// The provisioning connection is gosshd (started by the specialize onstart
	// SYSTEM task), reached on the host port that forwards to wslGosshdGuestPort.
	// It authenticates with gosshd's own credentials and its shell runs as
	// SYSTEM, so feature-enable is fully elevated with no UAC filtering. The
	// Windows account creds (cfg.Username/Password) are for autologon and the
	// Windows OpenSSH the bootstrap installs on :22, verified separately below.
	provAddr := fmt.Sprintf("127.0.0.1:%d", wslGosshdHostPort())
	provUser, provPass := gosshd.DefaultUser, gosshd.DefaultPassword
	logger.Info("waiting for gosshd provisioning channel", "addr", provAddr, "deadline", wslInstallWait)
	if err := waitForWindowsSSH(ctx, provAddr, provUser, provPass, wslInstallWait, logger, machine.Done()); err != nil {
		close(stopTail)
		return fmt.Errorf("install did not reach the gosshd provisioning channel: %w", err)
	}
	close(stopTail)
	logger.Info("provisioning channel up (gosshd, SYSTEM)")

	// --- Phase 4: post-SSH, host-driven feature enable (over gosshd, as SYSTEM) ---
	// WSL2-only: the hypervisor feature stack (VMP/WSL/Hyper-V) needs nested
	// virtualization on the eventual runtime host. WSL1 (the default) runs on
	// plain virt, so the baseline skips it entirely.
	if wsl2StackEnabled() {
		if err := wsl2EnableFeatures(ctx, provAddr, provUser, provPass, logger); err != nil {
			return fmt.Errorf("enabling WSL2/Hyper-V features: %w", err)
		}
	} else {
		logger.Info("WINKIT_WSL2 not set — skipping the hypervisor feature stack (WSL1-only image)")
	}

	// --- verify: SSH + RDP (+ features when WSL2); the delivered Windows OpenSSH on :22 ---
	if err := wslVerify(ctx, provAddr, provUser, provPass, wslRDPPort, wsl2StackEnabled(), logger); err != nil {
		return fmt.Errorf("post-install verification: %w", err)
	}
	wslVerifyDeliveredSSH(ctx, cfg.Username, cfg.Password, logger)

	// --- verify: the WSL1 distro the bootstrap imported ---
	// Over the delivered OpenSSH (:22) with the user's own account: WSL
	// registrations are per-user, so gosshd's SYSTEM session cannot see them.
	if len(wslData) > 0 {
		if err := wslVerifyDistro(ctx, cfg.Username, cfg.Password, cfg.DistroName, distro.VerifyCommand, distro.VerifyContains, logger); err != nil {
			return fmt.Errorf("%s distro verification: %w", cfg.DistroName, err)
		}
	}

	// --- verify: the W: mount live, as the real user over OpenSSH ---
	// The bootstrap marker only proves the mount worked once at first logon;
	// this exercises it end to end now — including the editor-save path
	// (writing into an EXISTING file), which only a non-SYSTEM session can
	// meaningfully test (run 20260903T181714).
	if sftpSrv != nil {
		osshAddr := fmt.Sprintf("127.0.0.1:%d", wslOpenSSHHostPort)
		if err := wslVerifySFTP(ctx, osshAddr, cfg.Username, cfg.Password, sftpSrv, sharedDir, logger); err != nil {
			return fmt.Errorf("SFTP share verification (as %s over delivered OpenSSH): %w", cfg.Username, err)
		}
	}

	// --- interactive hold or tear down ---
	if os.Getenv("WINKIT_E2E_TEARDOWN") == "false" {
		logger.Info("WINKIT_E2E_TEARDOWN=false — VM stays running for manual testing")
		logger.Info("  gosshd:   127.0.0.1:" + fmt.Sprintf("%d", wslGosshdHostPort()))
		logger.Info("  RDP:      127.0.0.1:" + fmt.Sprintf("%d", wslRDPPort))
		logger.Info("  OpenSSH:  127.0.0.1:" + fmt.Sprintf("%d", wslOpenSSHHostPort))
		logger.Info("Ctrl-C to shut down")
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		select {
		case <-sigCh:
			logger.Info("interrupt received; shutting down")
		case <-ctx.Done():
			logger.Info("context done; shutting down")
		}
		signal.Stop(sigCh)
	}

	wslTeardownGosshd(ctx, provAddr, provUser, provPass, logger)

	logger.Info("shutting down guest cleanly (grace period for WinSxS flush)")
	_, _, _, _ = sshRun(ctx, provAddr, provUser, provPass, "shutdown /s /t 5")
	time.Sleep(15 * time.Second)
	if qmpSock != "" {
		_ = qemu.QMPQuit(qmpSock)
	}
	time.Sleep(5 * time.Second)

	// Persist vars.fd next to the disk so `winkit start` can find it.
	if !strings.HasPrefix(accel, "tcg") {
		srcVars := filepath.Join(installOut, "vars.fd")
		if _, err := os.Stat(srcVars); err == nil {
			dstVars := SiblingVarsPath(dest)
			if copyErr := copyFile(srcVars, dstVars); copyErr == nil {
				logger.Info("saved NVRAM vars for reboot", "path", dstVars)
			}
		}
	}

	return nil
}

// wslVerifyDeliveredSSH checks the Windows OpenSSH the image ships on :22 is
// reachable with the Windows account credentials, over the dedicated forward.
// It is best-effort/non-fatal: gosshd is the provisioning channel that gates
// the build, while the delivered OpenSSH only needs to be present for the
// image's own consumers. A failure is logged, not returned, so a transient
// first-logon OpenSSH hiccup does not fail an otherwise-good build.
func wslVerifyDeliveredSSH(ctx context.Context, user, pass string, logger interface{ Info(string, ...any) }) {
	addr := fmt.Sprintf("127.0.0.1:%d", wslOpenSSHHostPort)
	who, _, code, err := sshRun(ctx, addr, user, pass, "whoami")
	if err != nil || code != 0 {
		logger.Info("delivered Windows OpenSSH (:22) not verified (non-fatal)", "addr", addr, "err", fmt.Sprint(err), "code", code)
		return
	}
	logger.Info("delivered Windows OpenSSH (:22) verified", "whoami", strings.TrimSpace(string(who)))
}

// wslVerifyDistro confirms the bootstrap-imported WSL1 distro answers its
// recipe's verify command (nix --version for nix, os-release for alpine),
// then (advisory) probes the W: fixed-disk share through WSL1 drvfs: the
// stat/read ops MRxDAV cannot do, i.e. the whole point of CELL-532.
//
// Connects over the delivered Windows OpenSSH (:22) as the real user: WSL
// registrations are per-user, and the import may still be in flight when the
// provisioning channel comes up, hence the generous retry.
func wslVerifyDistro(ctx context.Context, user, pass, distro, verifyCmd, verifyContains string, logger interface{ Info(string, ...any) }) error {
	addr := fmt.Sprintf("127.0.0.1:%d", wslOpenSSHHostPort)
	deadline := time.Now().Add(10 * time.Minute)
	var lastOut, lastErr string
	for {
		out, errb, code, err := sshRun(ctx, addr, user, pass,
			// -lc: login shell sources /etc/profile (for nix: profile.d/
			// nix.sh puts nix on PATH; init's default PATH has none).
			fmt.Sprintf(`$env:WSL_UTF8='1'; wsl.exe -d %s -e /bin/sh -lc "%s"`, distro, verifyCmd))
		if err == nil && code == 0 && strings.Contains(decodeWSLOutput(out), verifyContains) {
			logger.Info(distro+" verified", "out", strings.TrimSpace(decodeWSLOutput(out)))
			break
		}
		lastOut, lastErr = strings.TrimSpace(decodeWSLOutput(out)), strings.TrimSpace(decodeWSLOutput(errb))
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s never became usable (last: out=%q err=%q)", distro, lastOut, lastErr)
		}
		time.Sleep(wslSSHPollEvery)
	}

	out, errb, code, err := sshRun(ctx, addr, user, pass,
		// -lc so mkdir/stat/head resolve via the profile PATH (see above).
		fmt.Sprintf(`$env:WSL_UTF8='1'; wsl.exe -d %s -u root -e /bin/sh -lc "mkdir -p /mnt/w && /bin/mount -t drvfs W: /mnt/w 2>/dev/null; stat /mnt/w/.winkit/sftp-mounted >/dev/null && head -1 /mnt/w/.winkit/sftp-mounted"`, distro))
	if err == nil && code == 0 && strings.Contains(decodeWSLOutput(out), "mounted by") {
		logger.Info("W: drvfs fixed-disk stat/read verified from WSL1", "marker", strings.TrimSpace(decodeWSLOutput(out)))
	} else {
		logger.Info("W: drvfs probe not conclusive (non-fatal)", "code", code,
			"out", strings.TrimSpace(decodeWSLOutput(out)), "stderr", strings.TrimSpace(decodeWSLOutput(errb)), "err", fmt.Sprint(err))
	}
	return nil
}

// decodeWSLOutput makes wsl.exe output greppable. wsl.exe emits UTF-16LE;
// the verify commands set WSL_UTF8=1, but output captured before that env
// var takes hold (or from older images) arrives NUL-interleaved. Stripping
// NULs recovers the ASCII text either way.
func decodeWSLOutput(b []byte) string {
	return strings.ReplaceAll(string(b), "\x00", "")
}

// wsl2EnableFeatures enables the virtualization feature stack online. It does
// NOT reboot: enabling Hyper-V sets hypervisorlaunchtype=Auto, and a hypervisor-
// enabled boot only succeeds on the secure/EL3 machine — rebooting here on the
// plain-virt install machine would hang Windows (Vogtinator/CELL-495). The
// features are staged with -NoRestart and take effect on the first secure boot.
func wsl2EnableFeatures(ctx context.Context, addr, user, pass string, logger interface{ Info(string, ...any) }) error {
	logger.Info("enabling VirtualMachinePlatform + WSL + Hyper-V (online, -NoRestart; retried until past OOBE)")
	// Runs under gosshd's powershell shell (see the -shell powershell task), so
	// this is sent verbatim, no cmd wrapping. The sentinel proves the whole
	// pipeline ran, not just that the shell started. The feature list is shared
	// with wslVerify so enable and verify can never drift.
	quoted := "'" + strings.Join(wsl2Features, "','") + "'"
	enable := fmt.Sprintf(`$ErrorActionPreference='Stop'; foreach($f in %s){ Enable-WindowsOptionalFeature -Online -FeatureName $f -All -NoRestart | Out-Null }; 'FEATURES-ENABLED'`, quoted)
	deadline := time.Now().Add(wsl2FeatureEnableWait)
	for attempt := 1; ; attempt++ {
		out, errb, code, err := sshRun(ctx, addr, user, pass, enable)
		if err == nil && code == 0 && strings.Contains(string(out), "FEATURES-ENABLED") {
			logger.Info("features staged; hypervisor engages only on a secure/EL3 boot of the produced disk")
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("Enable-WindowsOptionalFeature never succeeded within %s (last: exit=%d err=%v out=%q stderr=%q)",
				wsl2FeatureEnableWait, code, err, strings.TrimSpace(string(out)), strings.TrimSpace(string(errb)))
		}
		logger.Info("feature-enable not ready (likely still in OOBE), retrying",
			"attempt", attempt, "exit", code, "stderr", strings.TrimSpace(string(errb)))
		time.Sleep(wslSSHPollEvery)
	}
}

// wsl2StackEnabled reports whether the WSL2 hypervisor feature stack is
// requested (WINKIT_WSL2=true/1). Default false: WSL1 needs none of it.
func wsl2StackEnabled() bool {
	v := os.Getenv("WINKIT_WSL2")
	return v == "true" || v == "1"
}

// wslVerify confirms SSH + RDP are reachable, and — when checkFeatures —
// that the staged feature stack is enabled. The hypervisor-engaged check
// (HypervisorPresent=True) requires a secure/EL3 boot of the produced disk
// and is left to the finalize test (CELL-495).
func wslVerify(ctx context.Context, addr, user, pass string, rdpPort uint16, checkFeatures bool, logger interface{ Info(string, ...any) }) error {
	who, _, code, err := sshRun(ctx, addr, user, pass, "whoami")
	if err != nil || code != 0 {
		return fmt.Errorf("whoami over SSH failed: code=%d err=%v", code, err)
	}
	logger.Info("SSH verified", "whoami", strings.TrimSpace(string(who)))

	features := wsl2Features
	if !checkFeatures {
		features = nil
	}
	// Assert ALL three features the enable staged, not just Hyper-V: WSL2 needs
	// VirtualMachinePlatform + the WSL subsystem, and a run that enabled only
	// Hyper-V would still look "green" against a Hyper-V-only check.
	for _, feature := range features {
		feat, _, _, err := sshRun(ctx, addr, user, pass,
			fmt.Sprintf(`(Get-WindowsOptionalFeature -Online -FeatureName %s).State`, feature))
		if err != nil {
			return fmt.Errorf("querying %s state: %w", feature, err)
		}
		state := strings.TrimSpace(string(feat))
		logger.Info("feature state", "feature", feature, "state", state)
		// -NoRestart without a reboot leaves each feature Enabled or
		// EnablePending; either means the enable took (it activates on the
		// first secure boot).
		if !strings.EqualFold(state, "Enabled") && !strings.EqualFold(state, "EnablePending") {
			return fmt.Errorf("%s not enabled (got %q)", feature, state)
		}
	}

	rdpAddr := fmt.Sprintf("127.0.0.1:%d", rdpPort)
	c, err := net.DialTimeout("tcp", rdpAddr, 10*time.Second)
	if err != nil {
		return fmt.Errorf("RDP port %s not reachable: %w", rdpAddr, err)
	}
	c.Close()
	logger.Info("RDP port reachable", "addr", rdpAddr)
	return nil
}

// wslTeardownGosshd removes the gosshd onstart task + binary so the delivered
// image does not ship a fixed-credential SYSTEM SSH backdoor. It is skipped when
// WINKIT_E2E_TEARDOWN=false — continue/dev images keep gosshd as the reachable
// management channel. Done over gosshd itself (powershell shell); the running
// process dies with the clean shutdown that follows.
func wslTeardownGosshd(ctx context.Context, addr, user, pass string, logger interface{ Info(string, ...any) }) {
	if os.Getenv("WINKIT_E2E_TEARDOWN") == "false" {
		logger.Info("keeping gosshd on the image (WINKIT_E2E_TEARDOWN=false)")
		return
	}
	logger.Info("removing gosshd provisioning task before shutdown")
	_, _, _, _ = sshRun(ctx, addr, user, pass,
		`schtasks /delete /tn gosshd /f; Remove-Item C:\gosshd.exe,C:\gosshd.log -Force -ErrorAction SilentlyContinue`)
}

// continueWSLImage boots an already-installed base disk (WINKIT_E2E_DISK) via a
// COW overlay so the base stays pristine, verifies the feature stack, and — in
// interactive mode (the default) — keeps the VM running so a human can connect
// and do manual tests, then bakes the session's changes into a standalone
// wsl-baked.qcow2 on shutdown. This needs gosshd on the base (build it with
// WINKIT_E2E_TEARDOWN=false), since gosshd is the reachable channel.
//
// The default accelerator is TCG (secure/EL3): continue mode is the
// verification path where the hypervisor must actually engage.
func continueWSLImage(ctx context.Context, base, dest, virtioISO, workDir string, logger *slog.Logger, accel, backendName string, backend vm.VMBackend) error {
	if accel == "" {
		accel = os.Getenv("WINKIT_E2E_ACCEL")
	}
	if accel == "" {
		accel = "tcg,thread=multi"
	}
	// Forwards bind here so another host (this container, via
	// host.docker.internal) can reach the VM running on the Mac, not just the
	// Mac's own loopback. Override with WINKIT_E2E_SSH_HOST.
	bindHost := os.Getenv("WINKIT_E2E_SSH_HOST")
	if bindHost == "" {
		bindHost = "0.0.0.0"
	}
	interactive := os.Getenv("WINKIT_E2E_INTERACTIVE") != "0" // default on in continue mode

	installOut := filepath.Join(workDir, "install")
	if err := os.MkdirAll(installOut, 0o755); err != nil {
		return fmt.Errorf("mkdir install dir: %w", err)
	}

	// Start the SFTP share BEFORE the VM boots and keep it up until this
	// function returns (after the interactive wait): the guest's boot-time
	// rclone task mounts on startup and retries until the host answers, so
	// the server must span the whole VM lifetime — not just the verify
	// window. Run 20260903T174850 skipped the late start entirely when SSH
	// was unreachable, leaving the guest with nothing to mount.
	sharedDir, err := wslSharedDir()
	if err != nil {
		return err
	}
	if sharedDir == "" {
		sharedDir = filepath.Join(installOut, "shared")
		if err := os.MkdirAll(sharedDir, 0o755); err != nil {
			return fmt.Errorf("mkdir shared dir: %w", err)
		}
	}
	sftpSrv, serr := startSFTPShare(sharedDir, logger)
	if serr != nil {
		return fmt.Errorf("starting SFTP share: %w", serr)
	}
	defer sftpSrv.Close()
	logger.Info("sharing host directory over SFTP", "dir", sharedDir, "port", sftpSrv.Port())

	baseAbs, err := filepath.Abs(base)
	if err != nil {
		return fmt.Errorf("resolving base disk: %w", err)
	}
	if _, err := os.Stat(baseAbs); err != nil {
		return fmt.Errorf("base disk %s: %w", baseAbs, err)
	}
	logger.Info("continue mode: overlaying base disk", "base", baseAbs, "overlay", dest)
	if err := backend.CreateOverlay(dest, baseAbs); err != nil {
		return fmt.Errorf("creating overlay: %w", err)
	}

	// NVRAM: secure/-kernel mode has no pflash vars (firmware auto-discovers the
	// ESP bootloader). Non-secure pflash mode needs the vars.fd that carries the
	// "Windows Boot Manager" UEFI entry from install.
	secure := strings.HasPrefix(accel, "tcg")
	var varsDst string
	if !secure {
		varsSrc := os.Getenv("WINKIT_E2E_VARS")
		if varsSrc == "" {
			varsSrc = FindSiblingVars(baseAbs)
		}
		if varsSrc == "" {
			return fmt.Errorf("no NVRAM vars found: set WINKIT_E2E_VARS to the base's vars.fd (carries the Windows Boot Manager entry)")
		}
		varsDst = filepath.Join(installOut, "vars.fd")
		if err := copyFile(varsSrc, varsDst); err != nil {
			return fmt.Errorf("copying vars %s: %w", varsSrc, err)
		}
		logger.Info("continue mode: using NVRAM", "vars", varsSrc)
	} else {
		logger.Info("continue mode: secure/-kernel boot (no NVRAM; firmware discovers ESP)")
	}

	logger.Info("continue mode: launching VM",
		"backend", backendName,
		"os", runtime.GOOS,
		"accel", accel,
		"virtioISO", virtioISO,
		"serialLog", filepath.Join(installOut, "serial.log"))

	runCfg := vm.VMRunConfig{
		DiskPath:        dest,
		OutputDir:       installOut,
		VMName:          "winkit-install",
		VirtIOISO:       virtioISO,
		CPUs:            wslCPUs,
		MemoryGB:        wslMemoryGB,
		Accel:           accel,
		SSHPort:         wslGosshdHostPort(),
		SSHGuestPort:    wslGosshdGuestPort,
		OpenSSHHostPort: wslOpenSSHHostPort,
		RDPPort:         wslRDPPort,
		SSHHost:         bindHost,
		SharedDir:       os.Getenv("WINKIT_E2E_SHARED_DIR"),
		SMBIOSSerial:    unattend.DefaultConfig().Hostname,
	}
	switch backendName {
	case "qemu":
		runCfg.BackendExtra = &qemu.RunOptions{
			VarsPath: varsDst,
			Secure:   secure,
		}
	case "vz":
		runCfg.BackendExtra = vzRunExtra(logger)
	}
	machine, err := backend.StartRun(ctx, runCfg)
	if err != nil {
		return fmt.Errorf("starting continue VM: %w", err)
	}
	defer machine.Stop()

	// The orchestrator connects over the Mac's own loopback; the 0.0.0.0 bind is
	// only so the container can also reach it.
	provAddr := fmt.Sprintf("127.0.0.1:%d", wslGosshdHostPort())
	provUser, provPass := gosshd.DefaultUser, gosshd.DefaultPassword
	logger.Info("continue mode: waiting for gosshd provisioning channel", "addr", provAddr)

	// gosshd first (SYSTEM, present on TEARDOWN=false bases), then the
	// delivered Windows OpenSSH on :22 with the account creds — a properly
	// baked image had gosshd torn out at build time, and everything the
	// continue flow runs works as the admin user too.
	provUp, viaGosshd := false, false
	if err := waitForWindowsSSH(ctx, provAddr, provUser, provPass, 4*time.Minute, logger, machine.Done()); err == nil {
		provUp, viaGosshd = true, true
		logger.Info("provisioning channel up (gosshd, SYSTEM)")
	} else {
		dc := unattend.DefaultConfig()
		osshAddr := fmt.Sprintf("127.0.0.1:%d", wslOpenSSHHostPort)
		logger.Info("gosshd not reachable (torn out of baked images at build time); falling back to the delivered Windows OpenSSH", "addr", osshAddr, "user", dc.Username)
		if err2 := waitForWindowsSSH(ctx, osshAddr, dc.Username, dc.Password, wslContinueWait-4*time.Minute, logger, machine.Done()); err2 == nil {
			provUp = true
			provAddr, provUser, provPass = osshAddr, dc.Username, dc.Password
			logger.Info("continuing over the delivered Windows OpenSSH", "user", dc.Username)
		} else if !interactive {
			return fmt.Errorf("no SSH channel: gosshd (%v); delivered OpenSSH (%v)", err, err2)
		} else {
			logger.Warn("no SSH channel reachable (advisory in interactive mode; VM stays up for troubleshooting)", "errGosshd", err, "errOpenSSH", err2)
		}
	}
	gosshdUp := provUp

	// In interactive mode the whole point is to poke the guest by hand (enable
	// features, attach an ISO, reboot), so a failed verify must NOT tear the VM
	// down — log it and keep going. Only the non-interactive (CI) path treats a
	// verify failure as fatal.
	if gosshdUp {
		if err := wslVerify(ctx, provAddr, provUser, provPass, wslRDPPort, wsl2StackEnabled(), logger); err != nil {
			if !interactive {
				return fmt.Errorf("continue-mode verification: %w", err)
			}
			logger.Warn("verify failed (advisory in interactive mode; VM stays up)", "err", err)
		}
		if sftpErr := wslVerifySFTP(ctx, provAddr, provUser, provPass, sftpSrv, sharedDir, logger); sftpErr != nil {
			if !interactive {
				return fmt.Errorf("SFTP share verification: %w", sftpErr)
			}
			logger.Warn("sftp verify failed (advisory in interactive mode)", "err", sftpErr)
		}
	}

	if interactive {
		logger.Info("interactive: VM is up — connect from this container via host.docker.internal:")
		if viaGosshd {
			logger.Info("  gosshd (SYSTEM):       host.docker.internal:" + fmt.Sprint(wslGosshdHostPort()) + fmt.Sprintf("  (user %s / pass %s → guest :%d)", provUser, provPass, wslGosshdGuestPort))
		} else {
			logger.Info("  gosshd:                NOT reachable (torn out at build time; delivered OpenSSH below is the channel)")
		}
		logger.Info("  Windows OpenSSH:       host.docker.internal:" + fmt.Sprint(wslOpenSSHHostPort) + "  (guest :22)")
		logger.Info("  RDP:                   host.docker.internal:" + fmt.Sprint(wslRDPPort))
		if qmpSock := qemu.QMPSocketFromVM(machine); qmpSock != "" {
			logger.Info("  screenshots:           " + filepath.Join(installOut, "screenshots"))
			logger.Info("  QMP:                   " + qmpSock)
		} else if vncPort := vzVNCPortFromVM(machine); vncPort > 0 {
			logger.Info("  screenshots:           " + filepath.Join(installOut, "screenshots"))
			logger.Info("  VNC:                   " + fmt.Sprintf("host.docker.internal:%d", vncPort))
		} else {
			logger.Info("  screenshots:           not available; use RDP")
		}
		logger.Info("do manual tests, then Ctrl-C to shut down")
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		select {
		case <-sigCh:
			logger.Info("interrupt received; shutting down")
		case <-ctx.Done():
			logger.Info("context done; shutting down")
		}
	}

	// --- bake: teardown (unless kept) → clean shutdown → flatten overlay ---
	// ctx may be cancelled (Ctrl-C), so bake commands use a fresh context.
	bakeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if provUp {
		if viaGosshd {
			wslTeardownGosshd(bakeCtx, provAddr, provUser, provPass, logger)
		}
		logger.Info("baking: clean shutdown")
		_, _, _, _ = sshRun(bakeCtx, provAddr, provUser, provPass, "shutdown /s /t 5")
	} else {
		logger.Info("baking: no SSH channel; sending QMP quit (no clean guest shutdown)")
	}
	if qmpSock := qemu.QMPSocketFromVM(machine); qmpSock != "" {
		_ = qemu.QMPQuit(qmpSock)
	}
	time.Sleep(8 * time.Second)

	bakedExt := ".qcow2"
	if backend.DefaultDiskFormat() == vm.DiskFormatRaw {
		bakedExt = ".raw"
	}
	baked := filepath.Join(filepath.Dir(dest), "wsl-baked"+bakedExt)
	if err := backend.Flatten(dest, baked); err != nil {
		return fmt.Errorf("flattening overlay to baked image: %w", err)
	}
	logger.Info("baked standalone image written", "path", baked)
	return nil
}

// siblingVarsPath returns the canonical path for a vars.fd file next to a disk
// image: "<dir>/<stem>-vars.fd".
func SiblingVarsPath(diskPath string) string {
	dir := filepath.Dir(diskPath)
	stem := strings.TrimSuffix(filepath.Base(diskPath), filepath.Ext(diskPath))
	return filepath.Join(dir, stem+"-vars.fd")
}

// findSiblingVars looks for the NVRAM vars store next to a base disk: first
// "<base without ext>-vars.fd", then "vars.fd" in the same directory, then
// inside a .winkit/run/<stem>/ output directory (or the legacy
// .winkit-run/<stem>/ location). Returns "" if none exists.
func FindSiblingVars(baseDisk string) string {
	dir := filepath.Dir(baseDisk)
	stem := strings.TrimSuffix(filepath.Base(baseDisk), filepath.Ext(baseDisk))
	for _, cand := range []string{
		filepath.Join(dir, stem+"-vars.fd"),
		filepath.Join(dir, "vars.fd"),
		filepath.Join(dir, "wsl-installed-vars.fd"),
		filepath.Join(dir, ".winkit", "run", stem, "vars.fd"),
		filepath.Join(dir, ".winkit-run", stem, "vars.fd"),
	} {
		if _, err := os.Stat(cand); err == nil {
			return cand
		}
	}
	return ""
}

// waitForWindowsSSH polls until the installed OS answers `whoami` over SSH.
// If vmDone is non-nil, it short-circuits when the VM exits unexpectedly.
func waitForWindowsSSH(ctx context.Context, addr, user, pass string, timeout time.Duration, logger interface{ Info(string, ...any) }, vmDone <-chan struct{}) error {
	deadline := time.Now().Add(timeout)
	start := time.Now()
	attempt := 0
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if vmDone != nil {
			select {
			case <-vmDone:
				return fmt.Errorf("VM exited unexpectedly while waiting for SSH")
			default:
			}
		}
		out, _, code, err := sshRun(ctx, addr, user, pass, "whoami")
		if err == nil && code == 0 && len(strings.TrimSpace(string(out))) > 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s", timeout)
		}
		attempt++
		elapsed := time.Since(start).Truncate(time.Second)
		logger.Info("not up yet, waiting", "addr", addr, "elapsed", elapsed, "attempt", attempt)
		time.Sleep(wslSSHPollEvery)
	}
}

// sshRun opens a fresh SSH connection, runs one command (in the guest's
// PowerShell default shell), and returns stdout/stderr/exit.
func sshRun(ctx context.Context, addr, user, pass, cmd string) (stdout, stderr []byte, exit int, err error) {
	dctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	c, err := gosshd.DialWith(dctx, addr, user, pass)
	if err != nil {
		return nil, nil, -1, err
	}
	defer c.Close()
	return c.Run(ctx, cmd)
}

// tailProgress streams appended lines of the guest progress log to the logger
// until stop is closed.
func tailProgress(path string, logger interface{ Info(string, ...any) }, stop <-chan struct{}) {
	var off int64
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			f, err := os.Open(path)
			if err != nil {
				continue
			}
			if _, err := f.Seek(off, 0); err != nil {
				f.Close()
				continue
			}
			buf := make([]byte, 64*1024)
			n, _ := f.Read(buf)
			if n > 0 {
				off += int64(n)
				for _, line := range strings.Split(strings.TrimRight(string(buf[:n]), "\n"), "\n") {
					if s := strings.TrimSpace(line); s != "" {
						logger.Info("guest: " + s)
					}
				}
			}
			f.Close()
		}
	}
}
