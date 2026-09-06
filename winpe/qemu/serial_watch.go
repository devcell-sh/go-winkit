package qemu

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

const EFIShellMarker = `"EFI Internal Shell"`
const StartupNSHFailMarker = "BOOTAA64.EFI not found"
const SyncExceptionMarker = "Synchronous Exception at"
const WindowsBootManagerMarker = `"Windows Boot Manager"`

// WatchSerialForEFIShell tails the serial log and sends when the firmware
// falls through to the EFI Interactive Shell.
func WatchSerialForEFIShell(path string, stop <-chan struct{}) <-chan string {
	return WatchSerialFor(path, EFIShellMarker, stop)
}

// WatchSerialForStartupNSHFail tails the serial log and fires when startup.nsh
// reports BOOTAA64.EFI was not found.
func WatchSerialForStartupNSHFail(path string, stop <-chan struct{}) <-chan string {
	return WatchSerialFor(path, StartupNSHFailMarker, stop)
}

// WatchSerialForSyncException tails the serial log and fires on a firmware
// synchronous exception (instruction/data abort).
func WatchSerialForSyncException(path string, stop <-chan struct{}) <-chan string {
	return WatchSerialFor(path, SyncExceptionMarker, stop)
}

// WatchSerialFor tails a log file and sends on the returned channel when
// marker appears. The goroutine exits when stop is closed.
func WatchSerialFor(path string, marker string, stop <-chan struct{}) <-chan string {
	ch := make(chan string, 1)
	go func() {
		defer close(ch)
		var f *os.File
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()

		var buf strings.Builder
		for {
			select {
			case <-stop:
				if f != nil {
					f.Close()
				}
				return
			case <-ticker.C:
				if f == nil {
					var err error
					f, err = os.Open(path)
					if err != nil {
						continue
					}
				}
				tmp := make([]byte, 4096)
				n, err := f.Read(tmp)
				if n > 0 {
					buf.Write(tmp[:n])
					if strings.Contains(buf.String(), marker) {
						line := extractBdsDxeLine(buf.String())
						ch <- line
						f.Close()
						return
					}
				}
				if err != nil && err != io.EOF {
					continue
				}
			}
		}
	}()
	return ch
}

// WatchSerialForDesktopBoot tails the serial log and fires when the firmware
// boots "Windows Boot Manager" for the Nth time.
func WatchSerialForDesktopBoot(path string, n int, stop <-chan struct{}) <-chan string {
	ch := make(chan string, 1)
	go func() {
		defer close(ch)
		var f *os.File
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()

		var buf strings.Builder
		count := 0
		lastPos := 0
		for {
			select {
			case <-stop:
				if f != nil {
					f.Close()
				}
				return
			case <-ticker.C:
				if f == nil {
					var err error
					f, err = os.Open(path)
					if err != nil {
						continue
					}
				}
				tmp := make([]byte, 4096)
				nr, err := f.Read(tmp)
				if nr > 0 {
					buf.Write(tmp[:nr])
					s := buf.String()
					for {
						idx := strings.Index(s[lastPos:], WindowsBootManagerMarker)
						if idx < 0 {
							break
						}
						count++
						lastPos += idx + len(WindowsBootManagerMarker)
						if count >= n {
							ch <- fmt.Sprintf("Windows Boot Manager boot #%d detected", count)
							f.Close()
							return
						}
					}
				}
				if err != nil && err != io.EOF {
					continue
				}
			}
		}
	}()
	return ch
}

func extractBdsDxeLine(s string) string {
	idx := strings.Index(s, "BdsDxe: starting")
	if idx < 0 {
		return "firmware fell through to EFI Internal Shell"
	}
	end := strings.Index(s[idx:], "\n")
	if end < 0 {
		return stripANSI(s[idx:])
	}
	return stripANSI(s[idx : idx+end])
}

func stripANSI(s string) string {
	var out strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && !((s[j] >= 'A' && s[j] <= 'Z') || (s[j] >= 'a' && s[j] <= 'z')) {
				j++
			}
			if j < len(s) {
				j++
			}
			i = j
			continue
		}
		out.WriteByte(s[i])
		i++
	}
	return out.String()
}
