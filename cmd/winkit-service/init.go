package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// initConfig is the manifest read by the "init" verb. It describes the
// PE environment's boot sequence: drivers to load, gosshd to start,
// user to create, and optional WSL1 bootstrap.
type initConfig struct {
	Drivers        []string `json:"drivers"`
	Gosshd         string   `json:"gosshd"`
	GosshdAddr     string   `json:"gosshdAddr"`
	User           string   `json:"user"`
	Password       string   `json:"password"`
	LogSerial      string   `json:"logSerial"`
	LogFile        string   `json:"logFile"`
	WSL1Bootstrap  string   `json:"wsl1Bootstrap,omitempty"`
	WSL1CatalogDir string   `json:"wsl1CatalogDir,omitempty"`
	WSL1Services   []string `json:"wsl1Services,omitempty"`
	// WSL1S6 is the wsl.exe command line that runs s6-svscan inside the
	// distro. init spawns it under the winkit user once the bootstrap
	// succeeds and respawns it if it exits, so it is supervised the same
	// way gosshd is: no SCM service, no service logon right.
	WSL1S6 []string `json:"wsl1S6,omitempty"`
	// DesktopShell, when set, is the path to the desktop shell binary
	// (implorer.exe). Launched as the admin user after DWM and WinSta0
	// are ready. Not set by default: populate init.json to enable.
	DesktopShell     string   `json:"desktopShell,omitempty"`
	DesktopShellArgs []string `json:"desktopShellArgs,omitempty"`
}

func parseInitConfig(data []byte) (*initConfig, error) {
	var cfg initConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing init config: %w", err)
	}
	if cfg.Gosshd == "" {
		return nil, fmt.Errorf("init config: gosshd is required")
	}
	if cfg.User == "" || cfg.Password == "" {
		return nil, fmt.Errorf("init config: user and password are required")
	}
	return &cfg, nil
}

// initPEEnvironment runs the PE environment setup shared by both init
// and bootstrap: wpeinit, driver loading, serial port, DWM, EventLog,
// firewall, and user creation with WinSta0 access.
func initPEEnvironment(out io.Writer, cfg *initConfig) error {
	initSetPath()

	initLog(out, "init-wpeinit", "running wpeinit")
	if err := runCmd(out, "wpeinit"); err != nil {
		initLog(out, "init-wpeinit-error", fmt.Sprintf("wpeinit failed: %v (continuing)", err))
	}

	for _, inf := range cfg.Drivers {
		initLog(out, "init-drvload", fmt.Sprintf("loading driver %s", inf))
		if err := runCmd(out, "drvload", inf); err != nil {
			initLog(out, "init-drvload-error", fmt.Sprintf("drvload %s failed: %v", inf, err))
		}
	}

	if cfg.LogSerial != "" {
		if mw, ok := out.(*multiWriter); ok {
			if f := openSerialPort(cfg.LogSerial); f != nil {
				mw.mu.Lock()
				mw.sinks = append(mw.sinks, f)
				mw.mu.Unlock()
				initLog(out, "init-serial", fmt.Sprintf("serial port %s now available", cfg.LogSerial))
			}
		}
	}

	if _, err := os.Stat(`X:\windows\system32\dwm.exe`); err == nil {
		initLog(out, "init-dwm", "starting Desktop Window Manager")
		if err := startDWM(out); err != nil {
			initLog(out, "init-dwm-error", fmt.Sprintf("DWM failed: %v (GUI compositing unavailable)", err))
		} else {
			initLog(out, "init-dwm-ok", "DWM compositor running")
		}
	}

	pwsh := findPwsh()
	if pwsh != "" {
		if err := runCmd(out, pwsh, "-NoProfile", "-Command",
			"Start-Service EventLog -ErrorAction SilentlyContinue"); err != nil {
			initLog(out, "init-eventlog-error", fmt.Sprintf("Start-Service EventLog: %v (continuing)", err))
		}
	}

	initLog(out, "init-firewall", "disabling firewall")
	if err := runCmd(out, "wpeutil", "DisableFirewall"); err != nil {
		initLog(out, "init-firewall-error", fmt.Sprintf("DisableFirewall failed: %v (continuing)", err))
	}

	initLog(out, "init-user", fmt.Sprintf("creating user %s", cfg.User))
	if err := ensureLocalUser(cfg.User, cfg.Password); err != nil {
		return fmt.Errorf("ensure-user %s: %w", cfg.User, err)
	}
	if err := addToAdministrators(cfg.User); err != nil {
		return fmt.Errorf("add-to-administrators %s: %w", cfg.User, err)
	}
	initLog(out, "init-user-ok", fmt.Sprintf("user %s ready", cfg.User))

	if err := grantWindowStationAccess(cfg.User); err != nil {
		initLog(out, "init-winsta-error", fmt.Sprintf("granting %s access to WinSta0: %v (GUI apps will not paint)", cfg.User, err))
	} else {
		initLog(out, "init-winsta-ok", fmt.Sprintf("%s granted access to WinSta0 and the Default desktop", cfg.User))
	}

	return nil
}

