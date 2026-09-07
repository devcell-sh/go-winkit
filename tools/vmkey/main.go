// Command vmkey drives a running QEMU guest's keyboard over its QMP socket,
// so you can interact with a VM that has no working network yet (e.g. a WinPE
// or freshly-installed Windows whose NIC driver has not loaded, so SSH/RDP are
// dead). It talks to the same QMP unix socket the screenshot poller uses, so
// run it on the host where QEMU runs (e.g. the Mac under HVF), pointing -qmp at
// the socket the run created under its output dir.
//
// Usage:
//
//	vmkey -qmp <socket> type "powershell -NoProfile"   # type an ASCII string
//	vmkey -qmp <socket> key meta_l r                    # one chord (Win+R)
//	vmkey -qmp <socket> key ctrl alt delete             # secure attention seq
//	vmkey -qmp <socket> enter                           # press Return
//	vmkey -qmp <socket> run "powershell"                # Win+R, type, Enter
//
// Recipe to open an elevated PowerShell on the Windows desktop and dump the
// first-logon logs to the C: root (read them later by mounting the qcow2):
//
//	vmkey -qmp S run "powershell"
//	vmkey -qmp S type "Copy-Item C:\Windows\Panther\UnattendGC\setupact.log,C:\Windows\INF\setupapi.dev.log C:\ ; Get-NetAdapter | Out-File C:\netadapter.txt"
//	vmkey -qmp S enter
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/devcell-sh/go-winkit/vm/qemu"
)

func main() {
	qmp := flag.String("qmp", "", "path to the QEMU QMP unix socket (required)")
	delay := flag.Int("delay", 30, "per-key delay in ms when typing")
	hold := flag.Int("hold", 0, "chord hold-time in ms (0 = QEMU default)")
	flag.Parse()

	if *qmp == "" {
		fatalf("-qmp is required (the QMP socket the run created under its output dir)")
	}
	args := flag.Args()
	if len(args) == 0 {
		fatalf("expected a subcommand: type | key | enter | run")
	}

	switch args[0] {
	case "type":
		if len(args) < 2 {
			fatalf("type needs a string: vmkey -qmp S type \"...\"")
		}
		text := strings.Join(args[1:], " ")
		must(qemu.QMPTypeText(*qmp, text, *delay))

	case "key":
		if len(args) < 2 {
			fatalf("key needs at least one qcode: vmkey -qmp S key meta_l r")
		}
		must(qemu.QMPSendKeys(*qmp, *hold, args[1:]...))

	case "enter":
		must(qemu.QMPSendKeys(*qmp, *hold, "ret"))

	case "run":
		if len(args) < 2 {
			fatalf("run needs a command: vmkey -qmp S run \"powershell\"")
		}
		// Win+R opens the Run dialog; give it a moment, type, then Enter.
		must(qemu.QMPSendKeys(*qmp, *hold, "meta_l", "r"))
		time.Sleep(700 * time.Millisecond)
		must(qemu.QMPTypeText(*qmp, strings.Join(args[1:], " "), *delay))
		time.Sleep(200 * time.Millisecond)
		must(qemu.QMPSendKeys(*qmp, *hold, "ret"))

	default:
		fatalf("unknown subcommand %q (want: type | key | enter | run)", args[0])
	}
}

func must(err error) {
	if err != nil {
		fatalf("%v", err)
	}
}

func fatalf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "vmkey: "+format+"\n", a...)
	os.Exit(1)
}
