package cli

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	charmlog "github.com/charmbracelet/log"
	"github.com/devcell-sh/go-winkit/winpe"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

// newLogger builds the leveled stderr logger: styled output via
// charmbracelet/log behind the standard slog API the library packages
// accept. Without debug only milestones (Info and up) show; with it the
// full Debug stream appears, timestamped.
func newLogger(w io.Writer, debug bool) *slog.Logger {
	opts := charmlog.Options{Level: charmlog.InfoLevel}
	if debug {
		opts.Level = charmlog.DebugLevel
		opts.ReportTimestamp = true
		opts.TimeFormat = "15:04:05.000"
	}
	return slog.New(charmlog.NewWithOptions(w, opts))
}

// runUI is a command's output surface. One of three modes, picked from
// the flags and the terminal:
//
//   - checklist (TTY, no --debug): milestones render as a spinner-driven
//     checklist; debug detail is dropped
//   - leveled log (--debug, or no TTY): the full charmbracelet/log
//     stream — levels, timestamps under --debug
//   - JSONL (--debug-json): winpe.GuestEvent lines, the same schema the
//     guest's PowerShell scripts stream as build.jsonl
type runUI struct {
	Logger *slog.Logger

	w       io.Writer
	logFile *os.File
	prog    *tea.Program
	done    chan struct{}
}

func newRunUI(cmd *cobra.Command) *runUI {
	debug, _ := cmd.Flags().GetBool("debug")
	debugJSON, _ := cmd.Flags().GetBool("debug-json")
	w := cmd.ErrOrStderr()

	if debugJSON {
		return &runUI{Logger: newLoggerJSON(w), w: io.Discard}
	}
	if !debug {
		if f, ok := w.(*os.File); ok && isatty.IsTerminal(f.Fd()) {
			prog := tea.NewProgram(newStepModel(),
				tea.WithOutput(f), tea.WithInput(nil))
			ui := &runUI{
				Logger: slog.New(stepHandler{send: prog.Send}),
				prog:   prog,
				done:   make(chan struct{}),
			}
			go func() {
				defer close(ui.done)
				_, _ = prog.Run()
			}()
			return ui
		}
	}
	return &runUI{Logger: newLogger(w, debug), w: w}
}

// Progress updates the live progress readout: the dim suffix on the
// current checklist step, or a carriage-return line in log modes.
func (u *runUI) Progress(text string) {
	if u.prog != nil {
		u.prog.Send(progressMsg(text))
		return
	}
	fmt.Fprintf(u.w, "\r%s", text)
}

// itemName renders an ESD filename as a checklist sub-item label.
func itemName(filename string) string {
	if n := len(filename); n > 4 && strings.EqualFold(filename[n-4:], ".esd") {
		return filename[:n-4]
	}
	return filename
}

// ItemStart adds a live sub-item under the current checklist step (one
// downloading file). No-op outside checklist mode — the log modes carry
// per-file lines at debug level instead.
func (u *runUI) ItemStart(name string) {
	if u.prog != nil {
		u.prog.Send(itemStartMsg(name))
	}
}

// ItemDone marks a sub-item finished.
func (u *runUI) ItemDone(name string) {
	if u.prog != nil {
		u.prog.Send(itemDoneMsg(name))
	}
}

// AttachLogFile opens a structured JSONL log file at path. All slog
// output is tee'd to the file using the same GuestEvent schema that
// build.jsonl uses, so one parser (winpe.ParseGuestEvents) reads both
// host and guest logs.
func (u *runUI) AttachLogFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	u.logFile = f
	u.Logger = slog.New(winpe.MultiHandler(
		u.Logger.Handler(),
		winpe.NewGuestEventHandler(f),
	))
	return nil
}

// Finish settles the checklist (marking the in-flight step) and waits
// for the terminal to be released. Call it before printing results.
func (u *runUI) Finish(err error) {
	if u.logFile != nil {
		u.logFile.Close()
	}
	if u.prog == nil {
		if u.w != io.Discard {
			fmt.Fprintln(u.w)
		}
		return
	}
	u.prog.Send(finishMsg{failed: err != nil})
	<-u.done
}