// runInit is the PID-1 activate supervisor for WinPE. It reads the init
// manifest, runs the PE environment setup, spawns services, and if the
// bootstrap phase hasn't run yet, runs it now. Stays foreground so WinPE
// doesn't reboot.
func runInit(configPath string) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("reading init config: %w", err)
	}
	cfg, err := parseInitConfig(data)
	if err != nil {
		return err
	}

	out := initOutput(cfg)

	initLog(out, "activate-start", "winkit-service activate starting")

	if err := initPEEnvironment(out, cfg); err != nil {
		return err
	}

	// Activate: start desktop shell (if configured) under user context.
	if cfg.DesktopShell != "" {
		initLog(out, "activate-desktop-shell", fmt.Sprintf("starting desktop shell %s", cfg.DesktopShell))
		if err := spawnAsUser(cfg.User, cfg.Password, cfg.DesktopShell, cfg.DesktopShellArgs); err != nil {
			initLog(out, "activate-desktop-shell-error", fmt.Sprintf("desktop shell failed to start: %v (continuing)", err))
		} else {
			initLog(out, "activate-desktop-shell-ok", "desktop shell started")
		}
	}

	// Activate: start pe-agent (structured logging) under user context.
	self, _ := os.Executable()
	peAgentArgs := []string{"run", "--name", "pe-agent"}
	if cfg.LogSerial != "" {
		peAgentArgs = append(peAgentArgs, "--log-serial", cfg.LogSerial)
	}
	initLog(out, "activate-pe-agent", "starting pe-agent")
	if err := spawnAsUser(cfg.User, cfg.Password, self, peAgentArgs); err != nil {
		initLog(out, "activate-pe-agent-error", fmt.Sprintf("pe-agent failed to start: %v (continuing)", err))
	}

	// Activate: start gosshd under user context (the primary SSH server).
	gosshdArgs := []string{}
	if cfg.GosshdAddr != "" {
		gosshdArgs = append(gosshdArgs, "-addr", cfg.GosshdAddr)
	}
	initLog(out, "activate-gosshd", fmt.Sprintf("starting gosshd as %s", cfg.User))
	if err := spawnAsUser(cfg.User, cfg.Password, cfg.Gosshd, gosshdArgs); err != nil {
		return fmt.Errorf("starting gosshd: %w", err)
	}

	// Bootstrap or activate: if the persistent marker on E:\ shows a
	// previous bootstrap completed, run the lightweight activate path
	// (mount volume, load drivers, load user hive). Otherwise run the
	// full bootstrap.
	if cfg.WSL1Bootstrap != "" {
		bsCfg := defaultWSL1BootstrapConfig(filepath.Dir(cfg.WSL1Bootstrap))
		bsCfg.UserName = cfg.User
		bsCfg.Password = cfg.Password
		if cfg.WSL1CatalogDir != "" {
			bsCfg.CatalogDir = cfg.WSL1CatalogDir
		}

		if err := ensureWritableVolume(out, bsCfg); err != nil {
			initLog(out, "activate-volume-error", fmt.Sprintf("writable volume: %v", err))
		}

		bootstrapMarker := fmt.Sprintf("%c:\\winkit\\bootstrap.ok", bsCfg.DriveLetter)
		if _, err := os.Stat(bootstrapMarker); err == nil {
			initLog(out, "activate-wsl1", "bootstrap already complete, running activate path")
			if err := activateWSL1(out, bsCfg); err != nil {
				initLog(out, "activate-wsl1-error", fmt.Sprintf("WSL1 activate failed: %v", err))
			} else {
				initLog(out, "activate-wsl1-ok", "WSL1 activate complete")
			}
		} else {
			initLog(out, "bootstrap-wsl1", "starting WSL1 bootstrap (native Go)")
			if err := runBootstrapWSL1(out, bsCfg); err != nil {
				initLog(out, "bootstrap-wsl1-error", fmt.Sprintf("WSL1 bootstrap failed: %v", err))
			} else {
				initLog(out, "bootstrap-wsl1-ok", "WSL1 bootstrap complete")
			}
		}
		if len(cfg.WSL1S6) > 0 {
			go superviseWSL1S6(out, cfg)
		}
	}

	initLog(out, "activate-ready", "all children started, supervising")

	// Stay alive: WinPE reboots when winpeshl's process exits.
	for {
		time.Sleep(30 * time.Second)
	}
}

