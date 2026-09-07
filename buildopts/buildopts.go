package buildopts

import (
	"fmt"
	"sort"
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
}

type BuildOpts struct {
	From     string
	PE       bool
	WSL      *WSLConfig
	Features []string
	Hooks    []Hook
}

func (o *BuildOpts) Validate() error {
	if o.PE && o.WSL != nil {
		return fmt.Errorf("pe and wsl are mutually exclusive: WinPE cannot run WSL")
	}
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

type FromKind int

const (
	FromMCT   FromKind = iota
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
