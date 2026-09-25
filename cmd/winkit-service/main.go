// Command winkit-service wraps an arbitrary command as a system service.
// On Windows it registers with the Service Control Manager; on macOS it
// creates a launchd plist; on Linux it writes a systemd unit.
//
// Usage:
//
//	winkit-service install   --name <svc> [--log-file <path>] [--log-serial <port>] -- <command> [args...]
//	winkit-service uninstall --name <svc>
//	winkit-service start     --name <svc>
//	winkit-service stop      --name <svc>
//	winkit-service status    --name <svc>
//	winkit-service run       --name <svc> [--log-file <path>] [--log-serial <port>] -- <command> [args...]
//	winkit-service run-user  --user <user> --password <password> [--current-dir <path>] -- <command> [args...]
//	winkit-service add-catalogs --dir <catalog-directory>
//	winkit-service find-catalog --file <signed-file>
//	winkit-service ensure-service --name <service>
//	winkit-service ensure-user --user <user> --password <password>
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/kardianos/service"
)

var validVerbs = map[string]bool{
	"install": true, "uninstall": true,
	"start": true, "stop": true, "status": true,
	"run":            true,
	"run-user":       true,
	"add-catalogs":   true,
	"find-catalog":   true,
	"ensure-service": true,
	"ensure-user":    true,
}

