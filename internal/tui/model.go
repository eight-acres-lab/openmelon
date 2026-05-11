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
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/eight-acres-lab/openmelon/internal/continuity"
	"github.com/eight-acres-lab/openmelon/internal/llm"
	"github.com/eight-acres-lab/openmelon/internal/onboard"
	"github.com/eight-acres-lab/openmelon/internal/projectx"
	"github.com/eight-acres-lab/openmelon/internal/runtime"
	"github.com/eight-acres-lab/openmelon/internal/session"
	"github.com/eight-acres-lab/openmelon/internal/skillplus"
	"github.com/eight-acres-lab/openmelon/internal/tools"
)

type runState int

const (
	stateIdle runState = iota
	stateRunning
	stateQuitArmed
	stateModelSelect      // /model — pick LLM from preset list
	stateModelCustom      // /model → "Custom..." → typing a model id
	stateImageModelSelect // /model-image — pick image model
	stateImageModelCustom // /model-image → "Custom..." → typing
	stateApprovalPending  // bash tool waiting on user confirmation
	stateSettings         // /settings — bash permission picker
	stateSkillSelect      // /skill — pick a skillplus package
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
	markdown MarkdownRenderer

	// State.
	state           runState
	keys            keyMap
	width, height   int
	transcript      strings.Builder // rendered transcript text fed into viewport
	streamingText   bool            // true if currently mid-stream of an assistant markdown reply
	streamingPrefix string          // transcript snapshot before the current assistant stream
	streamingRaw    strings.Builder // raw markdown accumulated for the current assistant stream
	inputHistory    []string
	historyCursor   int
	inputDraft      string
	queuedInputCh   chan string
	appliedInputCh  chan int
	pendingInputs   int
	history         []llm.Message
	currentTurn     int
	cancelTurn      context.CancelFunc
	quitArmedExpiry time.Time

	// Per-Run telemetry. activityText is what the spinner row shows
	// ("Asking gpt-5.5…", "Calling search…", "Streaming response…").
	// runStartedAt anchors the elapsed timer. promptTokens /
	// completionTokens accumulate across all turns of one Run so users
	// can see the cost building up.
	activityText     string
	runStartedAt     time.Time
	promptTokens     int
	completionTokens int

	// Status info displayed in the bottom bar.
	llmTag   string // e.g. "openrouter:openai/gpt-5"
	imageTag string // e.g. "openrouter:google/gemini-2.5-flash-image"

	// Slash-command palette state. Visible when the textarea value
	// starts with "/" — the palette filters known commands as the user
	// types more, Up/Down navigates filtered rows, Tab autocompletes
	// the textarea to the selected command. Enter submits as usual.
	paletteVisible bool
	paletteCursor  int

	// Model-selector state, used when state is stateModelSelect /
	// stateImageModelSelect. The cursor points into the (presets +
	// "Custom...") row list.
	provider          string // "openrouter" / "openai" / "anthropic"
	imageProvider     string // possibly different (e.g. anthropic LLM + openai image)
	llmModel          string // current
	imageModel        string // current; "" means image disabled
	selectorCursor    int
	customModelInput  textinput.Model
	rebuildLLM        func(model string) (tag string, err error)
	rebuildImageModel func(provider, model string) (tag string, err error)

	// Active approval modal — set when an approvalRequestMsg arrives,
	// cleared after the user answers. approvalCursor: 0=Yes,
	// 1=Yes-always, 2=No.
	approvalReq    *approvalRequestMsg
	approvalCursor int
	approvalScroll int

	// Settings panel state.
	settingsCursor  int
	bashMode        projectx.BashPermissionMode
	reasoningEffort string
	saveSettings    func(s projectx.Settings) error

	// resumedFrom is the prior session id when this run was started
	// via `openmelon resume`. Shown in the banner; used in the exit
	// hint footer.
	resumedFrom string

	// Active skillplus selection. activeSkill is the slug picked via
	// /skill; the next user submit prepends a hint instructing the
	// model to compile_skill it. Cleared on /skill clear.
	activeSkill  string
	skillList    []skillplus.SkillInfo
	skillCursor  int
	skillLoadErr string // set when ListSkills failed; rendered in picker
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
	{"/skill", "pick a skillplus package for the next message"},
	{"/model", "switch the LLM model for this session"},
	{"/model-image", "switch the image-generation model"},
	{"/settings", "open the settings panel (bash permissions, etc.)"},
	{"/clear", "forget the conversation history"},
	{"/history", "print the message log so far"},
	{"/save", "write the conversation to a file (jsonl)"},
	{"/session", "show the session directory"},
	{"/events", "show recent session lifecycle events"},
	{"/space", "show a creative space summary"},
	{"/compact", "print a space compaction draft"},
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

	// Provider info used to populate the /model and /model-image
	// selectors. Provider is required; ImageProvider may be "" when
	// the user has no image model configured. LLMModel / ImageModel
	// are the currently active ids (used to render the ✓ marker).
	Provider      string
	ImageProvider string
	LLMModel      string
	ImageModel    string

	// BashMode is the current project setting (strict / auto /
	// trusted), surfaced in the /settings panel.
	BashMode        projectx.BashPermissionMode
	ReasoningEffort string

	// SaveSettings persists a Settings change made via the /settings
	// panel back to project.json AND triggers any side-effects (e.g.
	// rebuilding the tools env so the bash mode change takes effect
	// immediately without restart).
	SaveSettings func(s projectx.Settings) error

	// InitialHistory pre-populates the conversation when resuming.
	InitialHistory []llm.Message

	// ResumedFrom is the prior session id (used for the banner).
	ResumedFrom string

	// RebuildLLM is called when the user picks a new LLM model in the
	// /model selector. It must construct a fresh llm.Client + Tool-
	// Caller against the same provider, swap it into Runtime.LLM, and
	// return the new "<provider>:<model>" tag for the status bar.
	// The implementation also persists the new model into the
	// project.json defaults.
	RebuildLLM func(model string) (string, error)

	// RebuildImageModel is called when the user picks a new image
	// model. provider may be empty to disable image generation; in
	// that case the returned tag is "". The impl rebuilds the tools
	// registry with the new ImageGen and persists the choice.
	RebuildImageModel func(provider, model string) (string, error)
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
		workdir:           init.Workdir,
		project:           init.Project,
		rt:                init.Runtime,
		provider:          init.Provider,
		imageProvider:     init.ImageProvider,
		llmModel:          init.LLMModel,
		imageModel:        init.ImageModel,
		rebuildLLM:        init.RebuildLLM,
		rebuildImageModel: init.RebuildImageModel,
		bashMode:          init.BashMode,
		reasoningEffort:   init.ReasoningEffort,
		saveSettings:      init.SaveSettings,
		history:           append([]llm.Message(nil), init.InitialHistory...),
		resumedFrom:       init.ResumedFrom,
		systemPrompt:      init.SystemPrompt,
		session:           init.Session,
		runner:            init.Runner,
		llmTag:            init.LLMTag,
		imageTag:          init.ImageTag,
		textarea:          ta,
		viewport:          vp,
		spinner:           sp,
		markdown:          newMarkdownRenderer(),
		state:             stateIdle,
		keys:              defaultKeys(),
		historyCursor:     -1,
		queuedInputCh:     make(chan string, 32),
		appliedInputCh:    make(chan int, 8),
	}
}

