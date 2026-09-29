package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
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

// runInit is the PID-1 supervisor for WinPE. It reads the init manifest,
// initializes the PE environment (wpeinit, drivers, firewall), creates
// the admin user, and spawns all children under that user's context.
// It stays foreground so WinPE doesn't reboot.
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

	initLog(out, "init-start", "winkit-service init starting")

	// Ensure X:\winkit and X:\winkit\pwsh are on PATH so children
	// (gosshd shell sessions, bootstrap scripts) can find pwsh.exe
	// and winkit-service.exe without full paths.
	initSetPath()

	// Phase 1: WinPE initialization (SYSTEM context).
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

	// Start EventLog so structured logging from pe-agent works.
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

	// Phase 2: create the admin user.
	initLog(out, "init-user", fmt.Sprintf("creating user %s", cfg.User))
	if err := ensureLocalUser(cfg.User, cfg.Password); err != nil {
		return fmt.Errorf("ensure-user %s: %w", cfg.User, err)
	}
	if err := addToAdministrators(cfg.User); err != nil {
		return fmt.Errorf("add-to-administrators %s: %w", cfg.User, err)
	}
	initLog(out, "init-user-ok", fmt.Sprintf("user %s ready", cfg.User))

	// Phase 3: start pe-agent (structured logging) under user context.
	self, _ := os.Executable()
	peAgentArgs := []string{"run", "--name", "pe-agent"}
	if cfg.LogSerial != "" {
		peAgentArgs = append(peAgentArgs, "--log-serial", cfg.LogSerial)
	}
	initLog(out, "init-pe-agent", "starting pe-agent")
	if err := spawnAsUser(cfg.User, cfg.Password, self, peAgentArgs); err != nil {
		initLog(out, "init-pe-agent-error", fmt.Sprintf("pe-agent failed to start: %v (continuing)", err))
	}

	// Phase 4: start gosshd under user context (the primary SSH server).
	gosshdArgs := []string{}
	if cfg.GosshdAddr != "" {
		gosshdArgs = append(gosshdArgs, "-addr", cfg.GosshdAddr)
	}
	initLog(out, "init-gosshd", fmt.Sprintf("starting gosshd as %s", cfg.User))
	if err := spawnAsUser(cfg.User, cfg.Password, cfg.Gosshd, gosshdArgs); err != nil {
		return fmt.Errorf("starting gosshd: %w", err)
	}

	// Phase 5: WSL1 bootstrap (if configured).
	if cfg.WSL1Bootstrap != "" {
		// Register WSL1 catalogs and start kernel drivers under SYSTEM
		// context: the catalog API and SCM require privileges the winkit
		// user doesn't have, even as a member of Administrators.
		if cfg.WSL1CatalogDir != "" {
			initLog(out, "init-wsl1-catalogs", "registering WSL1 catalogs (SYSTEM)")
			count, catErr := addCatalogs(cfg.WSL1CatalogDir)
			if catErr != nil {
				initLog(out, "init-wsl1-catalogs-error", fmt.Sprintf("add-catalogs: %v", catErr))
			} else {
				initLog(out, "init-wsl1-catalogs-ok", fmt.Sprintf("registered %d catalogs", count))
			}
		}
		for _, svc := range cfg.WSL1Services {
			initLog(out, "init-wsl1-svc", fmt.Sprintf("ensuring service %s", svc))
			if err := ensureService(svc, 30*time.Second); err != nil {
				initLog(out, "init-wsl1-svc-error", fmt.Sprintf("ensure-service %s: %v", svc, err))
			}
		}

		initLog(out, "init-wsl1", "starting WSL1 bootstrap (SYSTEM)")
		bpwsh := findPwsh()
		if bpwsh == "" {
			initLog(out, "init-wsl1-error", "pwsh not found, skipping WSL1 bootstrap")
		} else {
			// The bootstrap runs as SYSTEM: diskpart, wpeutil, HKLM
			// registry writes, and SCM service starts all need
			// privileges the winkit user may lack. WSL-specific
			// operations (import, probe) use run-user internally to
			// execute under the winkit user's context.
			bootstrapArgs := []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", cfg.WSL1Bootstrap}
			if err := runCmd(out, bpwsh, bootstrapArgs...); err != nil {
				initLog(out, "init-wsl1-error", fmt.Sprintf("WSL1 bootstrap failed: %v", err))
			} else {
				initLog(out, "init-wsl1-ok", "WSL1 bootstrap complete")
			}
		}
	}

	initLog(out, "init-ready", "all children started, supervising")

	// Stay alive: WinPE reboots when winpeshl's process exits.
	for {
		time.Sleep(30 * time.Second)
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