type options struct {
	verb       string
	name       string
	cmd        []string
	logFile    string
	logSerial  string
	user       string
	password   string
	currentDir string
	admin      bool
	detach     bool
	catalogDir string
	filePath   string
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
		return opts, fmt.Errorf("usage: winkit-service <install|uninstall|start|stop|status|run> --name <name> [options] [-- cmd ...] | winkit-service run-user --user <user> --password <password> [--current-dir <path>] -- cmd ... | winkit-service add-catalogs --dir <catalog-directory> | winkit-service find-catalog --file <signed-file> | winkit-service ensure-service --name <service> | winkit-service ensure-user --user <user> --password <password>")
	}
	opts.verb = args[0]
	if !validVerbs[opts.verb] {
		return opts, fmt.Errorf("unknown verb %q; expected install|uninstall|start|stop|status|run|run-user|add-catalogs|find-catalog|ensure-service|ensure-user", opts.verb)
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
		case "--log-serial", "--log-virtio":
			if i+1 < len(args) {
				opts.logSerial = args[i+1]
				i++
			}
		case "--user":
			if i+1 < len(args) {
				opts.user = args[i+1]
				i++
			}
		case "--password":
			if i+1 < len(args) {
				opts.password = args[i+1]
				i++
			}
		case "--current-dir":
			if i+1 < len(args) {
				opts.currentDir = args[i+1]
				i++
			}
		case "--admin":
			opts.admin = true
		case "--detach":
			opts.detach = true
		case "--dir":
			if i+1 < len(args) {
				opts.catalogDir = args[i+1]
				i++
			}
		case "--file":
			if i+1 < len(args) {
				opts.filePath = args[i+1]
				i++
			}
		case "--":
			opts.cmd = args[i+1:]
			i = len(args)
		}
	}
	if opts.verb == "add-catalogs" {
		if opts.catalogDir == "" {
			return opts, fmt.Errorf("add-catalogs requires --dir")
		}
		return opts, nil
	}
	if opts.verb == "find-catalog" {
		if opts.filePath == "" {
			return opts, fmt.Errorf("find-catalog requires --file")
		}
		return opts, nil
	}
	if opts.verb == "run-user" {
		if opts.user == "" || opts.password == "" {
			return opts, fmt.Errorf("run-user requires --user and --password")
		}
		if len(opts.cmd) == 0 {
			return opts, fmt.Errorf("run-user requires a command after --")
		}
		return opts, nil
	}
	if opts.verb == "ensure-user" {
		if opts.user == "" || opts.password == "" {
			return opts, fmt.Errorf("ensure-user requires --user and --password")
		}
		return opts, nil
	}
	if opts.name == "" {
		return opts, fmt.Errorf("--name is required")
	}
	if (opts.verb == "install" || opts.verb == "run") && len(opts.cmd) == 0 && opts.name != peAgentName {
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

// jsonlWriter wraps an io.Writer so every line written is valid JSONL.
// If a line is already valid JSON it passes through; otherwise it gets
// wrapped in {"ts":"...", "event":"winkit_service", "msg":"..."}.
type jsonlWriter struct {
	w    io.Writer
	name string
}

func (j *jsonlWriter) Write(p []byte) (int, error) {
	line := strings.TrimRight(string(p), "\r\n")
	if line == "" {
		return len(p), nil
	}
	var out []byte
	if json.Valid([]byte(line)) {
		out = append([]byte(line), '\n')
	} else {
		out = jsonlLine("ts", time.Now().UTC().Format(time.RFC3339Nano),
			"dir", "out", "event", "winkit_service", "svc", j.name, "msg", line)
	}
	j.w.Write(out)
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

	if opts.logSerial != "" {
		f := openSerialPort(opts.logSerial)
		if f != nil {
			mw.sinks = append(mw.sinks, &jsonlWriter{w: f, name: opts.name})
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
	if opts.verb == "add-catalogs" {
		count, registerErr := addCatalogs(opts.catalogDir)
		if registerErr != nil {
			fmt.Fprintf(os.Stderr, "add-catalogs: %v\n", registerErr)
			os.Exit(1)
		}
		fmt.Printf("registered %d catalogs\n", count)
		return
	}
	if opts.verb == "ensure-service" {
		if ensureErr := ensureService(opts.name, 30*time.Second); ensureErr != nil {
			fmt.Fprintf(os.Stderr, "ensure-service: %v\n", ensureErr)
			os.Exit(1)
		}
		fmt.Printf("service %s is running\n", opts.name)
		return
	}
	if opts.verb == "ensure-user" {
		if ensureErr := ensureLocalUser(opts.user, opts.password); ensureErr != nil {
			fmt.Fprintf(os.Stderr, "ensure-user: %v\n", ensureErr)
			os.Exit(1)
		}
		if opts.admin {
			if adminErr := addToAdministrators(opts.user); adminErr != nil {
				fmt.Fprintf(os.Stderr, "ensure-user: adding to Administrators: %v\n", adminErr)
				os.Exit(1)
			}
		}
		fmt.Printf("user %s exists\n", opts.user)
		return
	}
	if opts.verb == "find-catalog" {
		catalog, findErr := findCatalog(opts.filePath)
		if findErr != nil {
			fmt.Fprintf(os.Stderr, "find-catalog: %v\n", findErr)
			os.Exit(1)
		}
		fmt.Println(catalog)
		return
	}
	if opts.verb == "run-user" {
		exitCode, runErr := runAsUser(opts.user, opts.password, opts.currentDir, opts.cmd)
		if runErr != nil {
			fmt.Fprintf(os.Stderr, "run-user: %v\n", runErr)
			os.Exit(1)
		}
		if exitCode != 0 {
			fmt.Fprintf(os.Stderr, "run-user: command exited with %d\n", exitCode)
			os.Exit(int(exitCode))
		}
		return
	}

	cfg := &service.Config{
		Name:        opts.name,
		DisplayName: opts.name,
		Description: fmt.Sprintf("winkit managed service: %s", opts.name),
		UserName:    opts.user,
	}
	if opts.password != "" {
		cfg.Option = service.KeyValue{"Password": opts.password}
	}
	// For install: SCM must re-invoke winkit-service itself (not the child
	// command directly), so leave Executable empty (kardianos/service
	// defaults to os.Executable). Arguments tell SCM to run the "run" verb
	// with the same flags, so the output piping (--log-serial, --log-file)
	// is active when the service starts.
	if opts.verb == "install" {
		var args []string
		args = append(args, "run", "--name", opts.name)
		if opts.logFile != "" {
			args = append(args, "--log-file", opts.logFile)
		}
		if opts.logSerial != "" {
			args = append(args, "--log-serial", opts.logSerial)
		}
		args = append(args, "--")
		args = append(args, opts.cmd...)
		cfg.Arguments = args
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
		if opts.name == peAgentName && len(opts.cmd) == 0 {
			if opts.detach {
				detachPEAgent()
				return
			}
			out, closers := peAgentOutput(opts)
			defer func() {
				for _, c := range closers {
					c.Close()
				}
			}()
			runPEAgent(out)
			return
		}
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
