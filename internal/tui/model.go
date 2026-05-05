package tui

// model.go — the Bubbletea Model.
//
// State machine:
//
//	stateIdle       — waiting for user input
//	stateRunning    — runtime executing; spinner active; input is read-only
//	stateQuitArmed  — Ctrl-C pressed once; second press exits
//
// Layout, top to bottom:
//
//	1. viewport (scrollable transcript)
//	2. one-line spinner row (only when running)
//	3. textarea (bordered, multi-line input)
//	4. status line (project · model · key hints)

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/eight-acres-lab/openmelon/internal/llm"
	"github.com/eight-acres-lab/openmelon/internal/projectx"
	"github.com/eight-acres-lab/openmelon/internal/runtime"
	"github.com/eight-acres-lab/openmelon/internal/session"
)

type runState int

const (
	stateIdle runState = iota
	stateRunning
	stateQuitArmed
)

// Model is the Bubbletea Model. Constructed by Run() and never used
// outside the program loop.
type Model struct {
	// Wired by Run() before tea.NewProgram.
	workdir       string
	project       *projectx.Project
	rt            *runtime.Runtime
	systemPrompt  string
	session       *session.Session
	persistedUpTo int

	// Runner — the function the worker goroutine calls. Indirected so
	// tests can substitute a fake.
	runner func(ctx context.Context, in runtime.RunInput) (*runtime.RunResult, error)

	// Components.
	textarea textarea.Model
	viewport viewport.Model
	spinner  spinner.Model

	// State.
	state           runState
	keys            keyMap
	width, height   int
	transcript      strings.Builder // rendered transcript text fed into viewport
	streamingText   bool            // true if currently mid-stream of an assistant text reply
	history         []llm.Message
	currentTurn     int
	verbIdx         int
	cancelTurn      context.CancelFunc
	quitArmedExpiry time.Time

	// Status info displayed in the bottom bar.
	llmTag   string // e.g. "openrouter:openai/gpt-5"
	imageTag string // e.g. "openrouter:google/gemini-2.5-flash-image"

	// Slash-command palette state. Visible when the textarea value
	// starts with "/" — the palette filters known commands as the user
	// types more, Up/Down navigates filtered rows, Tab autocompletes
	// the textarea to the selected command. Enter submits as usual.
	paletteVisible bool
	paletteCursor  int
}

// slashCommand is one row in the slash palette.
type slashCommand struct {
	name string // including the leading "/"
	help string
}

// slashCommands lists every command openmelon recognizes inside the
// REPL. Order shown in the palette is the order here.
var slashCommands = []slashCommand{
	{"/help", "show this list of commands"},
	{"/clear", "forget the conversation history"},
	{"/history", "print the message log so far"},
	{"/save", "write the conversation to a file (jsonl)"},
	{"/session", "show the session directory"},
	{"/exit", "exit"},
}

// modelInit is the data Run() passes to construct the initial Model.
type modelInit struct {
	Workdir      string
	Project      *projectx.Project
	Runtime      *runtime.Runtime
	SystemPrompt string
	Session      *session.Session
	LLMTag       string
	ImageTag     string
	Runner       func(ctx context.Context, in runtime.RunInput) (*runtime.RunResult, error)
}

func newModel(init modelInit) *Model {
	ta := textarea.New()
	ta.Placeholder = "Ask anything"
	ta.Prompt = "› "
	ta.CharLimit = 0
	ta.SetHeight(1)
	ta.ShowLineNumbers = false
	// Strip the bordered chrome bubbles paints by default. Claude Code
	// is just a "› " prompt followed by the cursor — no panel around it.
	ta.FocusedStyle.Base = lipgloss.NewStyle()
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.FocusedStyle.Prompt = stylePromptArrow
	ta.FocusedStyle.Placeholder = lipgloss.NewStyle().Foreground(colorMuted)
	ta.BlurredStyle.Base = lipgloss.NewStyle()
	ta.BlurredStyle.CursorLine = lipgloss.NewStyle()
	ta.BlurredStyle.Prompt = stylePromptArrow
	ta.BlurredStyle.Placeholder = lipgloss.NewStyle().Foreground(colorMuted)
	ta.Focus()

	vp := viewport.New(80, 20)
	vp.SetContent("")

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = styleSpinner

	return &Model{
		workdir:      init.Workdir,
		project:      init.Project,
		rt:           init.Runtime,
		systemPrompt: init.SystemPrompt,
		session:      init.Session,
		runner:       init.Runner,
		llmTag:       init.LLMTag,
		imageTag:     init.ImageTag,
		textarea:     ta,
		viewport:     vp,
		spinner:      sp,
		state:        stateIdle,
		keys:         defaultKeys(),
	}
}

