package repl

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/term"
)

type promptOutcome int

const (
	promptNone promptOutcome = iota
	promptSubmit
	promptCancel
	promptExit
)

type promptResult struct {
	outcome promptOutcome
	text    string
}

type promptConfig struct {
	In          io.Reader
	Out         io.Writer
	History     []string
	ActiveSkill string
	Commands    []slashCommand
}

type promptModel struct {
	textarea textarea.Model

	width  int
	height int

	history       []string
	historyCursor int
	historyDraft  string

	activeSkill string
	commands    []slashCommand

	paletteVisible bool
	paletteCursor  int

	warning        string
	quitArmedUntil time.Time

	outcome promptOutcome
	text    string
}

func newPromptModel(cfg promptConfig) *promptModel {
	ta := textarea.New()
	ta.Placeholder = "Ask OpenMelon"
	ta.Prompt = "› "
	ta.CharLimit = 0
	ta.MaxHeight = 10
	ta.ShowLineNumbers = false
	ta.FocusedStyle.Base = lipgloss.NewStyle()
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.FocusedStyle.Prompt = promptArrowStyle
	ta.FocusedStyle.Placeholder = mutedStyle
	ta.BlurredStyle.Base = lipgloss.NewStyle()
	ta.BlurredStyle.CursorLine = lipgloss.NewStyle()
	ta.BlurredStyle.Prompt = promptArrowStyle
	ta.BlurredStyle.Placeholder = mutedStyle
	ta.Cursor.SetMode(cursor.CursorStatic)
	ta.Focus()

	width := 80
	if f, ok := cfg.Out.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		if w, _, err := term.GetSize(int(f.Fd())); err == nil && w > 0 {
			width = w
		}
	}
	ta.SetWidth(width)
	ta.SetHeight(1)

	m := &promptModel{
		textarea:      ta,
		width:         width,
		history:       append([]string(nil), cfg.History...),
		historyCursor: -1,
		activeSkill:   cfg.ActiveSkill,
		commands:      cfg.Commands,
	}
	m.recomputeInputSize()
	return m
}

func readInteractivePrompt(ctx context.Context, cfg promptConfig) (promptResult, error) {
	m := newPromptModel(cfg)
	opts := []tea.ProgramOption{tea.WithContext(ctx)}
	if cfg.In != nil {
		opts = append(opts, tea.WithInput(cfg.In))
	}
	if cfg.Out != nil {
		opts = append(opts, tea.WithOutput(cfg.Out))
	}
	prog := tea.NewProgram(m, opts...)
	finalModel, err := prog.Run()
	if err != nil {
		return promptResult{}, err
	}
	if pm, ok := finalModel.(*promptModel); ok {
		return promptResult{outcome: pm.outcome, text: pm.text}, nil
	}
	return promptResult{outcome: promptExit}, nil
}

func (m *promptModel) Init() tea.Cmd {
	return nil
}

func (m *promptModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.recomputeInputSize()
		return m, nil
	case tea.KeyMsg:
		if cmd, handled := m.handleKey(msg); handled {
			return m, cmd
		}
	}

	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	m.updatePalette()
	m.recomputeInputSize()
	return m, cmd
}

func (m *promptModel) handleKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.String() {
	case "ctrl+c":
		return m.handleCtrlC(), true
	case "ctrl+d":
		if strings.TrimSpace(m.textarea.Value()) == "" {
			m.outcome = promptExit
			return tea.Quit, true
		}
	case "esc":
		if m.paletteVisible {
			m.paletteVisible = false
			m.paletteCursor = 0
			return nil, true
		}
		if strings.TrimSpace(m.textarea.Value()) != "" {
			m.textarea.Reset()
			m.resetHistoryBrowse()
			m.warning = "input cleared"
			m.updatePalette()
			return nil, true
		}
		m.warning = ""
		return nil, true
	case "enter":
		return m.submit(), true
	case "ctrl+j", "shift+enter", "alt+enter":
		m.textarea.InsertString("\n")
		m.resetHistoryBrowse()
		m.warning = ""
		m.paletteVisible = false
		m.recomputeInputSize()
		return nil, true
	case "tab":
		if m.paletteVisible {
			m.completeSlashCommand()
			return nil, true
		}
	case "up":
		if m.paletteVisible {
			m.movePalette(-1)
			return nil, true
		}
		if m.canBrowseHistory() {
			m.browseHistory(-1)
			return nil, true
		}
	case "down":
		if m.paletteVisible {
			m.movePalette(1)
			return nil, true
		}
		if m.historyCursor >= 0 {
			m.browseHistory(1)
			return nil, true
		}
	}
	return nil, false
}

func (m *promptModel) handleCtrlC() tea.Cmd {
	if strings.TrimSpace(m.textarea.Value()) != "" {
		m.textarea.Reset()
		m.resetHistoryBrowse()
		m.paletteVisible = false
		m.warning = "input cleared"
		m.outcome = promptCancel
		return nil
	}
	now := time.Now()
	if !m.quitArmedUntil.IsZero() && now.Before(m.quitArmedUntil) {
		m.outcome = promptExit
		return tea.Quit
	}
	m.quitArmedUntil = now.Add(2 * time.Second)
	m.warning = "press Ctrl+C again to quit"
	return nil
}

