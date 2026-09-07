package hookexec

import (
	"context"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/devcell-sh/go-winkit/buildopts"
)

func hookTimeout(h buildopts.Hook) time.Duration {
	if h.Timeout > 0 {
		return h.Timeout
	}
	return 5 * time.Minute
}

func hookValidExitCodes(h buildopts.Hook) []int {
	if len(h.ValidExitCodes) > 0 {
		return h.ValidExitCodes
	}
	return []int{0}
}

type Runner interface {
	Run(ctx context.Context, cmd string) (stdout, stderr []byte, exitCode int, err error)
	Close() error
}

type SpecializeCommand struct {
	Order       int
	Path        string
	Description string
}

type OOBECommand struct {
	Order       int
	CommandLine string
	Description string
}

func InjectSpecialize(hooks []buildopts.Hook, startOrder int) []SpecializeCommand {
	var cmds []SpecializeCommand
	order := startOrder
	for _, h := range hooks {
		if h.Phase != buildopts.Specialize {
			continue
		}
		cmds = append(cmds, SpecializeCommand{
			Order:       order,
			Path:        h.Cmd,
			Description: label(h, "specialize hook"),
		})
		order++
	}
	return cmds
}

func InjectOOBE(hooks []buildopts.Hook, startOrder int) []OOBECommand {
	var cmds []OOBECommand
	order := startOrder
	for _, h := range hooks {
		if h.Phase != buildopts.OOBE {
			continue
		}
		cmds = append(cmds, OOBECommand{
			Order:       order,
			CommandLine: h.Cmd,
			Description: label(h, "oobe hook"),
		})
		order++
	}
	return cmds
}

type ExecResult struct {
	Hook     buildopts.Hook
	ExitCode int
	Stdout   []byte
	Stderr   []byte
	Err      error
}

func ExecuteSSH(ctx context.Context, hooks []buildopts.Hook, runner Runner, log io.Writer) ([]ExecResult, error) {
	var results []ExecResult
	for _, h := range hooks {
		if h.Phase != buildopts.Boot && h.Phase != buildopts.WSLPhase {
			continue
		}

		timeout := hookTimeout(h)
		validCodes := hookValidExitCodes(h)

		var result ExecResult
		result.Hook = h

		attempts := h.Retries + 1
		for attempt := range attempts {
			execCtx, cancel := context.WithTimeout(ctx, timeout)

			if log != nil && attempt > 0 {
				fmt.Fprintf(log, "hookexec: retrying %q (attempt %d/%d)\n", h.Cmd, attempt+1, attempts)
			}

			stdout, stderr, exitCode, err := runner.Run(execCtx, h.Cmd)
			cancel()

			result.Stdout = stdout
			result.Stderr = stderr
			result.ExitCode = exitCode
			result.Err = err

			if err != nil {
				if attempt < h.Retries {
					continue
				}
				results = append(results, result)
				return results, fmt.Errorf("hook %q failed: %w", h.Cmd, err)
			}

			if slices.Contains(validCodes, exitCode) {
				result.Err = nil
				break
			}

			if attempt < h.Retries {
				continue
			}

			result.Err = fmt.Errorf("exit code %d not in valid set %v", exitCode, validCodes)
			results = append(results, result)
			return results, result.Err
		}

		results = append(results, result)

		if h.Reboot {
			if log != nil {
				fmt.Fprintf(log, "hookexec: rebooting after %q\n", h.Cmd)
			}
			_, _, _, _ = runner.Run(ctx, "shutdown /r /t 0")
		}
	}
	return results, nil
}

func label(h buildopts.Hook, fallback string) string {
	if h.Label != "" {
		return h.Label
	}
	return fallback
}
