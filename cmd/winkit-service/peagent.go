package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	peAgentName        = "pe-agent"
	peAgentPollPeriod  = 5 * time.Second
	volumeMarkerFile   = "winkit-agent.marker"
	bootVolumeMarker   = "winkit-boot.marker"
	bootVolumeLogFile  = "winkit-pe-agent.log"
)

// pantherLogSuffixes are the paths (relative to a drive root) where Windows
// Setup writes logs. The pe-agent tails these on every drive it discovers.
var pantherLogSuffixes = []string{
	`Windows\Panther\setupact.log`,
	`Windows\Panther\setuperr.log`,
	`$windows.~bt\Sources\Panther\setupact.log`,
	`$windows.~bt\Sources\Panther\setuperr.log`,
	`Windows\INF\setupapi.dev.log`,
}

// snapshotSuffix returns the answer-volume filename for a given drive +
// log suffix pair. X:\ logs get an "x-" prefix; all others get the
// lowercase drive letter prefix (e.g. "i-winkit-setupact.log").
func snapshotSuffix(drive byte, suffix string) string {
	base := filepath.Base(suffix)
	prefix := strings.ToLower(string(drive))
	switch {
	case strings.Contains(base, "setupact"):
		return prefix + "-winkit-setupact.log"
	case strings.Contains(base, "setuperr"):
		return prefix + "-winkit-setuperr.log"
	case strings.Contains(base, "setupapi"):
		return prefix + "-winkit-setupapi.dev.log"
	default:
		return prefix + "-winkit-" + base
	}
}

// pantherLogPaths returns the full paths to tail for a set of drives.
func pantherLogPaths(drives []byte) []string {
	var paths []string
	for _, d := range drives {
		for _, suffix := range pantherLogSuffixes {
			paths = append(paths, fmt.Sprintf("%c:\\%s", d, suffix))
		}
	}
	return paths
}

// findTargetDrive scans drive letters for the offline SYSTEM registry
// hive, which signals that install.wim has been applied to that disk.
// Returns 0 if not found.
func findTargetDrive() byte {
	for _, d := range "CDEFGHIJKLMNOPQRSTUVWYZ" {
		hive := fmt.Sprintf("%c:\\Windows\\System32\\config\\SYSTEM", d)
		if _, err := os.Stat(hive); err == nil {
			return byte(d)
		}
	}
	return 0
}

func eventNameFromPath(path string) string {
	base := strings.ToLower(filepath.Base(path))
	switch {
	case strings.Contains(base, "setupact"):
		return "setupact"
	case strings.Contains(base, "setuperr"):
		return "setuperr"
	case strings.Contains(base, "setupapi"):
		return "setupapi"
	default:
		return "setup"
	}
}

type logTailer struct {
	cursors map[string]int64
	out     io.Writer
}

func newLogTailer(out io.Writer) *logTailer {
	return &logTailer{cursors: make(map[string]int64), out: out}
}

func (t *logTailer) tail(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return
	}

	cursor := t.cursors[path]
	if fi.Size() < cursor {
		cursor = 0
	}
	if fi.Size() == cursor {
		return
	}

	if _, err := f.Seek(cursor, io.SeekStart); err != nil {
		return
	}

	buf, err := io.ReadAll(f)
	if err != nil || len(buf) == 0 {
		return
	}

	eventName := eventNameFromPath(path)
	text := string(buf)
	lines := strings.Split(text, "\n")

	var advanced int64
	for i, rawLine := range lines {
		if i == len(lines)-1 {
			if !strings.HasSuffix(text, "\n") {
				break
			}
			if rawLine == "" {
				break
			}
		}
		advanced += int64(len(rawLine)) + 1
		line := strings.TrimRight(rawLine, "\r")
		if line == "" {
			continue
		}
		b := jsonlLine("ts", time.Now().UTC().Format(time.RFC3339Nano),
			"dir", "out", "event", eventName, "line", line, "src", path)
		t.out.Write(b)
	}
	t.cursors[path] = cursor + advanced
}

func findVolume() string {
	for _, d := range "CDEFGHIJKLMNOPQRSTUVWYZ" {
		marker := fmt.Sprintf("%c:\\%s", d, volumeMarkerFile)
		if _, err := os.Stat(marker); err == nil {
			return fmt.Sprintf("%c:", d)
		}
	}
	return ""
}

func findBootVolume() string {
	for _, d := range "CDEFGHIJKLMNOPQRSTUVWYZ" {
		marker := fmt.Sprintf("%c:\\%s", d, bootVolumeMarker)
		if _, err := os.Stat(marker); err == nil {
			return fmt.Sprintf("%c:", d)
		}
	}
	return ""
}

func snapshotLogs(vol string, drives []byte) {
	for _, d := range drives {
		for _, suffix := range pantherLogSuffixes {
			src := fmt.Sprintf("%c:\\%s", d, suffix)
			data, err := readFileShared(src)
			if err != nil || len(data) == 0 {
				continue
			}
			dstName := snapshotSuffix(d, suffix)
			_ = os.WriteFile(filepath.Join(vol, dstName), data, 0o644)
		}
	}
}