// Init starts the spinner ticker and shows the welcome banner.
func (m *Model) Init() tea.Cmd {
	m.appendLine(styleHelp.Render(fmt.Sprintf(
		"openmelon · project %s · session %s",
		m.project.ID, shortSession(m.session.Dir),
	)))
	m.appendLine(styleHelp.Render(
		"Type a request and press ↵. /help for commands. Esc cancels a turn; Ctrl+C twice to quit.",
	))
	m.appendLine("")
	return tea.Batch(textarea.Blink, m.spinner.Tick)
}

// Update is the bubbletea event reducer.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.resize(msg.Width, msg.Height)
		return m, nil

	case tea.KeyMsg:
		// Arm/disarm quit on Ctrl+C.
		if key.Matches(msg, m.keys.Quit) {
			if m.state == stateRunning {
				// First Ctrl+C while running → cancel the turn (Esc
				// also does this; Ctrl+C is the "I really mean it" path).
				m.cancelCurrentTurn("interrupted")
				return m, nil
			}
			if m.state == stateQuitArmed && time.Now().Before(m.quitArmedExpiry) {
				return m, tea.Quit
			}
			m.state = stateQuitArmed
			m.quitArmedExpiry = time.Now().Add(2 * time.Second)
			m.appendLine(styleWarn.Render("Press Ctrl+C again within 2s to quit."))
			return m, nil
		}
		if m.state == stateQuitArmed {
			// Any other key disarms.
			m.state = stateIdle
		}

		if key.Matches(msg, m.keys.Cancel) {
			if m.state == stateRunning {
				m.cancelCurrentTurn("interrupted")
				return m, nil
			}
			// In idle, Esc dismisses the palette if visible, otherwise
			// clears the input.
			if m.paletteVisible {
				m.paletteVisible = false
				return m, nil
			}
			m.textarea.Reset()
			m.recomputeLayout()
			return m, nil
		}

		if key.Matches(msg, m.keys.ScrollU) {
			m.viewport.HalfPageUp()
			return m, nil
		}
		if key.Matches(msg, m.keys.ScrollD) {
			m.viewport.HalfPageDown()
			return m, nil
		}

		// Slash-palette navigation. Only intercepts when palette is
		// open — otherwise these keys fall through to the textarea
		// (Up/Down would normally move the cursor in multi-line input).
		if m.state == stateIdle && m.paletteVisible {
			if msg.String() == "up" {
				if m.paletteCursor > 0 {
					m.paletteCursor--
				}
				return m, nil
			}
			if msg.String() == "down" {
				if filt := m.paletteFiltered(); m.paletteCursor < len(filt)-1 {
					m.paletteCursor++
				}
				return m, nil
			}
			if msg.String() == "tab" {
				filt := m.paletteFiltered()
				if len(filt) > 0 {
					// Replace textarea with selected command + space
					// so the user can tack on args (e.g. `/save foo.jsonl`).
					m.textarea.SetValue(filt[m.paletteCursor].name + " ")
					m.textarea.SetCursor(len(m.textarea.Value()))
					// Dismiss the palette since the command is now
					// fully typed.
					m.paletteVisible = false
					m.recomputeLayout()
				}
				return m, nil
			}
		}

		if m.state == stateIdle && key.Matches(msg, m.keys.Submit) {
			text := strings.TrimSpace(m.textarea.Value())
			if text != "" {
				m.paletteVisible = false
				return m, m.submit(text)
			}
			return m, nil
		}

		// Otherwise, route into textarea (handles shift+enter for
		// newlines automatically).
		if m.state == stateIdle {
			var cmd tea.Cmd
			m.textarea, cmd = m.textarea.Update(msg)
			m.refreshPalette()
			m.recomputeLayout()
			cmds = append(cmds, cmd)
		}

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		cmds = append(cmds, cmd)

	case verbTickMsg:
		if m.state == stateRunning {
			m.verbIdx++
			cmds = append(cmds, scheduleVerbTick())
		}

	case turnStartedMsg:
		m.currentTurn = msg.Turn
		// nothing to render — spinner shows we're working

	case textDeltaMsg:
		m.appendStreamingText(msg.Delta)

	case toolCallMsg:
		m.flushStreamingText()
		m.appendLine(renderToolCall(msg.Call))

	case toolResultMsg:
		m.appendLine(renderToolResult(msg.Call, msg.Content, msg.Err))

	case turnEndedMsg:
		m.flushStreamingText()
		// Spacer between model turns inside one Run().
		m.appendLine("")

	case runDoneMsg:
		m.state = stateIdle
		if msg.Result != nil {
			m.history = msg.Result.Messages
			if m.persistedUpTo < len(m.history) {
				_ = m.session.AppendMessages(m.history[m.persistedUpTo:])
				m.persistedUpTo = len(m.history)
			}
			if msg.Result.FinishSummary != "" {
				m.appendLine("")
				m.appendLine(msg.Result.FinishSummary)
			}
			for _, p := range msg.Result.FinishArtifacts {
				m.appendLine(styleHelp.Render("  artifact: " + p))
			}
		}
		if msg.Err != nil {
			if errIsCanceled(msg.Err) {
				m.appendLine(styleWarn.Render("[interrupted]"))
			} else {
				m.appendLine(styleErr.Render(fmt.Sprintf("error: %v", msg.Err)))
			}
		}
		m.appendLine("")
		m.textarea.Reset()
		m.textarea.Focus()
	}

	return m, tea.Batch(cmds...)
}

