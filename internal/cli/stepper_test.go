package cli

import (
	"log/slog"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
)

func TestStepHandler_MapsLevelsToChecklistMessages(t *testing.T) {
	var got []tea.Msg
	l := slog.New(stepHandler{send: func(m tea.Msg) { got = append(got, m) }})

	l.Debug("hidden detail")
	l.Info("searching builds")
	l.Warn("cache unusable")
	l.Error("boom")

	assert.Len(t, got, 3, "debug must not reach the checklist")
	assert.Equal(t, stepStartMsg("searching builds"), got[0])
	assert.IsType(t, stepNoteMsg{}, got[1])
	assert.IsType(t, stepNoteMsg{}, got[2])
}

func TestStepModel_ChecklistLifecycle(t *testing.T) {
	m := newStepModel()

	next, _ := m.Update(stepStartMsg("step one"))
	m = next.(stepModel)
	assert.Contains(t, m.View(), "step one")

	next, _ = m.Update(progressMsg("42%"))
	m = next.(stepModel)
	assert.Contains(t, m.View(), "42%")

	// a new step resets progress and settles the previous one
	next, cmd := m.Update(stepStartMsg("step two"))
	m = next.(stepModel)
	assert.NotNil(t, cmd, "previous step must be printed to scrollback")
	assert.NotContains(t, m.View(), "42%")

	next, _ = m.Update(finishMsg{})
	m = next.(stepModel)
	assert.Empty(t, m.View(), "finished checklist releases the live line")
}

func TestStepModel_SubItemsFoldOnSettle(t *testing.T) {
	m := newStepModel()
	next, _ := m.Update(stepStartMsg("fetching 3 ESDs"))
	m = next.(stepModel)
	next, _ = m.Update(itemStartMsg("Microsoft-Win4-Feature"))
	m = next.(stepModel)
	next, _ = m.Update(itemStartMsg("professional_en-us"))
	m = next.(stepModel)
	next, _ = m.Update(itemDoneMsg("Microsoft-Win4-Feature"))
	m = next.(stepModel)

	v := m.View()
	assert.Contains(t, v, "fetching 3 ESDs")
	assert.Contains(t, v, "Microsoft-Win4-Feature")
	assert.Contains(t, v, "professional_en-us")

	// next step folds the group: items leave the live area
	next, _ = m.Update(stepStartMsg("assembling ISO"))
	m = next.(stepModel)
	assert.NotContains(t, m.View(), "Microsoft-Win4-Feature")
	assert.Contains(t, m.View(), "assembling ISO")
}

func TestItemName_StripsESDExtension(t *testing.T) {
	assert.Equal(t, "Microsoft-Win4-Feature", itemName("Microsoft-Win4-Feature.ESD"))
	assert.Equal(t, "professional_en-us", itemName("professional_en-us.esd"))
	assert.Equal(t, "readme.txt", itemName("readme.txt"))
}