// superviseWSL1S6 keeps s6-svscan running inside the WSL1 distro. The
// command is wrapped in `winkit-service run` so every line the services
// print reaches the serial port as a JSON event tagged winkit-s6, exactly
// as it would under the SCM, but the process runs as the winkit user via
// CreateProcessAsUser like gosshd does. wsl.exe must run as that user:
// the distro is registered in the user's Lxss hive.
func superviseWSL1S6(out io.Writer, cfg *initConfig) {
	self, _ := os.Executable()
	args := []string{"run", "--name", "winkit-s6"}
	if cfg.LogSerial != "" {
		args = append(args, "--log-serial", cfg.LogSerial)
	}
	args = append(args, "--log-file", `E:\winkit\winkit-s6.log`, "--")
	args = append(args, cfg.WSL1S6...)
	for attempt := 1; ; attempt++ {
		initLog(out, "init-s6-start", fmt.Sprintf("starting s6 in WSL1 as %s (attempt %d)", cfg.User, attempt))
		err := spawnAsUserAndWait(cfg.User, cfg.Password, self, args)
		if err != nil {
			initLog(out, "init-s6-exit", fmt.Sprintf("s6 wrapper exited: %v", err))
		} else {
			initLog(out, "init-s6-exit", "s6 wrapper exited")
		}
		time.Sleep(5 * time.Second)
	}
}

func initOutput(cfg *initConfig) io.Writer {
	mw := &multiWriter{}
	mw.sinks = append(mw.sinks, os.Stderr)
	if cfg.LogFile != "" {
		if f, err := os.OpenFile(cfg.LogFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			mw.sinks = append(mw.sinks, f)
		}
	}
	if cfg.LogSerial != "" {
		if f := openSerialPort(cfg.LogSerial); f != nil {
			mw.sinks = append(mw.sinks, f)
		}
	}
	return mw
}

func initLog(out io.Writer, event, msg string) {
	b := jsonlLine("ts", time.Now().UTC().Format(time.RFC3339Nano),
		"dir", "out", "event", event, "msg", msg)
	out.Write(b)
}

func runCmd(out io.Writer, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = out
	cmd.Stderr = out
	return cmd.Run()
}

func initSetPath() {
	additions := []string{`X:\winkit`, `X:\winkit\pwsh`}
	existing := os.Getenv("PATH")
	os.Setenv("PATH", fmt.Sprintf("%s;%s", strings.Join(additions, ";"), existing))
}

func findPwsh() string {
	candidates := []string{
		`X:\winkit\pwsh\pwsh.exe`,
		`C:\Program Files\PowerShell\7\pwsh.exe`,
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath("pwsh.exe"); err == nil {
		return p
	}
	return ""
}