// View renders the current frame.
//
// Layout, top to bottom:
//   1. viewport (scrollable transcript)
//   2. spinner row (only while running)
//   3. slash-command palette (only when visible)
//   4. textarea — no border, just "› " prompt + cursor
//   5. status line — project + model only, no key hints
func (m *Model) View() string {
	var b strings.Builder
	b.WriteString(m.viewport.View())
	b.WriteString("\n")

	if m.state == stateRunning {
		b.WriteString(m.spinner.View())
		b.WriteString(" ")
		b.WriteString(spinnerVerb(m.verbIdx))
		b.WriteString("…\n")
	}

	if m.paletteVisible {
		b.WriteString(m.renderPalette())
	}

	b.WriteString(m.textarea.View())
	b.WriteString("\n")

	b.WriteString(m.statusLine())
	return b.String()
}

// --- helpers ---

// resize handles tea.WindowSizeMsg — store the new size, then recompute
// all dependent dimensions.
func (m *Model) resize(w, h int) {
	m.width = w
	m.height = h
	m.recomputeLayout()
	// Re-render any stored transcript at the new width.
	m.viewport.SetContent(m.transcript.String())
	m.viewport.GotoBottom()
}

// recomputeLayout sizes the viewport + textarea based on (a) terminal
// size, (b) current textarea content (auto-grow up to maxInputLines),
// (c) whether the spinner row + palette are showing.
//
// Called from resize() and after every keystroke that may have changed
// the textarea height.
func (m *Model) recomputeLayout() {
	if m.width == 0 || m.height == 0 {
		return
	}
	// Auto-grow textarea: 1 line by default, +1 per newline up to a cap.
	const maxInputLines = 10
	taLines := strings.Count(m.textarea.Value(), "\n") + 1
	if taLines < 1 {
		taLines = 1
	}
	if taLines > maxInputLines {
		taLines = maxInputLines
	}
	if m.textarea.Height() != taLines {
		m.textarea.SetHeight(taLines)
	}
	m.textarea.SetWidth(m.width)

	spinnerRows := 0
	if m.state == stateRunning {
		spinnerRows = 1
	}
	paletteRows := 0
	if m.paletteVisible {
		// Palette renders one row per filtered command + a header.
		paletteRows = len(m.paletteFiltered()) + 1
		if paletteRows > 8 {
			paletteRows = 8
		}
	}
	const statusRows = 1
	const spacingRows = 1 // newline between viewport and the rest
	vpHeight := m.height - taLines - spinnerRows - paletteRows - statusRows - spacingRows
	if vpHeight < 5 {
		vpHeight = 5
	}
	m.viewport.Width = m.width
	m.viewport.Height = vpHeight
}

