package buildopts

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

type HookPhase int

const (
	Specialize HookPhase = iota
	OOBE
	Boot
	WSLPhase
)

func (p HookPhase) String() string {
	switch p {
	case Specialize:
		return "specialize"
	case OOBE:
		return "oobe"
	case Boot:
		return "boot"
	case WSLPhase:
		return "wsl"
	default:
		return fmt.Sprintf("HookPhase(%d)", int(p))
	}
}

type FileInject struct {
	Src  string
	Dest string
}

type Hook struct {
	Phase          HookPhase
	Label          string
	Cmd            string
	Files          []FileInject
	Env            map[string]string
	ValidExitCodes []int
	Timeout        time.Duration
	Retries        int
	Reboot         bool
}

func (h *Hook) applyDefaults() {
	if h.Timeout == 0 {
		h.Timeout = 5 * time.Minute
	}
	if len(h.ValidExitCodes) == 0 {
		h.ValidExitCodes = []int{0}
	}
}

type WSLConfig struct {
	Image string
	// NixHome selects the home-manager configuration for image "nix":
	// a local directory, a remote flake ref, or empty for the embedded
	// default. Ignored for non-nix images.
	NixHome string
	// ServicesDir is a directory laid out like an s6 scan dir (each
	// subdirectory one service: run, optional finish, data files), baked
	// into /etc/s6/services of the distro rootfs. Path only — the CLI
	// loads it; buildopts does no file I/O.
	ServicesDir string
}

// Default host-side ports forwarded into the build VM: gosshd (the
// provisioning channel, guest :2222), the delivered Windows OpenSSH
// (guest :22), and RDP (guest :3389).
const (
	DefaultGosshPort   uint16 = 20022
	DefaultOpenSSHPort uint16 = 20122
	DefaultRDPPort     uint16 = 23389
)

// Ports are the host-side forward ports for the build VM. Zero values
// fall back to the defaults above.
type Ports struct {
	RDP     uint16
	Gossh   uint16
	OpenSSH uint16
	// Forward lists extra host→guest TCP forwards as "host:guest". They
	// are recorded in the image's artifact manifest so `winkit start`
	// applies them without needing the original winkit.yaml.
	Forward []string
}

// ParseForward splits a "host:guest" forward spec into its two ports.
func ParseForward(spec string) (host, guest uint16, err error) {
	h, g, ok := strings.Cut(strings.TrimSpace(spec), ":")
	if !ok {
		return 0, 0, fmt.Errorf("forward %q: want host:guest", spec)
	}
	parse := func(name, s string) (uint16, error) {
		n, convErr := strconv.Atoi(strings.TrimSpace(s))
		if convErr != nil || n < 1 || n > 65535 {
			return 0, fmt.Errorf("forward %q: %s port must be 1-65535", spec, name)
		}
		return uint16(n), nil
	}
	if host, err = parse("host", h); err != nil {
		return 0, 0, err
	}
	if guest, err = parse("guest", g); err != nil {
		return 0, 0, err
	}
	return host, guest, nil
}

func (p Ports) RDPOrDefault() uint16 {
	if p.RDP != 0 {
		return p.RDP
	}
	return DefaultRDPPort
}

func (p Ports) GosshOrDefault() uint16 {
	if p.Gossh != 0 {
		return p.Gossh
	}
	return DefaultGosshPort
}

func (p Ports) OpenSSHOrDefault() uint16 {
	if p.OpenSSH != 0 {
		return p.OpenSSH
	}
	return DefaultOpenSSHPort
}

// Packages lists packages to install via external package managers
// during the bootstrap phase of a PE build.
type Packages struct {
	Chocolatey []string
}

// NeedsNetFx returns true if any package manager requires .NET Framework.
func (p Packages) NeedsNetFx() bool {
	return len(p.Chocolatey) > 0
}

// NeedsWoW64 returns true if packages may contain x86/x64 binaries that
// need WoW64 emulation on ARM64 WinPE.
func (p Packages) NeedsWoW64() bool {
	return len(p.Chocolatey) > 0
}

type BuildOpts struct {
	From             string
	PE               bool
	DWM              bool
	WSL              *WSLConfig
	Packages         Packages
	Features         []string
	Hooks            []Hook
	Ports            Ports
	Hostname         string
	BootstrapOnBuild bool
	WallpaperName    string
	WallpaperData    []byte
}

func (o *BuildOpts) Validate() error {
	if o.PE {
		for _, h := range o.Hooks {
			if h.Phase != Boot {
				return fmt.Errorf("pe mode only supports boot phase hooks, got %s", h.Phase)
			}
		}
	}
	if o.WSL == nil {
		for _, h := range o.Hooks {
			if h.Phase == WSLPhase {
				return fmt.Errorf("wsl phase hooks require wsl to be enabled")
			}
		}
	}
	return nil
}

func (o *BuildOpts) ApplyDefaults() {
	if o.From == "" {
		o.From = "windows/11-pro-arm64"
	}
	for i := range o.Hooks {
		o.Hooks[i].applyDefaults()
	}
}

func (o *BuildOpts) SortHooks() {
	sort.SliceStable(o.Hooks, func(i, j int) bool {
		return o.Hooks[i].Phase < o.Hooks[j].Phase
	})
}

// Stage names the build variant an opts set produces. The string values
// double as output-image naming ("winkit-full.qcow2" etc.).
type Stage string

const (
	// StageFull is a full Windows install without WSL.
	StageFull Stage = "full"
	// StageFullWSL is a full Windows install with WSL enabled and a distro imported.
	StageFullWSL Stage = "full-wsl"
	// StagePE is a WinPE boot-volume build (~4GB FAT, ephemeral, no WSL).
	StagePE Stage = "pe"
	// StagePEWSL is a WinPE boot volume bundled with a WSL1 runtime and distro.
	StagePEWSL Stage = "pe-wsl"
)

// Stage resolves which build variant these opts select from the 2x2
// matrix of install-type (full/PE) x WSL (on/off).
func (o *BuildOpts) Stage() Stage {
	switch {
	case o.PE && o.WSL != nil:
		return StagePEWSL
	case o.PE:
		return StagePE
	case o.WSL != nil:
		return StageFullWSL
	default:
		return StageFull
	}
}

type FromKind int

const (
	FromMCT FromKind = iota
	FromISO
	FromWIM
)

func DetectFrom(from string) FromKind {
	lower := strings.ToLower(from)
	switch {
	case strings.HasSuffix(lower, ".wim"):
		return FromWIM
	case strings.HasSuffix(lower, ".iso"):
		return FromISO
	default:
		return FromMCT
	}
}
