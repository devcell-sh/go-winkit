package cli

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// The checklist UI shown on a TTY without --debug: milestones (Info)
// become steps — the current one spins, finished ones settle into the
// scrollback as a checklist. Warnings and errors are flagged inline.
// Debug detail is dropped; --debug switches to the full leveled log.

var (
	okMark   = lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Render("✓")
	warnMark = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Render("!")
	failMark = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Render("✗")
	dimStyle = lipgloss.NewStyle().Faint(true)
)

type (
	stepStartMsg string
	stepNoteMsg  struct{ mark, text string }
	progressMsg  string
	itemStartMsg string
	itemDoneMsg  string
	finishMsg    struct{ failed bool }
)

// stepItem is a live sub-item of the current step (one downloading file).
// Items render indented under the step while it runs and fold away when
// the step settles into scrollback.
type stepItem struct {
	name string
	done bool
}

type stepModel struct {
	sp       spinner.Model
	current  string
	progress string
	items    []stepItem
	quitting bool
}

func newStepModel() stepModel {
	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	return stepModel{sp: sp}
}

func (m stepModel) Init() tea.Cmd { return m.sp.Tick }

func (m stepModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case stepStartMsg:
		var cmds []tea.Cmd
		if m.current != "" {
			cmds = append(cmds, tea.Println(okMark+" "+m.settledLine()))
		}
		m.current, m.progress, m.items = string(v), "", nil
		return m, tea.Batch(cmds...)
	case stepNoteMsg:
		return m, tea.Println(v.mark + " " + v.text)
	case progressMsg:
		m.progress = string(v)
		return m, nil
	case itemStartMsg:
		m.items = append(m.items, stepItem{name: string(v)})
		return m, nil
	case itemDoneMsg:
		for i := range m.items {
			if m.items[i].name == string(v) {
				m.items[i].done = true
				break
			}
		}
		return m, nil
	case finishMsg:
		var cmds []tea.Cmd
		if m.current != "" {
			mark := okMark
			if v.failed {
				mark = failMark
			}
			cmds = append(cmds, tea.Println(mark+" "+m.settledLine()))
		}
		m.quitting = true
		return m, tea.Sequence(append(cmds, tea.Quit)...)
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.sp, cmd = m.sp.Update(v)
		return m, cmd
	}
	return m, nil
}

// settledLine is what a step folds into when it leaves the live area:
// the title alone; sub-items collapse into it.
func (m stepModel) settledLine() string {
	return m.current
}

func (m stepModel) View() string {
	if m.quitting || m.current == "" {
		return ""
	}
	out := m.sp.View() + " " + m.current
	if m.progress != "" {
		out += "  " + dimStyle.Render(m.progress)
	}
	for _, it := range m.items {
		if it.done {
			out += "\n  " + okMark + " " + dimStyle.Render(it.name)
		} else {
			out += "\n  " + m.sp.View() + " " + it.name
		}
	}
	return out
}

// stepHandler routes slog records into the checklist program. It carries
// no attr/group state: the checklist renders messages, not fields.
type stepHandler struct {
	send func(tea.Msg)
}

func (h stepHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= slog.LevelInfo
}

func (h stepHandler) Handle(_ context.Context, r slog.Record) error {
	text := r.Message
	r.Attrs(func(a slog.Attr) bool {
		text += " " + dimStyle.Render(fmt.Sprintf("%s=%v", a.Key, a.Value))
		return true
	})
	switch {
	case r.Level >= slog.LevelError:
		h.send(stepNoteMsg{mark: failMark, text: text})
	case r.Level >= slog.LevelWarn:
		h.send(stepNoteMsg{mark: warnMark, text: text})
	default:
		h.send(stepStartMsg(text))
	}
	return nil
}

func (h stepHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h stepHandler) WithGroup(string) slog.Handler      { return h }