// Init starts the spinner ticker and shows the welcome banner.
func (m *Model) Init() tea.Cmd {
	// The persistent identity row is now the top header (see View),
	// so the transcript only needs the per-launch hints.
	m.appendLine(styleHelp.Render("session " + shortSession(m.session.Dir)))
	if m.resumedFrom != "" {
		m.appendLine(styleHelp.Render("resumed from " + m.resumedFrom))
	}
	m.appendLine(styleHelp.Render(
		"Type a request and press ↵. /help for commands. Esc cancels a turn; Ctrl+C twice to quit.",
	))
	m.appendLine("")
	// Render any resumed history into the transcript.
	if len(m.history) > 0 {
		m.appendLine(styleHelp.Render(fmt.Sprintf("─── prior conversation (%d messages) ───", len(m.history))))
		m.appendLine("")
		for _, msg := range m.history {
			m.renderHistoricMessage(msg)
		}
		m.appendLine(styleHelp.Render("─── continue below ───"))
		m.appendLine("")
		// History is on disk via a different session — we don't
		// re-persist it. persistedUpTo starts at len(history) so the
		// new session only writes truly-new messages.
		m.persistedUpTo = len(m.history)
	}
	return tea.Batch(textarea.Blink, m.spinner.Tick)
}

// renderHistoricMessage prints one prior message into the transcript
// in the same format the live session uses, so a resumed conversation
// reads continuously.
func (m *Model) renderHistoricMessage(msg llm.Message) {
	switch msg.Role {
	case llm.RoleSystem:
		// Skip — system prompt is internal noise for the user.
	case llm.RoleUser:
		m.appendLine(styleUserPrompt.Render("> ") + msg.Content)
		m.appendLine("")
	case llm.RoleAssistant:
		if strings.TrimSpace(msg.Content) != "" {
			m.appendMarkdown(msg.Content)
		}
		for _, tc := range msg.ToolCalls {
			m.appendLine(renderToolCall(tc))
		}
	case llm.RoleTool:
		// We don't have the original ToolCall here, just the content.
		m.appendLine(renderToolResult(llm.ToolCall{}, msg.Content, nil))
	}
}

// Update is the bubbletea event reducer.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.resize(msg.Width, msg.Height)
		return m, nil

	case tea.MouseMsg:
		if m.state == stateApprovalPending {
			switch msg.Type {
			case tea.MouseWheelUp:
				m.scrollApproval(-3)
			case tea.MouseWheelDown:
				m.scrollApproval(3)
			}
			return m, nil
		}
		// bubbles/viewport handles wheel events natively; we just need
		// to forward the message when mouse reporting is enabled.
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd

	case tea.KeyMsg:
		// Approval modal owns all input until the user answers.
		if m.state == stateApprovalPending {
			m.updateApproval(msg)
			return m, tea.Batch(cmds...)
		}
		if m.state == stateSettings {
			m.updateSettings(msg)
			return m, tea.Batch(cmds...)
		}
		if m.state == stateSkillSelect {
			m.updateSkillSelect(msg)
			return m, tea.Batch(cmds...)
		}
		// Selector states own all key input until they exit.
		if m.inSelector() {
			switch m.state {
			case stateModelSelect, stateImageModelSelect:
				if cmd := m.updateSelector(msg); cmd != nil {
					cmds = append(cmds, cmd)
				}
			case stateModelCustom, stateImageModelCustom:
				if cmd := m.updateCustomInput(msg); cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
			return m, tea.Batch(cmds...)
		}
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
			switch msg.String() {
			case "up":
				if m.paletteCursor > 0 {
					m.paletteCursor--
				}
				return m, nil
			case "down":
				if filt := m.paletteFiltered(); m.paletteCursor < len(filt)-1 {
					m.paletteCursor++
				}
				return m, nil
			case "tab":
				// Tab autocompletes to the selected command + a trailing
				// space so the user can immediately type args.
				filt := m.paletteFiltered()
				if len(filt) > 0 {
					m.textarea.SetValue(filt[m.paletteCursor].name + " ")
					m.textarea.SetCursor(len(m.textarea.Value()))
					m.paletteVisible = false
					m.recomputeLayout()
				}
				return m, nil
			case "enter":
				// Enter executes the highlighted command directly.
				// (No args path — for that, use Tab to autocomplete,
				// type args, then Enter.)
				filt := m.paletteFiltered()
				if len(filt) == 0 {
					return m, nil // nothing to select
				}
				cmd := filt[m.paletteCursor].name
				m.paletteVisible = false
				m.textarea.Reset()
				m.recomputeLayout()
				return m, m.submit(cmd)
			}
		}

		if (m.state == stateIdle || m.state == stateRunning) && m.handleInputHistoryKey(msg) {
			return m, nil
		}

		if (m.state == stateIdle || m.state == stateRunning) && key.Matches(msg, m.keys.Newline) {
			m.insertInputNewline()
			return m, nil
		}

		if (m.state == stateIdle || m.state == stateRunning) && key.Matches(msg, m.keys.Submit) {
			text := strings.TrimSpace(m.textarea.Value())
			if text != "" {
				if m.state == stateRunning {
					m.queueInput(text)
					return m, nil
				}
				m.paletteVisible = false
				return m, m.submit(text)
			}
			return m, nil
		}

		// Otherwise, route into textarea (handles shift+enter for
		// newlines automatically).
		if m.state == stateIdle || m.state == stateRunning {
			var cmd tea.Cmd
			m.textarea, cmd = m.textarea.Update(msg)
			m.resetInputHistoryBrowse()
			if m.state == stateIdle {
				m.refreshPalette()
			} else {
				m.paletteVisible = false
			}
			m.recomputeLayout()
			cmds = append(cmds, cmd)
		}

	case spinner.TickMsg:
		m.consumeAppliedInputAcks()
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		cmds = append(cmds, cmd)

	case elapsedTickMsg:
		// Re-render once a second so the elapsed timer in the spinner
		// row updates. Only schedule the next tick while running.
		if m.state == stateRunning {
			cmds = append(cmds, scheduleElapsedTick())
		}

	case turnStartedMsg:
		m.currentTurn = msg.Turn
		m.activityText = "Thinking with " + m.llmModel
		// nothing to render — spinner row shows the activity

	case textDeltaMsg:
		m.activityText = "Streaming response"
		m.appendStreamingText(msg.Delta)

	case queuedInputAppliedMsg:
		if msg.Count > m.pendingInputs {
			m.pendingInputs = 0
		} else {
			m.pendingInputs -= msg.Count
		}

	case toolCallMsg:
		m.activityText = "Calling " + msg.Call.Name
		m.flushStreamingText()
		m.appendLine(renderToolCall(msg.Call))

	case toolResultMsg:
		m.activityText = "Got " + msg.Call.Name + " result"
		m.appendLine(renderToolResult(msg.Call, msg.Content, msg.Err))

	case turnEndedMsg:
		m.flushStreamingText()
		m.promptTokens += msg.Usage.PromptTokens
		m.completionTokens += msg.Usage.CompletionTokens
		// Spacer between model turns inside one Run().
		m.appendLine("")

	case approvalRequestMsg:
		// Worker goroutine is blocked on msg.Reply. Stash the request
		// and switch to the approval-pending state so the next View()
		// renders the modal.
		req := msg
		m.approvalReq = &req
		m.approvalCursor = 0
		m.approvalScroll = 0
		m.state = stateApprovalPending
		m.recomputeLayout()

	case runDoneMsg:
		m.state = stateIdle
		m.cancelTurn = nil
		draft := m.textarea.Value()
		if msg.Result != nil {
			m.history = msg.Result.Messages
			if m.session != nil && m.persistedUpTo < len(m.history) {
				_ = m.session.AppendMessages(m.history[m.persistedUpTo:])
				m.persistedUpTo = len(m.history)
			}
			if msg.Result.FinishSummary != "" {
				m.appendLine("")
				m.appendMarkdown(msg.Result.FinishSummary)
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
		m.textarea.Focus()
		m.consumeAppliedInputAcks()
		if next := m.takePendingInputForNextRun(); next != "" {
			cmds = append(cmds, m.submit(next))
			if draft != "" {
				m.textarea.SetValue(draft)
				m.textarea.SetCursor(len(draft))
			}
		}
	}

	return m, tea.Batch(cmds...)
}