func readFileShared(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// defaultSerialPorts is the list of device paths the pe-agent tries when
// no explicit --log-serial is given. COM2 is the pci-serial device backed
// by build.jsonl on the host; COM1 is the PL011 UART (UEFI/serial.log).
var defaultSerialPorts = []string{
	`\\.\COM2`,
	`\\.\COM3`,
	`\\.\COM4`,
	`\\.\COM5`,
}

// lazySerialWriter probes a list of serial device paths and opens the
// first one that responds. The PCI serial device may not be enumerated
// immediately, so all paths are retried each poll cycle.
type lazySerialWriter struct {
	ports  []string
	f      io.WriteCloser
	mu     sync.Mutex
	warned bool
}

func (w *lazySerialWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		for _, port := range w.ports {
			if f := openSerialPort(port); f != nil {
				w.f = f
				fmt.Fprintf(os.Stderr, "pe-agent: opened serial port %s\n", port)
				break
			}
		}
		if w.f == nil {
			if !w.warned {
				fmt.Fprintf(os.Stderr, "pe-agent: no serial port available yet (tried %v), will retry\n", w.ports)
				w.warned = true
			}
			return len(p), nil
		}
	}
	return w.f.Write(p)
}

func (w *lazySerialWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f != nil {
		return w.f.Close()
	}
	return nil
}

// peAgentOutput builds the output writer for the pe-agent. It always
// probes serial ports (explicit --log-serial narrows to one path;
// otherwise all defaultSerialPorts are tried). Log file and stderr
// sinks are added immediately.
func peAgentOutput(opts options) (io.Writer, []io.Closer) {
	mw := &multiWriter{}
	var closers []io.Closer

	mw.sinks = append(mw.sinks, os.Stderr)

	if opts.logFile != "" {
		f, err := os.OpenFile(opts.logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err == nil {
			mw.sinks = append(mw.sinks, f)
			closers = append(closers, f)
		}
	}

	ports := defaultSerialPorts
	if opts.logSerial != "" {
		ports = []string{opts.logSerial}
	}
	lv := &lazySerialWriter{ports: ports}
	mw.sinks = append(mw.sinks, lv)
	closers = append(closers, lv)

	return mw, closers
}

// TODO: the PS1 agent (winkit-agent.ps1) supports remote command execution
// (AgentCommandFile -> run -> AgentResultFile/AgentDoneFile) and a dedicated
// diagnostics script (winkit-winpe-diag.ps1). Implement these in the native
// pe-agent so the PS1 agent path can be removed entirely.
// probeSerialPorts dumps comprehensive serial/COM port diagnostics to out.
// Runs at pe-agent startup so the host can read the results from the log
// file (C:\winkit-pe-agent.log) after SSH comes up.
func probeSerialPorts(out io.Writer) {
	emitEvent(out, "serial-probe-start", "enumerating serial ports and PCI devices")

	// 1. Try opening every COM port 1-16
	for i := 1; i <= 16; i++ {
		port := fmt.Sprintf(`\\.\COM%d`, i)
		f := openSerialPort(port)
		if f != nil {
			emitEvent(out, "serial-probe-open", fmt.Sprintf("COM%d: OPEN OK", i))
			f.Close()
		} else {
			emitEvent(out, "serial-probe-open", fmt.Sprintf("COM%d: FAILED", i))
		}
	}

	// 2. Environment and OS info
	emitEvent(out, "serial-probe-env", fmt.Sprintf("COMPUTERNAME=%s", os.Getenv("COMPUTERNAME")))
	emitEvent(out, "serial-probe-env", fmt.Sprintf("SYSTEMROOT=%s", os.Getenv("SYSTEMROOT")))
	emitEvent(out, "serial-probe-env", fmt.Sprintf("PROCESSOR_ARCHITECTURE=%s", os.Getenv("PROCESSOR_ARCHITECTURE")))

	// 3. List drive letters with content markers
	for _, d := range "CDEFGHIJKLMNOPQRSTUVWXYZ" {
		root := fmt.Sprintf("%c:\\", d)
		if _, err := os.Stat(root); err == nil {
			markers := []string{}
			for _, m := range []string{"winkit-scratch.marker", "winkit-boot.marker", "winkit-agent.marker", "autounattend.xml", "Windows\\System32\\config\\SYSTEM"} {
				if _, err := os.Stat(fmt.Sprintf("%c:\\%s", d, m)); err == nil {
					markers = append(markers, m)
				}
			}
			emitEvent(out, "serial-probe-drive", fmt.Sprintf("%c: exists, markers=%v", d, markers))
		}
	}

	// 4. Run system commands for deeper diagnostics
	cmds := []struct{ label, prog string; args []string }{
		{"pnputil-enum-devices", "pnputil", []string{"/enum-devices", "/class", "Ports"}},
		{"pnputil-enum-drivers", "pnputil", []string{"/enum-drivers"}},
		{"mode-com", "mode", nil},
		{"reg-serialcomm", "reg", []string{"query", `HKLM\HARDWARE\DEVICEMAP\SERIALCOMM`}},
		{"reg-enum-pci", "reg", []string{"query", `HKLM\SYSTEM\CurrentControlSet\Enum\PCI`, "/s", "/f", "serial", "/d"}},
		{"wmic-serialport", "wmic", []string{"path", "Win32_SerialPort", "get", "DeviceID,Name,Description,Status", "/format:list"}},
		{"wmic-pnpentity-ports", "wmic", []string{"path", "Win32_PnPEntity", "where", "PNPClass='Ports'", "get", "DeviceID,Name,Status", "/format:list"}},
		{"wmic-pnpentity-unknown", "wmic", []string{"path", "Win32_PnPEntity", "where", "ConfigManagerErrorCode<>0", "get", "DeviceID,Name,ConfigManagerErrorCode", "/format:list"}},
		{"sc-query-serial", "sc", []string{"query", "Serial"}},
		{"driverquery-serial", "driverquery", []string{"/v", "/fo", "list"}},
	}
	for _, c := range cmds {
		cmdOut, err := exec.Command(c.prog, c.args...).CombinedOutput()
		result := strings.TrimSpace(string(cmdOut))
		if err != nil {
			result = fmt.Sprintf("ERROR: %v\noutput: %s", err, result)
		}
		// Split into lines to avoid giant single JSONL entries
		for _, line := range strings.Split(result, "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				emitEvent(out, "serial-probe-"+c.label, line)
			}
		}
	}

	emitEvent(out, "serial-probe-done", "serial port probe complete")
}

