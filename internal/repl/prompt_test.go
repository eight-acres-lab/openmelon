package repl

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestPromptCtrlCClearsDraftWithoutExiting(t *testing.T) {
	m := newPromptModel(promptConfig{})
	m.textarea.SetValue("中文输入")

	model, cmd := m.Update(key("ctrl+c"))
	if cmd != nil {
		t.Fatalf("ctrl+c with draft should not quit")
	}
	pm := model.(*promptModel)
	if pm.outcome != promptCancel {
		t.Fatalf("outcome = %v, want promptCancel", pm.outcome)
	}
	if got := pm.textarea.Value(); got != "" {
		t.Fatalf("draft = %q, want empty", got)
	}
}

func TestPromptCtrlCEmptyNeedsTwoPresses(t *testing.T) {
	m := newPromptModel(promptConfig{})

	model, cmd := m.Update(key("ctrl+c"))
	if cmd != nil {
		t.Fatalf("first ctrl+c should arm quit, not quit")
	}
	pm := model.(*promptModel)
	if pm.outcome == promptExit {
		t.Fatalf("first ctrl+c exited")
	}

	model, cmd = pm.Update(key("ctrl+c"))
	if cmd == nil {
		t.Fatalf("second ctrl+c should return tea.Quit")
	}
	pm = model.(*promptModel)
	if pm.outcome != promptExit {
		t.Fatalf("outcome = %v, want promptExit", pm.outcome)
	}
}

func TestPromptHistoryRecall(t *testing.T) {
	m := newPromptModel(promptConfig{History: []string{"first", "second"}})

	model, _ := m.Update(key("up"))
	pm := model.(*promptModel)
	if got := pm.textarea.Value(); got != "second" {
		t.Fatalf("up recalled %q, want second", got)
	}

	model, _ = pm.Update(key("up"))
	pm = model.(*promptModel)
	if got := pm.textarea.Value(); got != "first" {
		t.Fatalf("second up recalled %q, want first", got)
	}

	model, _ = pm.Update(key("down"))
	pm = model.(*promptModel)
	if got := pm.textarea.Value(); got != "second" {
		t.Fatalf("down recalled %q, want second", got)
	}
}

func TestPromptSlashPaletteCompletes(t *testing.T) {
	m := newPromptModel(promptConfig{Commands: slashCommands})
	m.textarea.SetValue("/mo")
	m.updatePalette()

	model, _ := m.Update(key("tab"))
	pm := model.(*promptModel)
	if got := pm.textarea.Value(); !strings.HasPrefix(got, "/model ") {
		t.Fatalf("tab completed %q, want /model", got)
	}
}

func TestPromptCtrlJInsertsNewline(t *testing.T) {
	m := newPromptModel(promptConfig{})
	m.textarea.SetValue("line one")

	model, _ := m.Update(key("ctrl+j"))
	pm := model.(*promptModel)
	if got := pm.textarea.Value(); got != "line one\n" {
		t.Fatalf("value = %q, want newline inserted", got)
	}
}

func key(name string) tea.KeyMsg {
	switch name {
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	case "ctrl+j":
		return tea.KeyMsg{Type: tea.KeyCtrlJ}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(name)}
	}
}