func (m *promptModel) submit() tea.Cmd {
	text := strings.TrimSpace(m.textarea.Value())
	if text == "" {
		m.warning = ""
		return nil
	}
	if text == "/" && m.paletteVisible {
		m.completeSlashCommand()
		return nil
	}
	m.text = text
	m.outcome = promptSubmit
	return tea.Quit
}

func (m *promptModel) View() string {
	var parts []string
	if m.activeSkill != "" {
		parts = append(parts, helpStyle.Render("skill: "+m.activeSkill+" applies to the next message"))
	}
	if m.paletteVisible {
		parts = append(parts, m.renderPalette())
	}
	parts = append(parts, m.textarea.View())
	if m.warning != "" {
		parts = append(parts, warnStyle.Render(m.warning))
	}
	return strings.TrimRight(strings.Join(parts, "\n"), "\n")
}

func (m *promptModel) canBrowseHistory() bool {
	return !strings.Contains(m.textarea.Value(), "\n") && len(m.history) > 0
}

func (m *promptModel) browseHistory(delta int) {
	if len(m.history) == 0 {
		return
	}
	if m.historyCursor < 0 {
		m.historyDraft = m.textarea.Value()
		if delta < 0 {
			m.historyCursor = len(m.history) - 1
		} else {
			return
		}
	} else {
		m.historyCursor += delta
	}
	if m.historyCursor < 0 {
		m.historyCursor = 0
	}
	if m.historyCursor >= len(m.history) {
		m.historyCursor = -1
		m.setTextareaValueEnd(m.historyDraft)
		m.updatePalette()
		return
	}
	m.setTextareaValueEnd(m.history[m.historyCursor])
	m.updatePalette()
	m.recomputeInputSize()
}

func (m *promptModel) resetHistoryBrowse() {
	m.historyCursor = -1
	m.historyDraft = ""
}

func (m *promptModel) updatePalette() {
	value := m.textarea.Value()
	firstLine := value
	if i := strings.IndexByte(firstLine, '\n'); i >= 0 {
		firstLine = firstLine[:i]
	}
	trimmed := strings.TrimLeft(firstLine, " \t")
	m.paletteVisible = strings.HasPrefix(trimmed, "/")
	if !m.paletteVisible {
		m.paletteCursor = 0
		return
	}
	if m.paletteCursor >= len(m.filteredCommands()) {
		m.paletteCursor = 0
	}
}

func (m *promptModel) filteredCommands() []slashCommand {
	value := strings.TrimSpace(m.textarea.Value())
	if i := strings.IndexByte(value, '\n'); i >= 0 {
		value = value[:i]
	}
	if value == "" || !strings.HasPrefix(value, "/") {
		return nil
	}
	query := strings.Fields(value)
	if len(query) > 0 {
		value = query[0]
	}
	out := make([]slashCommand, 0, len(m.commands))
	for _, c := range m.commands {
		if strings.HasPrefix(c.name, value) {
			out = append(out, c)
		}
	}
	if len(out) == 0 && value == "/" {
		out = append(out, m.commands...)
	}
	return out
}

func (m *promptModel) renderPalette() string {
	rows := m.filteredCommands()
	if len(rows) == 0 {
		return helpStyle.Render("  no matching commands")
	}
	if len(rows) > 8 {
		rows = rows[:8]
	}
	var b strings.Builder
	for i, c := range rows {
		marker := "  "
		name := commandNameStyle.Render(c.name)
		if i == m.paletteCursor {
			marker = promptArrowStyle.Render("› ")
			name = paletteActiveStyle.Render(c.name)
		}
		fmt.Fprintf(&b, "%s%s %s\n", marker, name, helpStyle.Render(c.help))
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m *promptModel) movePalette(delta int) {
	rows := m.filteredCommands()
	if len(rows) == 0 {
		m.paletteCursor = 0
		return
	}
	m.paletteCursor += delta
	if m.paletteCursor < 0 {
		m.paletteCursor = len(rows) - 1
	}
	if m.paletteCursor >= len(rows) {
		m.paletteCursor = 0
	}
}

func (m *promptModel) completeSlashCommand() {
	rows := m.filteredCommands()
	if len(rows) == 0 {
		return
	}
	if m.paletteCursor >= len(rows) {
		m.paletteCursor = 0
	}
	m.setTextareaValueEnd(rows[m.paletteCursor].name + " ")
	m.paletteVisible = false
	m.warning = ""
	m.recomputeInputSize()
}

func (m *promptModel) setTextareaValueEnd(value string) {
	m.textarea.SetValue(value)
	for m.textarea.Line() < m.textarea.LineCount()-1 {
		m.textarea.CursorDown()
	}
	m.textarea.CursorEnd()
}

func (m *promptModel) recomputeInputSize() {
	width := m.width
	if width <= 0 {
		width = 80
	}
	if width < 24 {
		width = 24
	}
	m.textarea.SetWidth(width)
	height := promptVisualHeight(m.textarea.Value(), width-2)
	if height < 1 {
		height = 1
	}
	if height > 8 {
		height = 8
	}
	m.textarea.SetHeight(height)
}

func promptVisualHeight(s string, width int) int {
	if width < 8 {
		width = 8
	}
	if s == "" {
		return 1
	}
	total := 0
	for _, line := range strings.Split(s, "\n") {
		cols := ansi.StringWidth(line)
		rows := cols / width
		if cols%width != 0 || rows == 0 {
			rows++
		}
		total += rows
	}
	return total
}