// refreshPalette toggles the palette based on the current textarea
// value. Visible when the value starts with "/" and the user hasn't
// pressed space yet (slash + word, not "/foo arg" — once they type a
// space, the command is presumed picked).
func (m *Model) refreshPalette() {
	val := m.textarea.Value()
	if !strings.HasPrefix(val, "/") {
		m.paletteVisible = false
		m.paletteCursor = 0
		return
	}
	if strings.Contains(val, " ") {
		// User has moved past the command into args — hide palette.
		m.paletteVisible = false
		return
	}
	m.paletteVisible = true
	// Clamp cursor in case the filtered list shrank.
	if max := len(m.paletteFiltered()) - 1; m.paletteCursor > max {
		m.paletteCursor = 0
		if max < 0 {
			m.paletteCursor = 0
		}
	}
}

// paletteFiltered returns the slash commands whose name starts with the
// current textarea value (case-insensitive prefix match).
func (m *Model) paletteFiltered() []slashCommand {
	q := strings.ToLower(strings.TrimSpace(m.textarea.Value()))
	if q == "" || q == "/" {
		out := make([]slashCommand, len(slashCommands))
		copy(out, slashCommands)
		return out
	}
	var out []slashCommand
	for _, c := range slashCommands {
		if strings.HasPrefix(c.name, q) {
			out = append(out, c)
		}
	}
	return out
}

// renderPalette renders the floating list above the textarea.
func (m *Model) renderPalette() string {
	filt := m.paletteFiltered()
	if len(filt) == 0 {
		return stylePaletteHelp.Render("  (no matching commands)") + "\n"
	}
	var b strings.Builder
	for i, c := range filt {
		if i >= 8 {
			break
		}
		marker := "  "
		name := stylePaletteName.Render(c.name)
		if i == m.paletteCursor {
			marker = stylePaletteActive.Render("› ")
			name = stylePaletteActive.Render(c.name)
		}
		help := stylePaletteHelp.Render("  " + c.help)
		b.WriteString(marker + name + help + "\n")
	}
	return b.String()
}

// appendLine writes one rendered line into the transcript and scrolls
// the viewport to the bottom.
func (m *Model) appendLine(line string) {
	m.transcript.WriteString(line)
	m.transcript.WriteString("\n")
	m.viewport.SetContent(m.transcript.String())
	m.viewport.GotoBottom()
}

// appendStreamingText accumulates a streaming assistant reply. Replaces
// the trailing line in-place rather than appending (so streaming reads
// as one growing line, not many short lines).
func (m *Model) appendStreamingText(delta string) {
	if !m.streamingText {
		m.streamingText = true
	}
	m.transcript.WriteString(delta)
	m.viewport.SetContent(m.transcript.String())
	m.viewport.GotoBottom()
}

// flushStreamingText finalizes any in-progress streaming text by
// terminating it with a newline.
func (m *Model) flushStreamingText() {
	if !m.streamingText {
		return
	}
	m.transcript.WriteString("\n")
	m.streamingText = false
	m.viewport.SetContent(m.transcript.String())
	m.viewport.GotoBottom()
}

