package config

import (
	"fmt"

	"github.com/devcell-sh/go-winkit/buildopts"
)

var phaseMap = map[string]buildopts.HookPhase{
	"specialize": buildopts.Specialize,
	"oobe":       buildopts.OOBE,
	"boot":       buildopts.Boot,
	"wsl":        buildopts.WSLPhase,
}

func (c *Config) ToBuildOpts() (*buildopts.BuildOpts, error) {
	opts := &buildopts.BuildOpts{
		From:     c.From,
		PE:       c.PE,
		Features: c.Features,
	}

	if c.WSL != nil {
		opts.WSL = &buildopts.WSLConfig{Image: c.WSL.Image}
	}

	for phaseName, cmds := range c.Commands.Phases {
		phase, ok := phaseMap[phaseName]
		if !ok {
			return nil, fmt.Errorf("unknown phase %q", phaseName)
		}
		for _, cmd := range cmds {
			h := buildopts.Hook{
				Phase: phase,
				Cmd:   cmd.Cmd,
			}
			if cmd.Timeout.Duration() > 0 {
				h.Timeout = cmd.Timeout.Duration()
			}
			if cmd.Retries > 0 {
				h.Retries = cmd.Retries
			}
			if len(cmd.ValidExitCodes) > 0 {
				h.ValidExitCodes = cmd.ValidExitCodes
			}
			h.Reboot = cmd.Reboot
			opts.Hooks = append(opts.Hooks, h)
		}
	}

	for _, f := range c.Files {
		found := false
		for i := range opts.Hooks {
			if opts.Hooks[i].Phase == buildopts.Boot {
				opts.Hooks[i].Files = append(opts.Hooks[i].Files, buildopts.FileInject{
					Src: f.Src, Dest: f.Dest,
				})
				found = true
				break
			}
		}
		if !found {
			opts.Hooks = append(opts.Hooks, buildopts.Hook{
				Phase: buildopts.Boot,
				Label: "file-inject",
				Files: []buildopts.FileInject{{Src: f.Src, Dest: f.Dest}},
			})
		}
	}

	opts.ApplyDefaults()
	opts.SortHooks()

	if err := opts.Validate(); err != nil {
		return nil, err
	}

	return opts, nil
}

