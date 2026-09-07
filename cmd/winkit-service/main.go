// Command winkit-service wraps an arbitrary command as a system service.
// On Windows it registers with the Service Control Manager; on macOS it
// creates a launchd plist; on Linux it writes a systemd unit.
//
// Usage:
//
//	winkit-service install   --name <svc> [--log-file <path>] [--log-virtio <port>] -- <command> [args...]
//	winkit-service uninstall --name <svc>
//	winkit-service start     --name <svc>
//	winkit-service stop      --name <svc>
//	winkit-service status    --name <svc>
//	winkit-service run       --name <svc> [--log-file <path>] [--log-virtio <port>] -- <command> [args...]
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"

	"github.com/kardianos/service"
)

var validVerbs = map[string]bool{
	"install": true, "uninstall": true,
	"start": true, "stop": true, "status": true,
	"run": true,
}

type options struct {
	verb      string
	name      string
	cmd       []string
	logFile   string
	logVirtio string
}

func parseArgs(args []string) (verb, name string, cmd []string, err error) {
	opts, err := parseOptions(args)
	if err != nil {
		return "", "", nil, err
	}
	return opts.verb, opts.name, opts.cmd, nil
}

func parseOptions(args []string) (options, error) {
	var opts options
	if len(args) == 0 {
		return opts, fmt.Errorf("usage: winkit-service <install|uninstall|start|stop|status|run> --name <name> [--log-file <path>] [--log-virtio <port>] [-- cmd ...]")
	}
	opts.verb = args[0]
	if !validVerbs[opts.verb] {
		return opts, fmt.Errorf("unknown verb %q; expected install|uninstall|start|stop|status|run", opts.verb)
	}
	args = args[1:]

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--name":
			if i+1 < len(args) {
				opts.name = args[i+1]
				i++
			}
		case "--log-file":
			if i+1 < len(args) {
				opts.logFile = args[i+1]
				i++
			}
		case "--log-virtio":
			if i+1 < len(args) {
				opts.logVirtio = args[i+1]
				i++
			}
		case "--":
			opts.cmd = args[i+1:]
			i = len(args)
		}
	}
	if opts.name == "" {
		return opts, fmt.Errorf("--name is required")
	}
	if (opts.verb == "install" || opts.verb == "run") && len(opts.cmd) == 0 {
		return opts, fmt.Errorf("%s requires a command after --", opts.verb)
	}
	return opts, nil
}

// multiWriter fans child output lines to all configured sinks.
type multiWriter struct {
	sinks []io.Writer
	mu    sync.Mutex
}

func (m *multiWriter) Write(p []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, w := range m.sinks {
		w.Write(p)
	}
	return len(p), nil
}

// svcLogWriter adapts service.Logger to io.Writer.
type svcLogWriter struct {
	logger service.Logger
}

func (w *svcLogWriter) Write(p []byte) (int, error) {
	w.logger.Info(string(p))
	return len(p), nil
}

type wrapper struct {
	cmd    *exec.Cmd
	cmdArg []string
	out    io.Writer
	done   chan struct{}
}

func (w *wrapper) Start(s service.Service) error {
	w.cmd = exec.Command(w.cmdArg[0], w.cmdArg[1:]...)
	w.done = make(chan struct{})

	stdout, err := w.cmd.StdoutPipe()
	if err != nil {
		return err
	}
	w.cmd.Stderr = w.cmd.Stdout

	if err := w.cmd.Start(); err != nil {
		return err
	}
	go func() {
		if w.out != nil {
			scanner := bufio.NewScanner(stdout)
			for scanner.Scan() {
				line := scanner.Text() + "\n"
				w.out.Write([]byte(line))
			}
		} else {
			io.Copy(io.Discard, stdout)
		}
		w.cmd.Wait()
		close(w.done)
	}()
	return nil
}

func (w *wrapper) Stop(s service.Service) error {
	if w.cmd != nil && w.cmd.Process != nil {
		w.cmd.Process.Kill()
		<-w.done
	}
	return nil
}

// buildOutput constructs a multi-writer from the configured log sinks.
func buildOutput(opts options, svcLogger service.Logger) (io.Writer, []io.Closer) {
	mw := &multiWriter{}
	var closers []io.Closer

	if svcLogger != nil {
		mw.sinks = append(mw.sinks, &svcLogWriter{logger: svcLogger})
	}

	if opts.logFile != "" {
		f, err := os.OpenFile(opts.logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err == nil {
			mw.sinks = append(mw.sinks, f)
			closers = append(closers, f)
		}
	}

	if opts.logVirtio != "" {
		f := openVirtioPort(opts.logVirtio)
		if f != nil {
			mw.sinks = append(mw.sinks, f)
			closers = append(closers, f)
		}
	}

	if len(mw.sinks) == 0 {
		return os.Stderr, nil
	}
	return mw, closers
}

func main() {
	opts, err := parseOptions(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	cfg := &service.Config{
		Name:        opts.name,
		DisplayName: opts.name,
		Description: fmt.Sprintf("winkit managed service: %s", opts.name),
	}
	if len(opts.cmd) > 0 {
		cfg.Executable, _ = exec.LookPath(opts.cmd[0])
		cfg.Arguments = opts.cmd[1:]
	}

	w := &wrapper{cmdArg: opts.cmd}
	svc, err := service.New(w, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "service init: %v\n", err)
		os.Exit(1)
	}

	switch opts.verb {
	case "install":
		err = service.Control(svc, "install")
	case "uninstall":
		err = service.Control(svc, "uninstall")
	case "start":
		err = service.Control(svc, "start")
	case "stop":
		err = service.Control(svc, "stop")
	case "run":
		logger, lerr := svc.Logger(nil)
		if lerr != nil {
			logger = nil
		}
		out, closers := buildOutput(opts, logger)
		w.out = out
		defer func() {
			for _, c := range closers {
				c.Close()
			}
		}()
		err = svc.Run()
	case "status":
		st, serr := svc.Status()
		if serr != nil {
			err = serr
		} else {
			names := map[service.Status]string{
				service.StatusRunning: "running",
				service.StatusStopped: "stopped",
				service.StatusUnknown: "unknown",
			}
			fmt.Println(names[st])
		}
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", opts.verb, err)
		os.Exit(1)
	}
}