// View renders the current frame.
//
// Layout, top to bottom:
//  1. viewport (scrollable transcript)
//  2. spinner row (only while running)
//  3. slash-command palette (only when visible)
//  4. textarea — no border, just "› " prompt + cursor
//  5. status line — project + model only, no key hints
func (m *Model) View() string {
	var b strings.Builder
	// Fixed header — top-left. Project + model identity stays anchored
	// here regardless of terminal size or scroll position. Replaces
	// the old bottom status bar.
	b.WriteString(m.headerLine())
	b.WriteString("\n")
	b.WriteString(m.viewport.View())
	b.WriteString("\n")

	if m.paletteVisible {
		b.WriteString(m.renderPalette())
	}

	switch m.state {
	case stateRunning:
		b.WriteString(m.runningStatusRow())
		b.WriteString("\n")
		b.WriteString(m.textarea.View())
		b.WriteString("\n")
	case stateApprovalPending:
		b.WriteString(m.renderApproval())
	case stateSettings:
		b.WriteString(m.renderSettings())
	case stateSkillSelect:
		b.WriteString(m.renderSkillSelect())
	case stateModelSelect, stateImageModelSelect:
		b.WriteString(m.renderSelector())
	case stateModelCustom, stateImageModelCustom:
		b.WriteString(m.renderCustomInput())
	default:
		b.WriteString(m.textarea.View())
		b.WriteString("\n")
	}
	return b.String()
}

// runningStatusRow renders the single-line status shown in place of
// the input while a turn is in flight:
//
//	⠋ Calling search · 0:12 · 1.2k in / 340 out · esc to cancel
func (m *Model) runningStatusRow() string {
	parts := []string{
		m.spinner.View() + " " + m.activityText,
		formatElapsed(time.Since(m.runStartedAt)),
		formatTokens(m.promptTokens, m.completionTokens),
	}
	if m.pendingInputs > 0 {
		parts = append(parts, fmt.Sprintf("%d pending", m.pendingInputs))
	}
	parts = append(parts, styleHelp.Render("enter adds context · esc cancels"))
	// Filter empty cells.
	filtered := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			filtered = append(filtered, p)
		}
	}
	return strings.Join(filtered, styleHelp.Render(" · "))
}