// submit kicks off a runtime.Run in a worker goroutine. Returns the
// tea.Cmd that the worker will eventually use to send runDoneMsg back.
func (m *Model) submit(text string) tea.Cmd {
	// Slash command? Handle inline.
	if strings.HasPrefix(text, "/") {
		return m.handleSlash(text)
	}

	m.appendLine(styleUserPrompt.Render("> ") + text)
	m.appendLine("")
	m.textarea.Reset()
	m.textarea.Blur()
	m.state = stateRunning
	m.verbIdx = 0
	in := runtime.RunInput{UserInput: text}
	if len(m.history) == 0 {
		in.SystemPrompt = m.systemPrompt
	} else {
		in.History = m.history
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.cancelTurn = cancel

	runCmd := func() tea.Msg {
		res, err := m.runner(ctx, in)
		return runDoneMsg{Result: res, Err: err}
	}
	return tea.Batch(runCmd, scheduleVerbTick())
}

// handleSlash processes a / command line (slash already included).
// Returns nil tea.Cmd or tea.Quit for /exit.
func (m *Model) handleSlash(line string) tea.Cmd {
	parts := strings.Fields(line)
	cmd := parts[0]
	m.appendLine(styleHelp.Render("> " + line))
	m.textarea.Reset()
	switch cmd {
	case "/exit", "/quit", "/q":
		return tea.Quit
	case "/help", "/?":
		m.appendLine(styleHelp.Render("  /clear              forget conversation history"))
		m.appendLine(styleHelp.Render("  /history            print the message log so far"))
		m.appendLine(styleHelp.Render("  /session            show the session directory"))
		m.appendLine(styleHelp.Render("  /exit | /quit | /q  exit"))
	case "/clear":
		m.history = nil
		m.persistedUpTo = 0
		m.transcript.Reset()
		m.viewport.SetContent("")
		m.appendLine(styleHelp.Render("(history cleared)"))
	case "/history":
		for i, mm := range m.history {
			label := string(mm.Role)
			if len(mm.ToolCalls) > 0 {
				label += " → tool_calls"
			}
			body := strings.ReplaceAll(mm.Content, "\n", " ")
			if len(body) > 200 {
				body = body[:200] + "…"
			}
			m.appendLine(styleHelp.Render(fmt.Sprintf("  [%d] %s: %s", i, label, body)))
		}
	case "/session":
		m.appendLine(m.session.Dir)
	default:
		m.appendLine(styleErr.Render("unknown command: " + cmd + " (try /help)"))
	}
	m.appendLine("")
	return nil
}

// cancelCurrentTurn aborts the in-flight runtime.Run. The worker will
// eventually emit a runDoneMsg with context.Canceled.
func (m *Model) cancelCurrentTurn(reason string) {
	if m.cancelTurn != nil {
		m.cancelTurn()
		m.cancelTurn = nil
	}
	m.appendLine(styleWarn.Render("[" + reason + "]"))
}

// statusLine renders the bottom bar — just project + model context.
// Key bindings (↵, ⇧↵, esc, ctrl+c) are intentionally omitted: experienced
// CLI users know them, and surfacing them on every frame is noise.
// `/help` inside the input shows the full command list when needed.
func (m *Model) statusLine() string {
	left := m.project.ID
	if m.llmTag != "" {
		left += " · " + m.llmTag
	}
	if m.imageTag != "" {
		left += " · img:" + m.imageTag
	}
	return styleStatusBar.Render(left)
}

// --- rendering helpers ---

// renderToolCall returns the "  ⏺ name(args)" line.
func renderToolCall(c llm.ToolCall) string {
	args := truncateOneLine(prettyJSON(c.Arguments), 120)
	return "  " + styleToolName.Render("⏺ "+c.Name) + styleToolArgs.Render("("+args+")")
}

// renderToolResult returns the "    ⎿ result" line, dimmed.
func renderToolResult(_ llm.ToolCall, content string, err error) string {
	if err != nil {
		return "    " + styleErr.Render("⎿ error: "+err.Error())
	}
	return "    " + styleToolResult.Render("⎿ "+truncateOneLine(content, 240))
}

func prettyJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return string(raw)
	}
	return string(b)
}

func truncateOneLine(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func shortSession(dir string) string {
	parts := strings.Split(dir, "/")
	if len(parts) == 0 {
		return dir
	}
	return parts[len(parts)-1]
}

// errIsCanceled checks if err is/wraps context.Canceled. Avoid importing
// context just for this in a place where errors.Is would do.
func errIsCanceled(err error) bool {
	return err != nil && (err == context.Canceled || strings.Contains(err.Error(), "context canceled"))
}

// --- spinner verb tick ---

// verbTickMsg fires every 2 seconds while the runtime is working so
// the spinner verb rotates ("Sketching…" → "Drafting…" → ...).
type verbTickMsg struct{}

func scheduleVerbTick() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return verbTickMsg{} })
}