func runPEAgent(out io.Writer) {
	emitEvent(out, "pe-agent-start", "tailing setup logs")

	// Write probe to a dedicated file on C: so it survives even when
	// --log-file is dropped by detachPEAgent and the boot volume isn't
	// found yet. The host reads it via SSH after gosshd is up.
	probeFile, err := os.OpenFile(`C:\winkit-serial-probe.log`, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err == nil {
		probeOut := io.MultiWriter(out, probeFile)
		probeSerialPorts(probeOut)
		probeFile.Close()
	} else {
		emitEvent(out, "serial-probe-error", fmt.Sprintf("cannot create probe log: %v", err))
		probeSerialPorts(out)
	}

	tailer := newLogTailer(out)
	var vol string
	var bootVol string
	var targetDrive byte
	// X:\ is always tailed (WinPE ramdisk). Target drive is added once
	// discovered. After reboot into the install phase, X:\ won't exist
	// but C:\ (the reassigned target) will.
	drives := []byte{'X', 'C'}
	for {
		for _, path := range pantherLogPaths(drives) {
			tailer.tail(path)
		}
		if vol == "" {
			vol = findVolume()
			if vol != "" {
				emitEvent(out, "pe-agent-volume", fmt.Sprintf("found answer volume at %s", vol))
			}
		}
		if vol != "" {
			snapshotLogs(vol, drives)
		}
		if bootVol == "" {
			bootVol = findBootVolume()
			if bootVol != "" {
				emitEvent(out, "pe-agent-boot-volume", fmt.Sprintf("found boot volume at %s", bootVol))
				logPath := filepath.Join(bootVol+`\`, bootVolumeLogFile)
				f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
				if err == nil {
					if mw, ok := out.(*multiWriter); ok {
						mw.mu.Lock()
						mw.sinks = append(mw.sinks, f)
						mw.mu.Unlock()
					}
					emitEvent(out, "pe-agent-boot-log", fmt.Sprintf("writing log to %s", logPath))
				}
			}
		}
		if targetDrive == 0 {
			targetDrive = findTargetDrive()
			if targetDrive != 0 {
				emitEvent(out, "pe-agent-target", fmt.Sprintf("found target disk at %c:\\", targetDrive))
				found := false
				for _, d := range drives {
					if d == targetDrive {
						found = true
						break
					}
				}
				if !found {
					drives = append(drives, targetDrive)
				}
			}
		}
		time.Sleep(peAgentPollPeriod)
	}
}

func emitEvent(out io.Writer, event, msg string) {
	b := jsonlLine("ts", time.Now().UTC().Format(time.RFC3339Nano),
		"dir", "out", "event", event, "msg", msg)
	out.Write(b)
}

// jsonlLine builds a JSONL line with keys in the order given.
func jsonlLine(kvs ...string) []byte {
	var buf []byte
	buf = append(buf, '{')
	for i := 0; i < len(kvs)-1; i += 2 {
		if i > 0 {
			buf = append(buf, ',')
		}
		k, _ := json.Marshal(kvs[i])
		v, _ := json.Marshal(kvs[i+1])
		buf = append(buf, k...)
		buf = append(buf, ':')
		buf = append(buf, v...)
	}
	buf = append(buf, '}', '\n')
	return buf
}
