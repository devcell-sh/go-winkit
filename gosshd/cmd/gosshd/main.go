// Command gosshd is the SSH server staged into a guest.
//
// Usage: gosshd.exe [-addr host:port] [-vsock-port N] [logfile] [structured-port]
//
// It is cross-compiled for windows/arm64 by the host and copied onto the
// agent volume; see internal/vm/qemu. The log path is a positional argument
// because the guest has no environment to configure it through: pointing it
// at the shared volume is what keeps the log readable after the VM is gone.
//
// The optional second positional is the virtio-serial structured port (e.g.
// \\.\Global\winkit.structured.0). When present and openable, every SSH
// session is emitted there as one JSON line — command, exit code, captured
// stdout/stderr — landing in the host's build.jsonl for a durable 1:1
// record. When absent or unopenable (a base image without the port wired),
// gosshd runs exactly as before.
//
// -addr overrides the listen address (default gosshd.DefaultAddr, ":22"). The
// wsl build runs a second gosshd as a dedicated provisioning channel on a
// non-standard port so it can coexist with the Windows OpenSSH the image
// ships on :22. Flags come before the positionals; WinPE callers that pass
// none behave exactly as before.
//
// -vsock-port enables an additional vsock listener on the given port. This is
// used by the Virtualization.framework (vz) backend, which has no NAT port
// forwarding. The host connects via VirtioSocketDevice.Connect(port). Requires
// the viosock.sys driver from virtio-win.
package main

import (
	"flag"
	"io"
	"log"
	"os"

	"github.com/devcell-sh/go-winkit/gosshd"
)

// parseArgs splits the command line into the listen address, vsock port, and
// the two positional arguments (log file, structured port). It is a function
// so the flag/positional layout can be unit-tested without spawning a server.
func parseArgs(args []string) (addr, shell string, vsockPort uint, logfile, structPort string) {
	fs := flag.NewFlagSet("gosshd", flag.ContinueOnError)
	addrFlag := fs.String("addr", gosshd.DefaultAddr, "listen address (host:port)")
	shellFlag := fs.String("shell", "cmd", "session shell: cmd (WinPE-safe default) or powershell")
	vsockFlag := fs.Uint("vsock-port", 0, "vsock listen port (0 = disabled, requires viosock.sys)")
	// Errors are ignored deliberately: a bad flag falls back to defaults
	// rather than killing a guest we cannot otherwise reach to fix.
	_ = fs.Parse(args)
	rest := fs.Args()
	if len(rest) > 0 {
		logfile = rest[0]
	}
	if len(rest) > 1 {
		structPort = rest[1]
	}
	return *addrFlag, *shellFlag, *vsockFlag, logfile, structPort
}

func main() {
	addr, shell, vsockPort, logfile, structPort := parseArgs(os.Args[1:])

	out := io.Writer(os.Stderr)
	if logfile != "" {
		f, err := os.OpenFile(logfile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			log.Printf("cannot open log %s: %v", logfile, err)
		} else {
			defer f.Close()
			out = io.MultiWriter(os.Stderr, f)
		}
	}
	logger := log.New(out, "gosshd: ", log.LstdFlags)

	var events io.Writer
	if structPort != "" {
		port, err := os.OpenFile(structPort, os.O_WRONLY, 0)
		if err != nil {
			logger.Printf("cannot open structured port %s: %v", structPort, err)
		} else {
			defer port.Close()
			events = port
			logger.Printf("structured session log: %s", structPort)
		}
	}

	srv := gosshd.Server{
		Addr:     addr,
		User:     gosshd.DefaultUser,
		Password: gosshd.DefaultPassword,
		Log:      logger,
		Events:   events,
		Shell:    shell,
	}

	if vsockPort > 0 {
		vl, err := gosshd.ListenVsock(uint32(vsockPort))
		if err != nil {
			logger.Printf("vsock listen on port %d failed: %v (continuing with TCP only)", vsockPort, err)
		} else {
			logger.Printf("vsock listener on port %d", vsockPort)
			go func() {
				if err := srv.Serve(vl); err != nil {
					logger.Printf("vsock serve: %v", err)
				}
			}()
		}
	}

	logger.Printf("listening on %s (shell=%s)", addr, shell)
	logger.Fatal(srv.ListenAndServe())
}
