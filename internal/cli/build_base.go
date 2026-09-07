package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/devcell-sh/go-winkit/buildopts"
	"github.com/devcell-sh/go-winkit/gosshd"
	"github.com/devcell-sh/go-winkit/hookexec"
	"github.com/devcell-sh/go-winkit/isokit"
	"github.com/devcell-sh/go-winkit/unattend"
	"github.com/devcell-sh/go-winkit/winpe"
	"github.com/devcell-sh/go-winkit/winpe/qemu"
)

const (
	baseDiskSizeGB     = 64
	baseMemoryGB       = 6
	baseCPUs           = 4
	baseSSHPort        = 20022
	baseGosshdPort     = 2222
	baseOpenSSHPort    = 20122
	baseRDPPort        = 23389
	baseInstallWait    = 4 * time.Hour
	baseSSHPollEvery   = 30 * time.Second
	baseBootVolumeSize = 4 * 1024 * 1024 * 1024
)

// buildBaseInstallImage performs a full unattended Windows install to dest,
// running user hooks from opts over SSH after the OS boots. This is the
// default (non-PE, non-WSL) build mode.
func buildBaseInstallImage(ctx context.Context, dest, cacheDir, winISO, virtioISO, workDir string, opts *buildopts.BuildOpts, ui *runUI, noCache bool, accel, displayType string) error {
	if accel == "" {
		accel = qemu.DefaultAccel()
	}
	logger := ui.Logger

	backend, backendName, err := resolveWSLBackend()
	if err != nil {
		return err
	}

	// --- Phase 0: fetch dependencies ---
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

	logger.Info("cross-compiling gosshd provisioning server", "arch", "arm64")
	gosshdExe := filepath.Join(workDir, "gosshd.exe")
	if err := winpe.CrossCompileGosshd(gosshdExe, "arm64"); err != nil {
		return fmt.Errorf("cross-compiling gosshd: %w", err)
	}
	gosshdData, err := os.ReadFile(gosshdExe)
	if err != nil {
		return fmt.Errorf("reading gosshd binary: %w", err)
	}

	// --- Build unattend config ---
	cfg := unattend.DefaultConfig()
	cfg.EnableRDP = true
	cfg.PwshFiles = pwshFiles
	cfg.OpenSSHPayload = filepath.Base(opensshZip)
	cfg.OpenSSHPayloadData = opensshData
	cfg.OpenSSHPayloadSize = len(opensshData)
	cfg.VirtIODrivers = append(unattend.NetKVMDriverPaths(), unattend.VioserialDriverPaths()...)
	cfg.GosshdBinaryName = "gosshd.exe"
	cfg.GosshdBinaryData = gosshdData
	cfg.GosshdListenAddr = fmt.Sprintf(":%d", baseGosshdPort)
	cfg.KeepDisplayAwake = true
	cfg.SMBIOSHostname = true

	// Apply features from BuildOpts.
	if len(opts.Features) > 0 {
		cfg.Features = opts.Features
	}

	wslWinPEAgentConfig(&cfg, virtioISO, logger)

	answerImg := filepath.Join(workDir, "autounattend.img")
	logger.Info("building answer volume", "user", cfg.Username, "rdp", cfg.EnableRDP)
	if err := unattend.BuildAnswerVolume(cfg, answerImg); err != nil {
		return fmt.Errorf("building answer volume: %w", err)
	}

	// --- Create target disk ---
	logger.Info("creating target disk", "path", dest, "gb", baseDiskSizeGB)
	if err := backend.CreateDisk(dest, baseDiskSizeGB, winpe.DiskFormatDefault); err != nil {
		return fmt.Errorf("creating target disk: %w", err)
	}

	// --- Build Setup boot volume ---
	logger.Info("building Setup boot volume from Windows ISO")
	bootFiles, err := winpe.BuildSetupBootVolumeFiles(winISO, workDir)
	if err != nil {
		return fmt.Errorf("building Setup boot volume: %w", err)
	}
	var bootVolume string
	switch backendName {
	case "qemu":
		bootVolume = filepath.Join(workDir, "base-boot.qcow2")
		if err := qemu.CreateFATQcow2(bootVolume, bootFiles, baseBootVolumeSize); err != nil {
			return fmt.Errorf("writing Setup boot volume: %w", err)
		}
	default:
		bootVolume = filepath.Join(workDir, "base-boot.img")
		create := isokit.CreateGPTFATImageSized
		if os.Getenv("WINKIT_VZ_BOOT_GPT") == "0" {
			create = isokit.CreateFATImageSized
		}
		if err := create(bootVolume, bootFiles, baseBootVolumeSize); err != nil {
			return fmt.Errorf("writing Setup boot volume: %w", err)
		}
	}

	// --- Boot VM ---
	buildJSONL := filepath.Join(filepath.Dir(dest), "build.jsonl")
	installOut := filepath.Join(workDir, "install")
	secure := strings.HasPrefix(accel, "tcg")
	logger.Info("starting Windows install VM",
		"accel", accel, "secure", secure, "backend", backendName)

	installCfg := winpe.VMInstallConfig{
		WindowsISO:      winISO,
		VirtIOISO:       virtioISO,
		AnswerVolume:    answerImg,
		DiskPath:        dest,
		CPUs:            baseCPUs,
		MemoryGB:        baseMemoryGB,
		OutputDir:       installOut,
		SSHPort:         baseSSHPort,
		SSHGuestPort:    baseGosshdPort,
		OpenSSHHostPort: baseOpenSSHPort,
		RDPPort:         baseRDPPort,
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
	vm, err := backend.StartInstall(ctx, installCfg)
	if err != nil {
		return fmt.Errorf("starting install: %w", err)
	}
	defer vm.Stop()

	stopTail := make(chan struct{})
	go tailProgress(filepath.Join(vm.OutputDir(), "guest-progress.log"), logger, stopTail)
	go tailProgress(buildJSONL, logger, stopTail)

	// --- Wait for SSH ---
	provAddr := fmt.Sprintf("127.0.0.1:%d", baseSSHPort)
	provUser, provPass := gosshd.DefaultUser, gosshd.DefaultPassword
	logger.Info("waiting for gosshd provisioning channel", "addr", provAddr, "deadline", baseInstallWait)
	if err := waitForWindowsSSH(ctx, provAddr, provUser, provPass, baseInstallWait, logger, vm.Done()); err != nil {
		close(stopTail)
		return fmt.Errorf("install did not reach the gosshd provisioning channel: %w", err)
	}
	close(stopTail)
	logger.Info("provisioning channel up (gosshd, SYSTEM)")

	// --- Run user hooks over SSH ---
	bootHooks := filterSSHHooks(opts.Hooks)
	if len(bootHooks) > 0 {
		logger.Info("running build hooks over SSH", "count", len(bootHooks))
		runner, err := gosshd.DialWith(ctx, provAddr, provUser, provPass)
		if err != nil {
			return fmt.Errorf("connecting to gosshd for hooks: %w", err)
		}
		defer runner.Close()

		_, err = hookexec.ExecuteSSH(ctx, bootHooks, runner, os.Stderr)
		if err != nil {
			return fmt.Errorf("hook execution failed: %w", err)
		}
		logger.Info("all hooks completed")
	}

	// --- Verify SSH + RDP ---
	who, _, code, err := sshRun(ctx, provAddr, provUser, provPass, "whoami")
	if err != nil || code != 0 {
		return fmt.Errorf("post-install whoami failed: code=%d err=%v", code, err)
	}
	logger.Info("SSH verified", "whoami", strings.TrimSpace(string(who)))

	// --- Teardown gosshd and clean shutdown ---
	wslTeardownGosshd(ctx, provAddr, provUser, provPass, logger)

	logger.Info("shutting down guest cleanly")
	_, _, _, _ = sshRun(ctx, provAddr, provUser, provPass, "shutdown /s /t 5")
	time.Sleep(15 * time.Second)
	if qmpSock := qemu.QMPSocketFromVM(vm); qmpSock != "" {
		_ = qemu.QMPQuit(qmpSock)
	}
	time.Sleep(5 * time.Second)

	// Persist vars.fd next to the disk so `winkit start` can find it.
	if !secure {
		srcVars := filepath.Join(installOut, "vars.fd")
		if _, err := os.Stat(srcVars); err == nil {
			dstVars := siblingVarsPath(dest)
			if copyErr := copyFile(srcVars, dstVars); copyErr == nil {
				logger.Info("saved NVRAM vars for reboot", "path", dstVars)
			}
		}
	}

	return nil
}

// filterSSHHooks returns hooks that execute over SSH (boot and wsl phases).
func filterSSHHooks(hooks []buildopts.Hook) []buildopts.Hook {
	var out []buildopts.Hook
	for _, h := range hooks {
		if h.Phase == buildopts.Boot || h.Phase == buildopts.WSLPhase {
			out = append(out, h)
		}
	}
	return out
}