// formatElapsed renders a Duration as "0:12" / "1:23".
func formatElapsed(d time.Duration) string {
	s := int(d.Seconds())
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// formatTokens renders a "Nk in / Nk out" string when usage has been
// reported. Returns "" when both counters are zero (we hide the field
// rather than show "0 in / 0 out", which is noise pre-first-turn).
func formatTokens(in, out int) string {
	if in == 0 && out == 0 {
		return ""
	}
	return fmt.Sprintf("%s in / %s out", shortInt(in), shortInt(out))
}

// shortInt formats an integer as "1.2k" / "12.3k" / "423" — terse
// enough to fit on the running status row alongside everything else.
func shortInt(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 100000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%dk", n/1000)
}

// --- helpers ---

// resize handles tea.WindowSizeMsg — store the new size, then recompute
// all dependent dimensions. recomputeLayout calls refreshViewport
// internally, which re-pads the transcript at the new viewport height.
func (m *Model) resize(w, h int) {
	m.width = w
	m.height = h
	m.recomputeLayout()
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
	// Auto-grow textarea: count explicit newlines and soft-wrapped
	// visual rows. bubbles/textarea wraps long lines internally, but
	// if height stays at 1 the line appears to overflow horizontally.
	const maxInputLines = 10
	taLines := inputVisualLines(m.textarea.Value(), inputTextWidth(m.width))
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

	paletteRows := 0
	if m.paletteVisible {
		// Palette renders one row per filtered command + a header.
		paletteRows = len(m.paletteFiltered()) + 1
		if paletteRows > 8 {
			paletteRows = 8
		}
	}
	// State-specific overlays can either sit above the textarea
	// (running status) or replace it (modals/selectors).
	overlayRows := 0
	replaceInput := false
	switch m.state {
	case stateRunning:
		overlayRows = 1 // single status row
	case stateApprovalPending:
		replaceInput = true
		overlayRows = m.approvalBodyRows() + 9
	case stateSettings:
		replaceInput = true
		overlayRows = 12 // header + desc + 3 mode rows + footer + spacing
	case stateSkillSelect:
		replaceInput = true
		rows := len(m.skillList) + 1 // skills + "(none)"
		if rows < 2 {
			rows = 2
		}
		if rows > 12 {
			rows = 12
		}
		overlayRows = rows + 5 // header + desc + rows + footer
	case stateModelSelect, stateImageModelSelect:
		replaceInput = true
		overlayRows = len(m.modelSelectorRows()) + 5 // header+desc+blank+rows+blank+footer
	case stateModelCustom, stateImageModelCustom:
		replaceInput = true
		overlayRows = 6
	}
	if replaceInput {
		taLines = 0
	}
	const headerRows = 1
	const spacingRows = 1 // newline between viewport and the rest
	vpHeight := m.height - taLines - overlayRows - paletteRows - headerRows - spacingRows
	if vpHeight < 5 {
		vpHeight = 5
	}
	m.viewport.Width = m.width
	m.viewport.Height = vpHeight
	// Re-pad transcript so content stays bottom-anchored as the
	// viewport's effective height changes (palette opens/closes,
	// terminal resizes).
	m.refreshViewport()
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

func inputTextWidth(totalWidth int) int {
	const promptWidth = 2 // "› "
	w := totalWidth - promptWidth
	if w < 1 {
		return 1
	}
	return w
}

func inputVisualLines(value string, width int) int {
	if width < 1 {
		width = 1
	}
	if value == "" {
		return 1
	}
	lines := strings.Split(value, "\n")
	total := 0
	for _, line := range lines {
		runes := []rune(line)
		n := len(runes)
		rows := (n / width) + 1
		if n > 0 && n%width == 0 {
			rows = n / width
		}
		if rows < 1 {
			rows = 1
		}
		total += rows
	}
	return total
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
	for _, wrapped := range wrapTranscriptLine(line, m.transcriptWidth()) {
		m.transcript.WriteString(wrapped)
		m.transcript.WriteString("\n")
	}
	m.refreshViewport()
}

func (m *Model) appendMarkdown(markdown string) {
	rendered := m.renderMarkdown(markdown)
	if rendered == "" {
		return
	}
	for _, line := range strings.Split(rendered, "\n") {
		for _, wrapped := range wrapTranscriptLine(line, m.transcriptWidth()) {
			m.transcript.WriteString(wrapped)
			m.transcript.WriteString("\n")
		}
	}
	m.refreshViewport()
}

func (m *Model) renderMarkdown(markdown string) string {
	width := m.transcriptWidth()
	if m.markdown == nil {
		return renderMarkdownWithWidth(markdown, width)
	}
	return m.markdown.Render(markdown, width)
}

func (m *Model) transcriptWidth() int {
	width := m.viewport.Width
	if width <= 0 {
		width = m.width
	}
	if width <= 0 {
		width = 80
	}
	return width
}

func wrapTranscriptLine(line string, width int) []string {
	if line == "" {
		return []string{""}
	}
	if width < 8 {
		width = 8
	}
	wrapped := ansi.Wrap(line, width, "/._=&?:,")
	if wrapped == "" {
		return []string{""}
	}
	return strings.Split(wrapped, "\n")
}

func (m *Model) setTranscript(s string) {
	m.transcript.Reset()
	m.transcript.WriteString(s)
}

// refreshViewport feeds the transcript into the viewport, padding with
// leading blank lines when content is shorter than the viewport so the
// content sits at the bottom (right above the palette/input) instead of
// at the top with a sea of empty space below.
//
// Once content exceeds viewport height, padding becomes 0 and normal
// scroll-from-bottom behavior takes over.
func (m *Model) refreshViewport() {
	content := m.transcript.String()
	if m.viewport.Height > 0 {
		// Count rendered lines (transcript ends with \n, so subtract 1).
		nl := strings.Count(content, "\n")
		if nl < m.viewport.Height {
			pad := strings.Repeat("\n", m.viewport.Height-nl)
			content = pad + content
		}
	}
	m.viewport.SetContent(content)
	m.viewport.GotoBottom()
}

// appendStreamingText accumulates a streaming assistant reply. Replaces
// the trailing line in-place rather than appending (so streaming reads
// as one growing line, not many short lines).
func (m *Model) appendStreamingText(delta string) {
	if !m.streamingText {
		m.streamingText = true
		m.streamingPrefix = m.transcript.String()
		m.streamingRaw.Reset()
	}
	m.streamingRaw.WriteString(delta)
	rendered := m.renderMarkdown(m.streamingRaw.String())
	m.viewport.SetContent(m.streamingPrefix + rendered)
	m.viewport.GotoBottom()
}

// flushStreamingText finalizes any in-progress streaming text by
// terminating it with a newline.
func (m *Model) flushStreamingText() {
	if !m.streamingText {
		return
	}
	rendered := m.renderMarkdown(m.streamingRaw.String())
	if rendered != "" {
		m.setTranscript(m.streamingPrefix + rendered + "\n")
	} else {
		m.setTranscript(m.streamingPrefix)
	}
	m.streamingText = false
	m.streamingPrefix = ""
	m.streamingRaw.Reset()
	m.refreshViewport()
}

// submit kicks off a runtime.Run in a worker goroutine. Returns the
// tea.Cmd that the worker will eventually use to send runDoneMsg back.
func (m *Model) submit(text string) tea.Cmd {
	// Slash command? Handle inline.
	if strings.HasPrefix(text, "/") {
		m.resetInputHistoryBrowse()
		return m.handleSlash(text)
	}

	m.recordInputHistory(text)
	if m.session != nil {
		_ = m.session.AppendPrompt("user", text)
	}
	m.appendLine(styleUserPrompt.Render("> ") + text)
	m.appendLine("")
	m.textarea.Reset()
	m.textarea.Blur()
	m.state = stateRunning
	m.runStartedAt = time.Now()
	m.activityText = "Sending to " + m.llmModel
	m.promptTokens = 0
	m.completionTokens = 0

	// Active skill: prepend a hint so the model invokes compile_skill
	// for that package before responding. We consume the selection
	// here — one /skill pick → one applied message; clear it after
	// so the next turn isn't surprise-bound to the same skill.
	userInput := text
	if m.activeSkill != "" {
		// Be explicit about the slug format — earlier sessions saw the
		// model emit `skillplus:<id>` (an old spec format) and the
		// compile failed. Pass the bare slug.
		userInput = fmt.Sprintf(
			"Apply the skill %q to this request: first call compile_skill with skill=%q (BARE slug, no 'skillplus:' prefix) to fetch the package's prompt + output schema, then proceed.\n\n%s",
			m.activeSkill, m.activeSkill, text,
		)
		m.activeSkill = ""
	}
	in := runtime.RunInput{UserInput: userInput}
	if len(m.history) == 0 {
		in.SystemPrompt = m.systemPrompt
	} else {
		in.History = m.history
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.cancelTurn = cancel

	runCmd := func() tea.Msg {
		prevDrain := m.rt.DrainUserInput
		m.rt.DrainUserInput = m.drainQueuedInput
		defer func() { m.rt.DrainUserInput = prevDrain }()
		res, err := m.runner(ctx, in)
		return runDoneMsg{Result: res, Err: err}
	}
	return tea.Batch(runCmd, scheduleElapsedTick())
}

func (m *Model) queueInput(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	m.recordInputHistory(text)
	if m.session != nil {
		_ = m.session.AppendPrompt("pending", text)
	}
	select {
	case m.queuedInputCh <- text:
		m.pendingInputs++
		m.appendLine(styleHelp.Render("> " + text + " (pending context)"))
	default:
		m.appendLine(styleWarn.Render("input queue is full; wait for the next model call"))
	}
	m.textarea.Reset()
	m.textarea.Focus()
	m.recomputeLayout()
}

func (m *Model) drainQueuedInput() []string {
	var out []string
	for {
		select {
		case text := <-m.queuedInputCh:
			if strings.TrimSpace(text) != "" {
				out = append(out, text)
			}
		default:
			if len(out) > 0 {
				select {
				case m.appliedInputCh <- len(out):
				default:
				}
			}
			return out
		}
	}
}

func (m *Model) takePendingInputForNextRun() string {
	var pending []string
	for {
		select {
		case text := <-m.queuedInputCh:
			text = strings.TrimSpace(text)
			if text != "" {
				pending = append(pending, text)
			}
		default:
			if len(pending) == 0 {
				return ""
			}
			if m.pendingInputs < len(pending) {
				m.pendingInputs = 0
			} else {
				m.pendingInputs -= len(pending)
			}
			return strings.Join(pending, "\n\n")
		}
	}
}

func (m *Model) consumeAppliedInputAcks() {
	for {
		select {
		case count := <-m.appliedInputCh:
			if count <= 0 {
				continue
			}
			if m.pendingInputs < count {
				m.pendingInputs = 0
			} else {
				m.pendingInputs -= count
			}
		default:
			return
		}
	}
}

func (m *Model) handleInputHistoryKey(msg tea.KeyMsg) bool {
	switch msg.Type {
	case tea.KeyUp:
		return m.recallInputHistory(-1)
	case tea.KeyDown:
		return m.recallInputHistory(1)
	default:
		return false
	}
}

func (m *Model) recallInputHistory(direction int) bool {
	if len(m.inputHistory) == 0 {
		return false
	}
	if m.historyCursor == -1 && !inputHistoryCanStart(m.textarea.Value()) {
		return false
	}

	switch {
	case direction < 0:
		if m.historyCursor == -1 {
			m.inputDraft = m.textarea.Value()
			m.historyCursor = len(m.inputHistory) - 1
		} else if m.historyCursor > 0 {
			m.historyCursor--
		}
	case direction > 0:
		if m.historyCursor == -1 {
			return false
		}
		if m.historyCursor < len(m.inputHistory)-1 {
			m.historyCursor++
		} else {
			m.textarea.SetValue(m.inputDraft)
			m.textarea.SetCursor(len(m.inputDraft))
			m.resetInputHistoryBrowse()
			m.refreshPalette()
			m.recomputeLayout()
			return true
		}
	default:
		return false
	}

	if m.historyCursor < 0 || m.historyCursor >= len(m.inputHistory) {
		return false
	}
	value := m.inputHistory[m.historyCursor]
	m.textarea.SetValue(value)
	m.textarea.SetCursor(len(value))
	m.refreshPalette()
	m.recomputeLayout()
	return true
}

func inputHistoryCanStart(value string) bool {
	return !strings.Contains(value, "\n")
}

func (m *Model) recordInputHistory(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if len(m.inputHistory) == 0 || m.inputHistory[len(m.inputHistory)-1] != text {
		m.inputHistory = append(m.inputHistory, text)
	}
	m.resetInputHistoryBrowse()
}

func (m *Model) resetInputHistoryBrowse() {
	m.historyCursor = -1
	m.inputDraft = ""
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
		m.appendLine(styleHelp.Render("  /model              switch the LLM model for this session"))
		m.appendLine(styleHelp.Render("  /model-image        switch the image-generation model"))
		m.appendLine(styleHelp.Render("  /clear              forget conversation history"))
		m.appendLine(styleHelp.Render("  /history            print the message log so far"))
		m.appendLine(styleHelp.Render("  /save <path>        write the conversation to a file (jsonl)"))
		m.appendLine(styleHelp.Render("  /session            show the session directory"))
		m.appendLine(styleHelp.Render("  /events             show recent session lifecycle events"))
		m.appendLine(styleHelp.Render("  /space <id>         show a creative space summary"))
		m.appendLine(styleHelp.Render("  /compact <id>       print a compaction draft"))
		m.appendLine(styleHelp.Render("  /exit | /quit | /q  exit"))
	case "/model":
		m.openModelSelector(false)
		return nil
	case "/model-image":
		m.openModelSelector(true)
		return nil
	case "/settings", "/config":
		m.openSettings()
		return nil
	case "/skill":
		// /skill                → open picker
		// /skill clear / off    → unset active skill
		// /skill <id>           → set active skill directly
		if len(parts) == 1 {
			m.openSkillSelector()
			return nil
		}
		arg := parts[1]
		if arg == "clear" || arg == "off" || arg == "none" {
			if m.activeSkill == "" {
				m.appendLine(styleHelp.Render("(no active skill)"))
			} else {
				m.appendLine(styleHelp.Render("(skill cleared: " + m.activeSkill + ")"))
				m.activeSkill = ""
			}
			return nil
		}
		m.activeSkill = arg
		m.appendLine(styleHelp.Render("(skill: " + arg + ")"))
		return nil
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
	case "/save":
		if len(parts) < 2 {
			m.appendLine(styleErr.Render("/save: usage: /save <path>"))
			break
		}
		f, err := os.Create(parts[1])
		if err != nil {
			m.appendLine(styleErr.Render("/save: " + err.Error()))
			break
		}
		enc := json.NewEncoder(f)
		var saveErr error
		for _, mm := range m.history {
			if err := enc.Encode(mm); err != nil {
				saveErr = err
				break
			}
		}
		if err := f.Close(); saveErr == nil {
			saveErr = err
		}
		if saveErr != nil {
			m.appendLine(styleErr.Render("/save: " + saveErr.Error()))
			break
		}
		m.appendLine(styleHelp.Render(fmt.Sprintf("saved %d messages → %s", len(m.history), parts[1])))
	case "/session":
		m.appendLine(m.session.Dir)
	case "/events":
		events, err := session.LoadEvents(m.workdir, m.session.ID, 20)
		if err != nil {
			m.appendLine(styleErr.Render("/events: " + err.Error()))
			break
		}
		if len(events) == 0 {
			m.appendLine(styleHelp.Render("(no events recorded yet)"))
			break
		}
		for _, e := range events {
			m.appendLine(styleHelp.Render(fmt.Sprintf("  %s step=%d tool=%s space=%s status=%s", e.Type, e.Step, e.Tool, e.SpaceID, e.Status)))
		}
	case "/space":
		if len(parts) != 2 {
			m.appendLine(styleErr.Render("/space: usage: /space <id>"))
			break
		}
		p, err := continuity.BuildContextPacket(m.workdir, m.project.ID, parts[1])
		if err != nil {
			m.appendLine(styleErr.Render("/space: " + err.Error()))
			break
		}
		m.appendLine(styleHelp.Render(fmt.Sprintf("%s (%s): %s", p.Space.ID, p.Space.Status, p.Space.Name)))
		m.appendLine(styleHelp.Render(fmt.Sprintf("  %d decisions · %d feedback · %d episodes · %d assets", len(p.RecentDecisions), len(p.RecentFeedback), len(p.RecentEpisodes), len(p.Assets))))
	case "/compact":
		if len(parts) != 2 {
			m.appendLine(styleErr.Render("/compact: usage: /compact <space-id>"))
			break
		}
		body, err := continuity.BuildCompactionDraft(m.workdir, m.project.ID, parts[1])
		if err != nil {
			m.appendLine(styleErr.Render("/compact: " + err.Error()))
			break
		}
		m.appendMarkdown(body)
	default:
		m.appendLine(styleErr.Render("unknown command: " + cmd + " (try /help)"))
	}
	m.appendLine("")
	return nil
}

func (m *Model) insertInputNewline() {
	m.textarea.InsertString("\n")
	m.resetInputHistoryBrowse()
	m.paletteVisible = false
	m.recomputeLayout()
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

// headerLine renders the top-of-screen identity bar — project + active
// LLM + active image model. Replaces the old bottom status row so this
// info stays anchored at the top-left like the user's terminal title.
func (m *Model) headerLine() string {
	parts := []string{"openmelon", m.project.ID}
	if m.llmTag != "" {
		parts = append(parts, m.llmTag)
	}
	if m.imageTag != "" {
		parts = append(parts, "img:"+m.imageTag)
	}
	return styleStatusBar.Render(strings.Join(parts, " · "))
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
	trimmed := strings.TrimSpace(content)
	switch trimmed {
	case "[]", "{}", "null", `""`:
		return "    " + styleToolResult.Render("⎿ (no results)")
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

// elapsedTickMsg fires once a second so the spinner row's elapsed
// timer updates. The TUI re-schedules another tick from Update only
// while state == stateRunning, so the timer naturally stops when the
// turn ends.
type elapsedTickMsg struct{}

func scheduleElapsedTick() tea.Cmd {
	return tea.Tick(1*time.Second, func(time.Time) tea.Msg { return elapsedTickMsg{} })
}

// =====================================================================
// /model and /model-image selectors
// =====================================================================

// modelSelectorRows returns the list of preset ids the active selector
// should show, plus a final "Custom..." sentinel ("").
func (m *Model) modelSelectorRows() []string {
	info, ok := onboard.ProviderBySlug(m.activeSelectorProvider())
	if !ok {
		return []string{""}
	}
	var presets []onboard.Preset
	if m.state == stateImageModelSelect {
		presets = info.ImagePresets
	} else {
		presets = info.LLMPresets
	}
	out := make([]string, 0, len(presets)+1)
	for _, p := range presets {
		out = append(out, p.ID)
	}
	out = append(out, "") // sentinel for "Custom…"
	return out
}

// activeSelectorProvider picks which provider's presets to use.
// Image selector reads imageProvider; otherwise the LLM provider.
func (m *Model) activeSelectorProvider() string {
	if m.state == stateImageModelSelect {
		if m.imageProvider != "" {
			return m.imageProvider
		}
		// User has no image provider yet — default to the LLM provider
		// since most providers (openrouter / openai) support both.
		return m.provider
	}
	return m.provider
}

// openModelSelector switches the TUI into one of the model-select
// states. The current model gets the cursor highlight (shows ✓ next
// to the row when rendered).
func (m *Model) openModelSelector(image bool) {
	if image {
		m.state = stateImageModelSelect
	} else {
		m.state = stateModelSelect
	}
	rows := m.modelSelectorRows()
	cur := m.llmModel
	if image {
		cur = m.imageModel
	}
	m.selectorCursor = 0
	for i, id := range rows {
		if id != "" && id == cur {
			m.selectorCursor = i
			break
		}
	}
	m.textarea.Blur()
	m.recomputeLayout()
}

// openModelCustom transitions from the preset list into the "type a
// model id" state.
func (m *Model) openModelCustom() {
	if m.state == stateModelSelect {
		m.state = stateModelCustom
	} else if m.state == stateImageModelSelect {
		m.state = stateImageModelCustom
	}
	ti := textinput.New()
	ti.Placeholder = "vendor/model-id"
	ti.CharLimit = 200
	ti.Width = 60
	cur := m.llmModel
	if m.state == stateImageModelCustom {
		cur = m.imageModel
	}
	if cur != "" {
		ti.SetValue(cur)
	}
	ti.Focus()
	m.customModelInput = ti
	m.recomputeLayout()
}

// closeSelector returns to the idle / running state.
func (m *Model) closeSelector() {
	m.state = stateIdle
	m.textarea.Focus()
	m.recomputeLayout()
}

// applySelectedModel commits the chosen id by calling back into
// cmd_repl's rebuild closure. Logs success / failure into the
// transcript.
func (m *Model) applySelectedModel(id string, image bool) {
	if image {
		// Empty id with image mode = stay (no-op). To disable image
		// generation entirely, we'd need a separate "Disable" row;
		// skipping for now.
		if m.rebuildImageModel == nil {
			m.appendLine(styleErr.Render("openmelon: image model swap not wired"))
			return
		}
		tag, err := m.rebuildImageModel(m.activeSelectorProvider(), id)
		if err != nil {
			m.appendLine(styleErr.Render("image model swap failed: " + err.Error()))
			return
		}
		m.imageModel = id
		m.imageProvider = m.activeSelectorProvider()
		m.imageTag = tag
		m.appendLine(styleHelp.Render("(image model: " + id + ")"))
		return
	}
	if m.rebuildLLM == nil {
		m.appendLine(styleErr.Render("openmelon: LLM swap not wired"))
		return
	}
	tag, err := m.rebuildLLM(id)
	if err != nil {
		m.appendLine(styleErr.Render("LLM swap failed: " + err.Error()))
		return
	}
	m.llmModel = id
	m.llmTag = tag
	m.appendLine(styleHelp.Render("(LLM: " + id + ")"))
}

// updateSelector handles key input for the preset-list selector states.
func (m *Model) updateSelector(msg tea.KeyMsg) tea.Cmd {
	rows := m.modelSelectorRows()
	switch msg.String() {
	case "esc", "ctrl+c":
		m.closeSelector()
		return nil
	case "up", "k":
		if m.selectorCursor > 0 {
			m.selectorCursor--
		}
	case "down", "j":
		if m.selectorCursor < len(rows)-1 {
			m.selectorCursor++
		}
	case "enter":
		image := m.state == stateImageModelSelect
		picked := rows[m.selectorCursor]
		if picked == "" {
			// Custom row.
			m.openModelCustom()
			return nil
		}
		m.applySelectedModel(picked, image)
		m.closeSelector()
	}
	// Number-key shortcut (1-9).
	if len(msg.String()) == 1 && msg.String()[0] >= '1' && msg.String()[0] <= '9' {
		n := int(msg.String()[0] - '1')
		if n < len(rows) {
			image := m.state == stateImageModelSelect
			picked := rows[n]
			if picked == "" {
				m.openModelCustom()
				return nil
			}
			m.applySelectedModel(picked, image)
			m.closeSelector()
		}
	}
	return nil
}

// updateCustomInput handles key input for the "type a model id" state.
func (m *Model) updateCustomInput(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.closeSelector()
		return nil
	case "enter":
		val := strings.TrimSpace(m.customModelInput.Value())
		if val == "" {
			return nil
		}
		image := m.state == stateImageModelCustom
		m.applySelectedModel(val, image)
		m.closeSelector()
		return nil
	}
	var cmd tea.Cmd
	m.customModelInput, cmd = m.customModelInput.Update(msg)
	return cmd
}

// renderSelector renders the preset-list selector overlay (replaces
// the input area while a selector is active).
func (m *Model) renderSelector() string {
	var b strings.Builder
	header := "Select LLM model"
	desc := "Switch the model used by this and future turns. Persists to project.json."
	if m.state == stateImageModelSelect {
		header = "Select image model"
		desc = "Switch the model used by generate_image. Persists to project.json."
	}
	b.WriteString(headerStyle.Render(header))
	b.WriteString("\n")
	b.WriteString(styleHelp.Render(desc))
	b.WriteString("\n\n")

	info, _ := onboard.ProviderBySlug(m.activeSelectorProvider())
	var presets []onboard.Preset
	current := m.llmModel
	if m.state == stateImageModelSelect {
		presets = info.ImagePresets
		current = m.imageModel
	} else {
		presets = info.LLMPresets
	}
	rows := append([]onboard.Preset(nil), presets...)
	rows = append(rows, onboard.Preset{ID: "", Subtitle: "Type your own model id"})

	for i, r := range rows {
		marker := "  "
		num := fmt.Sprintf("%d.", i+1)
		title := r.ID
		if title == "" {
			title = "Custom…"
		}
		check := ""
		if r.ID != "" && r.ID == current {
			check = " ✓"
		}
		if i == m.selectorCursor {
			marker = stylePromptArrow.Render("› ")
			num = stylePaletteActive.Render(num)
			title = stylePaletteActive.Render(title + check)
		} else {
			num = stylePaletteName.Render(num)
			title = title + check
		}
		fmt.Fprintf(&b, "%s%s %-30s %s\n", marker, num, title, styleHelp.Render(r.Subtitle))
	}
	b.WriteString("\n")
	b.WriteString(styleHelp.Render("Enter to confirm · Esc to cancel · 1-N shortcut"))
	b.WriteString("\n")
	return b.String()
}

// renderCustomInput renders the textinput overlay.
func (m *Model) renderCustomInput() string {
	var b strings.Builder
	header := "Custom LLM model id"
	if m.state == stateImageModelCustom {
		header = "Custom image model id"
	}
	b.WriteString(headerStyle.Render(header))
	b.WriteString("\n")
	b.WriteString(styleHelp.Render("Type the vendor-specific id (e.g. openai/gpt-5.5, google/gemini-3-pro-preview)."))
	b.WriteString("\n\n")
	b.WriteString(m.customModelInput.View())
	b.WriteString("\n\n")
	b.WriteString(styleHelp.Render("Enter to confirm · Esc to cancel"))
	b.WriteString("\n")
	return b.String()
}

// inSelector returns true when the model is in any selector state.
func (m *Model) inSelector() bool {
	switch m.state {
	case stateModelSelect, stateModelCustom, stateImageModelSelect, stateImageModelCustom:
		return true
	}
	return false
}

// =====================================================================
// Approval modal (bash tool confirmation)
// =====================================================================

// approvalOptions are the three rows of the bash approval modal. The
// "always" row is data-driven so renderApproval can include the
// command's first binary in its label.
func (m *Model) approvalOptions() []string {
	binary := "this binary"
	if m.approvalReq != nil && m.approvalReq.Binary != "" {
		binary = m.approvalReq.Binary
	}
	return []string{
		"Yes",
		fmt.Sprintf("Yes, always allow `%s` this session", binary),
		"No",
	}
}

// updateApproval handles key input while a bash approval is pending.
// 1 / Enter / y → approve once, 2 → approve + always for this binary,
// 3 / n / Esc → deny. Up/Down navigates choices; PgUp/PgDn scrolls
// the command/details pane.
func (m *Model) updateApproval(msg tea.KeyMsg) {
	max := len(m.approvalOptions()) - 1
	switch msg.String() {
	case "up", "k":
		if m.approvalCursor > 0 {
			m.approvalCursor--
		}
	case "down", "j":
		if m.approvalCursor < max {
			m.approvalCursor++
		}
	case "pgup", "ctrl+u":
		m.scrollApproval(-m.approvalBodyRows())
	case "pgdown", "ctrl+d":
		m.scrollApproval(m.approvalBodyRows())
	case "home":
		m.approvalScroll = 0
	case "end":
		m.approvalScroll = m.approvalMaxScroll()
	case "1", "y", "Y":
		m.approvalCursor = 0
		m.answerApproval(true, false)
	case "2":
		m.approvalCursor = 1
		m.answerApproval(true, true)
	case "3", "n", "N", "esc":
		m.approvalCursor = max
		m.answerApproval(false, false)
	case "enter":
		switch m.approvalCursor {
		case 0:
			m.answerApproval(true, false)
		case 1:
			m.answerApproval(true, true)
		default:
			m.answerApproval(false, false)
		}
	}
}

// answerApproval sends the user's choice back to the worker goroutine
// and transitions back to stateRunning so the spinner / activity row
// resumes.
func (m *Model) answerApproval(approved, always bool) {
	if m.approvalReq == nil {
		return
	}
	m.approvalReq.Reply <- tools.ApprovalDecision{Approved: approved, Always: always}
	m.approvalReq = nil
	m.approvalScroll = 0
	m.state = stateRunning
	switch {
	case approved && always:
		m.activityText = "Running bash (allowed for session)"
	case approved:
		m.activityText = "Running bash"
	default:
		m.activityText = "Bash denied"
	}
	m.recomputeLayout()
}

func (m *Model) approvalBodyRows() int {
	rows := 8
	if m.height > 0 {
		rows = m.height / 3
	}
	if rows < 4 {
		rows = 4
	}
	if rows > 14 {
		rows = 14
	}
	return rows
}

func (m *Model) approvalContentWidth() int {
	width := m.width
	if width <= 0 {
		width = 80
	}
	width -= 4
	if width < 20 {
		width = 20
	}
	return width
}

func (m *Model) approvalContentLines() []string {
	r := m.approvalReq
	if r == nil {
		return nil
	}
	width := m.approvalContentWidth()
	var lines []string
	if strings.TrimSpace(r.Description) != "" {
		lines = append(lines, styleHelp.Render("Reason"))
		lines = append(lines, wrapPlainText(r.Description, width)...)
		lines = append(lines, "")
	}
	lines = append(lines, styleHelp.Render("Command"))
	for _, line := range strings.Split(r.Command, "\n") {
		if line == "" {
			lines = append(lines, "  ")
			continue
		}
		for _, wrapped := range wrapPlainText(line, width-2) {
			lines = append(lines, "  "+wrapped)
		}
	}
	return lines
}

func (m *Model) approvalMaxScroll() int {
	lines := m.approvalContentLines()
	max := len(lines) - m.approvalBodyRows()
	if max < 0 {
		return 0
	}
	return max
}

func (m *Model) clampApprovalScroll() {
	max := m.approvalMaxScroll()
	if m.approvalScroll < 0 {
		m.approvalScroll = 0
	}
	if m.approvalScroll > max {
		m.approvalScroll = max
	}
}

func (m *Model) scrollApproval(delta int) {
	m.approvalScroll += delta
	m.clampApprovalScroll()
}

func wrapPlainText(text string, width int) []string {
	if width < 8 {
		width = 8
	}
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if line == "" {
			out = append(out, "")
			continue
		}
		out = append(out, strings.Split(ansi.Wrap(line, width, "/._=&?:,"), "\n")...)
	}
	if len(out) == 0 {
		return []string{""}
	}
	return out
}

// =====================================================================
// Skill picker (/skill)
// =====================================================================

func (m *Model) openSkillSelector() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	skills, err := skillplus.ListSkills(ctx)
	if err != nil {
		m.skillLoadErr = err.Error()
	} else {
		m.skillLoadErr = ""
	}
	m.skillList = skills
	m.skillCursor = 0
	for i, s := range skills {
		if s.ID == m.activeSkill {
			m.skillCursor = i
			break
		}
	}
	m.state = stateSkillSelect
	m.recomputeLayout()
}

func (m *Model) updateSkillSelect(msg tea.KeyMsg) {
	max := len(m.skillList) // last row = "(none)"
	switch msg.String() {
	case "esc", "ctrl+c":
		m.state = stateIdle
		m.recomputeLayout()
		return
	case "up", "k":
		if m.skillCursor > 0 {
			m.skillCursor--
		}
	case "down", "j":
		if m.skillCursor < max {
			m.skillCursor++
		}
	case "enter":
		m.commitSkillPick()
	}
	if len(msg.String()) == 1 && msg.String()[0] >= '1' && msg.String()[0] <= '9' {
		n := int(msg.String()[0] - '1')
		if n <= max {
			m.skillCursor = n
			m.commitSkillPick()
		}
	}
}

func (m *Model) commitSkillPick() {
	if m.skillCursor == len(m.skillList) {
		// "(none)" — clear selection.
		if m.activeSkill != "" {
			m.appendLine(styleHelp.Render("(skill cleared: " + m.activeSkill + ")"))
			m.activeSkill = ""
		}
	} else if m.skillCursor < len(m.skillList) {
		picked := m.skillList[m.skillCursor]
		m.activeSkill = picked.ID
		m.appendLine(styleHelp.Render("(skill: " + picked.ID + ") — applies to your next message"))
	}
	m.state = stateIdle
	m.recomputeLayout()
}

func (m *Model) renderSkillSelect() string {
	var b strings.Builder
	b.WriteString(headerStyle.Render("Select a skillplus package"))
	b.WriteString("\n")
	b.WriteString(styleHelp.Render("Picked skill is applied to your next message — the model is told to compile_skill it before responding. Pick (none) to clear."))
	b.WriteString("\n\n")
	if m.skillLoadErr != "" {
		b.WriteString(styleErr.Render("error listing skills: " + m.skillLoadErr))
		b.WriteString("\n\n")
	}
	rows := append([]skillplus.SkillInfo(nil), m.skillList...)
	for i, s := range rows {
		marker := "  "
		num := fmt.Sprintf("%d.", i+1)
		title := s.ID
		check := ""
		if s.ID == m.activeSkill {
			check = " ✓"
		}
		if i == m.skillCursor {
			marker = stylePromptArrow.Render("› ")
			num = stylePaletteActive.Render(num)
			title = stylePaletteActive.Render(title + check)
		} else {
			title = title + check
		}
		desc := s.Description
		if len(desc) > 80 {
			desc = desc[:80] + "…"
		}
		fmt.Fprintf(&b, "%s%s %-28s %s\n", marker, num, title, styleHelp.Render(desc))
	}
	// "(none)" row.
	{
		i := len(rows)
		marker := "  "
		num := fmt.Sprintf("%d.", i+1)
		title := "(none)"
		if i == m.skillCursor {
			marker = stylePromptArrow.Render("› ")
			num = stylePaletteActive.Render(num)
			title = stylePaletteActive.Render(title)
		}
		fmt.Fprintf(&b, "%s%s %-28s %s\n", marker, num, title, styleHelp.Render("don't apply any skill"))
	}
	b.WriteString("\n")
	b.WriteString(styleHelp.Render("Enter to confirm · Esc to cancel · 1-N shortcut"))
	b.WriteString("\n")
	return b.String()
}

// =====================================================================
// Settings panel (/settings)
// =====================================================================

// bashModeRows is the ordered list shown in /settings → Bash perms.
var bashModeRows = []struct {
	mode  projectx.BashPermissionMode
	title string
	desc  string
}{
	{projectx.BashModeStrict, "Strict",
		"Every bash needs your approval. Judge LLM auto-blocks anything destructive."},
	{projectx.BashModeAuto, "Auto-judge",
		"Judge LLM auto-runs read-only commands; you approve writes; destructive ones blocked."},
	{projectx.BashModeTrusted, "Trusted (DANGEROUS)",
		"Run any bash without asking. Like Claude Code's --dangerously-skip-permissions. Use only in throwaway projects."},
}

var reasoningRows = []struct {
	effort string
	title  string
	desc   string
}{
	{"", "Auto",
		"Use OpenMelon's model-aware default. GPT-5-family models default to xhigh."},
	{"medium", "Medium",
		"Balanced reasoning depth for normal iteration."},
	{"high", "High",
		"Deeper reasoning for planning, code, and tool-heavy tasks."},
	{"xhigh", "XHigh",
		"Maximum reasoning hint when the endpoint supports it."},
}

func (m *Model) openSettings() {
	m.state = stateSettings
	m.settingsCursor = 0
	for i, r := range bashModeRows {
		if r.mode == m.bashMode {
			m.settingsCursor = i + 1
			break
		}
	}
	m.recomputeLayout()
}

func (m *Model) updateSettings(msg tea.KeyMsg) {
	max := len(settingsRows()) - 1
	switch msg.String() {
	case "esc", "ctrl+c":
		m.state = stateIdle
		m.recomputeLayout()
	case "up", "k":
		if m.settingsCursor > 0 {
			m.settingsCursor--
		}
		m.skipSettingsSection(-1)
	case "down", "j":
		if m.settingsCursor < max {
			m.settingsCursor++
		}
		m.skipSettingsSection(1)
	case "enter":
		m.commitSettingsPick()
		m.state = stateIdle
		m.recomputeLayout()
	}
	// Number-key shortcut.
	if len(msg.String()) == 1 && msg.String()[0] >= '1' && msg.String()[0] <= '9' {
		if idx, ok := settingsRowIndexForNumber(int(msg.String()[0] - '0')); ok && idx <= max {
			m.settingsCursor = idx
		}
	}
}

func (m *Model) skipSettingsSection(direction int) {
	rows := settingsRows()
	if len(rows) == 0 {
		return
	}
	if m.settingsCursor < 0 {
		m.settingsCursor = 0
	}
	if m.settingsCursor >= len(rows) {
		m.settingsCursor = len(rows) - 1
	}
	if rows[m.settingsCursor].kind != "section" {
		return
	}
	if direction < 0 {
		for m.settingsCursor > 0 && rows[m.settingsCursor].kind == "section" {
			m.settingsCursor--
		}
		if rows[m.settingsCursor].kind == "section" && len(rows) > 1 {
			m.settingsCursor = 1
		}
		return
	}
	for m.settingsCursor < len(rows)-1 && rows[m.settingsCursor].kind == "section" {
		m.settingsCursor++
	}
	if rows[m.settingsCursor].kind == "section" && len(rows) > 1 {
		m.settingsCursor = len(rows) - 1
	}
}

type settingsRow struct {
	kind  string
	title string
	desc  string
}

func settingsRows() []settingsRow {
	rows := make([]settingsRow, 0, len(bashModeRows)+len(reasoningRows)+2)
	rows = append(rows, settingsRow{kind: "section", title: "Bash permissions"})
	for _, r := range bashModeRows {
		rows = append(rows, settingsRow{kind: "bash:" + string(r.mode), title: r.title, desc: r.desc})
	}
	rows = append(rows, settingsRow{kind: "section", title: "Reasoning effort"})
	for _, r := range reasoningRows {
		rows = append(rows, settingsRow{kind: "reasoning:" + r.effort, title: r.title, desc: r.desc})
	}
	return rows
}

func settingsRowIndexForNumber(n int) (int, bool) {
	if n <= 0 {
		return 0, false
	}
	count := 0
	for i, row := range settingsRows() {
		if row.kind == "section" {
			continue
		}
		count++
		if count == n {
			return i, true
		}
	}
	return 0, false
}

func (m *Model) commitSettingsPick() {
	rows := settingsRows()
	if m.settingsCursor < 0 || m.settingsCursor >= len(rows) || m.saveSettings == nil {
		return
	}
	row := rows[m.settingsCursor]
	next := projectx.Settings{
		BashPermissionMode: m.bashMode,
		ReasoningEffort:    m.reasoningEffort,
	}
	switch {
	case strings.HasPrefix(row.kind, "bash:"):
		next.BashPermissionMode = projectx.BashPermissionMode(strings.TrimPrefix(row.kind, "bash:"))
	case strings.HasPrefix(row.kind, "reasoning:"):
		next.ReasoningEffort = strings.TrimPrefix(row.kind, "reasoning:")
	default:
		return
	}
	if err := m.saveSettings(next); err != nil {
		m.appendLine(styleErr.Render("settings save failed: " + err.Error()))
		return
	}
	m.bashMode = next.EffectiveBashMode()
	m.reasoningEffort = next.EffectiveReasoningEffort()
	if m.reasoningEffort == "" {
		m.appendLine(styleHelp.Render("(settings: bash=" + string(m.bashMode) + " reasoning=auto)"))
	} else {
		m.appendLine(styleHelp.Render("(settings: bash=" + string(m.bashMode) + " reasoning=" + m.reasoningEffort + ")"))
	}
}

func (m *Model) renderSettings() string {
	var b strings.Builder
	b.WriteString(headerStyle.Render("Settings"))
	b.WriteString("\n\n")
	b.WriteString(styleHelp.Render("Persists to project.json."))
	b.WriteString("\n\n")
	num := 0
	for i, r := range settingsRows() {
		if r.kind == "section" {
			b.WriteString(headerStyle.Render(r.title))
			b.WriteString("\n")
			continue
		}
		num++
		marker := "  "
		label := fmt.Sprintf("%d.", num)
		title := r.title
		check := ""
		if strings.HasPrefix(r.kind, "bash:") && strings.TrimPrefix(r.kind, "bash:") == string(m.bashMode) {
			check = " ✓"
		}
		if strings.HasPrefix(r.kind, "reasoning:") && strings.TrimPrefix(r.kind, "reasoning:") == m.reasoningEffort {
			check = " ✓"
		}
		if i == m.settingsCursor {
			marker = stylePromptArrow.Render("› ")
			label = stylePaletteActive.Render(label)
			title = stylePaletteActive.Render(r.title + check)
		} else {
			title = r.title + check
		}
		fmt.Fprintf(&b, "%s%s %s\n", marker, label, title)
		fmt.Fprintf(&b, "     %s\n", styleHelp.Render(r.desc))
	}
	b.WriteString("\n")
	b.WriteString(styleHelp.Render("Enter to set · Esc to close · ↑/↓ select · 1-7 shortcut"))
	b.WriteString("\n")
	return b.String()
}

// renderApproval draws the bash-confirmation panel.
func (m *Model) renderApproval() string {
	r := m.approvalReq
	if r == nil {
		return ""
	}
	m.clampApprovalScroll()
	lines := m.approvalContentLines()
	bodyRows := m.approvalBodyRows()
	start := m.approvalScroll
	end := start + bodyRows
	if end > len(lines) {
		end = len(lines)
	}
	var b strings.Builder
	b.WriteString(headerStyle.Render("Bash approval required"))
	b.WriteString("\n")
	if len(lines) > bodyRows {
		fmt.Fprintf(&b, "%s\n", styleHelp.Render(fmt.Sprintf("Details %d-%d/%d · PgUp/PgDn scroll", start+1, end, len(lines))))
	} else {
		b.WriteString(styleHelp.Render("Review the command before approving."))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	for _, line := range lines[start:end] {
		b.WriteString(line)
		b.WriteString("\n")
	}
	for i := end - start; i < bodyRows; i++ {
		b.WriteString("\n")
	}
	b.WriteString("\nDo you want to proceed?\n")
	for i, opt := range m.approvalOptions() {
		marker := "  "
		num := fmt.Sprintf("%d.", i+1)
		title := opt
		if i == m.approvalCursor {
			marker = stylePromptArrow.Render("› ")
			num = stylePaletteActive.Render(num)
			title = stylePaletteActive.Render(opt)
		}
		fmt.Fprintf(&b, "%s%s %s\n", marker, num, title)
	}
	b.WriteString("\n")
	b.WriteString(styleHelp.Render("Enter confirm · Esc deny · 1/2/3 shortcut · PgUp/PgDn details"))
	b.WriteString("\n")
	return b.String()
}
