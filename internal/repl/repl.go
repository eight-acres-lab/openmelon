// Package repl is openmelon's terminal interaction loop.
//
// The default UI deliberately keeps transcript output in the normal
// terminal scrollback while using a small prompt-only Bubble Tea editor
// for the current input. That gives us proper Unicode editing, history,
// multiline input, slash completion, and Ctrl-C handling without owning
// the entire screen.
package repl

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	osc52 "github.com/aymanbagabas/go-osc52/v2"
	"github.com/eight-acres-lab/openmelon/internal/continuity"
	"github.com/eight-acres-lab/openmelon/internal/hooks"
	"github.com/eight-acres-lab/openmelon/internal/llm"
	"github.com/eight-acres-lab/openmelon/internal/onboard"
	"github.com/eight-acres-lab/openmelon/internal/projectx"
	"github.com/eight-acres-lab/openmelon/internal/runtime"
	"github.com/eight-acres-lab/openmelon/internal/session"
	"github.com/eight-acres-lab/openmelon/internal/skillplus"
	"github.com/eight-acres-lab/openmelon/internal/tools"
	"golang.org/x/term"
)

// Options configures a REPL.
type Options struct {
	// Workdir is the project root.
	Workdir string

	// Project is the loaded project config (used in the system prompt).
	Project *projectx.Project

	// Runtime carries the LLM. The REPL sets Tracer + (optionally)
	// rebuilds Registry via WireSession after creating the session.
	Runtime *runtime.Runtime

	// WireSession is called once after the REPL creates a session, with
	// the session directory. Implementations should rebuild any tools
	// that need to write into the session (most notably generate_image,
	// which writes images into <session>/) and assign the new registry
	// onto Runtime.Registry. Optional.
	WireSession func(sessionDir string)

	// SystemPrompt is sent on the first turn.
	SystemPrompt string

	// SessionIntent is recorded into the session's meta.json.
	SessionIntent string

	// ResumedFrom, when non-empty, records the prior session id in the
	// new session metadata.
	ResumedFrom string

	// InitialHistory seeds a resumed conversation.
	InitialHistory []llm.Message

	// Provider / Model are recorded in the session metadata and shown
	// in the startup banner.
	Provider string
	Model    string
	ModelTag string

	// Image provider/model are shown in status and used by /model-image.
	ImageProvider string
	ImageModel    string
	ImageTag      string

	// Hot-swap callbacks for slash commands. They rebuild the actual
	// clients in cmd/openmelon and persist the project defaults.
	RebuildLLM        func(model string) (string, error)
	RebuildImageModel func(provider, model string) (string, error)

	BashMode        projectx.BashPermissionMode
	ReasoningEffort string
	SaveSettings    func(projectx.Settings) error

	// InstallApprove wires a terminal approval prompt into tools.Env.
	InstallApprove func(approve func(req tools.ApprovalRequest) tools.ApprovalDecision)

	// In / Out / Err default to os.Stdin / os.Stdout / os.Stderr.
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

// slashCommand is one row in the prompt slash palette.
type slashCommand struct {
	name string
	help string
}

var slashCommands = []slashCommand{
	{"/help", "show commands"},
	{"/skill", "apply a skillplus package to the next message"},
	{"/model", "switch the LLM model"},
	{"/model-image", "switch or disable image generation"},
	{"/settings", "show or change project settings"},
	{"/copy", "copy the conversation transcript via OSC52"},
	{"/clear", "clear screen and forget conversation history"},
	{"/history", "print the message log so far"},
	{"/save", "write conversation to a jsonl file"},
	{"/session", "show the session directory"},
	{"/events", "show recent session lifecycle events"},
	{"/space", "show a creative space summary"},
	{"/compact", "print a compaction draft"},
	{"/exit", "exit"},
}

type app struct {
	opts Options
	sess *session.Session

	interactive bool
	scanner     *bufio.Scanner
	sigCh       chan os.Signal

	history       []llm.Message
	persistedUpTo int
	inputHistory  []string

	llmTag          string
	imageTag        string
	provider        string
	llmModel        string
	imageProvider   string
	imageModel      string
	bashMode        projectx.BashPermissionMode
	reasoningEffort string
	activeSkill     string
}

// Run enters the REPL. Returns when the user exits with /exit, EOF
// (Ctrl-D), or SIGTERM.
func Run(ctx context.Context, opts Options) error {
	if opts.In == nil {
		opts.In = os.Stdin
	}
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	if opts.Err == nil {
		opts.Err = os.Stderr
	}
	if opts.Runtime == nil {
		return errors.New("repl: Runtime is required")
	}
	if opts.Project == nil {
		return errors.New("repl: Project is required")
	}

	sess, err := session.NewResume(opts.Workdir, opts.Project.ID, opts.SessionIntent, opts.ResumedFrom)
	if err != nil {
		return fmt.Errorf("repl: session: %w", err)
	}
	defer sess.Close()
	_ = sess.SetRuntimeInfo(opts.Provider, opts.Model)
	opts.Runtime.Hooks = hooks.ChainManagers(opts.Runtime.Hooks, sess.HookRecorder())

	if opts.WireSession != nil {
		opts.WireSession(sess.Dir)
	}
	tr := newTerminalTracer(opts.Out)
	opts.Runtime.Tracer = tr

	a := &app{
		opts:            opts,
		sess:            sess,
		interactive:     isInteractive(opts.In, opts.Out),
		history:         append([]llm.Message(nil), opts.InitialHistory...),
		persistedUpTo:   len(opts.InitialHistory),
		llmTag:          firstNonEmpty(opts.ModelTag, composeModelTag(opts.Provider, opts.Model)),
		imageTag:        firstNonEmpty(opts.ImageTag, composeModelTag(opts.ImageProvider, opts.ImageModel)),
		provider:        opts.Provider,
		llmModel:        opts.Model,
		imageProvider:   opts.ImageProvider,
		imageModel:      opts.ImageModel,
		bashMode:        opts.BashMode,
		reasoningEffort: opts.ReasoningEffort,
		sigCh:           make(chan os.Signal, 4),
	}
	if !a.interactive {
		a.scanner = bufio.NewScanner(opts.In)
		a.scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	}
	if opts.InstallApprove != nil {
		if a.interactive {
			opts.InstallApprove(newApprovalPromptReader(opts.In, opts.Out))
		} else {
			opts.InstallApprove(newApprovalPrompt(a.scanner, opts.Out))
		}
	}
	signal.Notify(a.sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(a.sigCh)

	printBanner(opts.Out, a.bannerInfo())
	if len(a.history) > 0 {
		fmt.Fprintf(opts.Out, "%s\n", helpStyle.Render(fmt.Sprintf("loaded %d prior messages", len(a.history))))
		renderHistory(opts.Out, a.history)
	}

	for {
		line, ok, err := a.readLine(ctx)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(opts.Out)
			return nil
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		a.recordInput(line)
		if strings.HasPrefix(line, "/") {
			done, err := a.handleSlash(ctx, line)
			if err != nil {
				a.printError("openmelon", err.Error())
			}
			if done {
				return nil
			}
			continue
		}
		done, err := a.runTurn(ctx, line)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
	}
}

func (a *app) readLine(ctx context.Context) (string, bool, error) {
	if !a.interactive {
		fmt.Fprint(a.opts.Out, "\n> ")
		if !a.scanner.Scan() {
			if err := a.scanner.Err(); err != nil {
				return "", false, fmt.Errorf("repl: read input: %w", err)
			}
			return "", false, nil
		}
		return a.scanner.Text(), true, nil
	}
	res, err := readInteractivePrompt(ctx, promptConfig{
		In:          a.opts.In,
		Out:         a.opts.Out,
		History:     a.inputHistory,
		ActiveSkill: a.activeSkill,
		Commands:    slashCommands,
	})
	if err != nil {
		return "", false, fmt.Errorf("repl: prompt: %w", err)
	}
	fmt.Fprintln(a.opts.Out)
	switch res.outcome {
	case promptSubmit:
		return res.text, true, nil
	case promptExit:
		return "", false, nil
	case promptCancel:
		return "", true, nil
	default:
		return "", true, nil
	}
}

func (a *app) runTurn(ctx context.Context, line string) (bool, error) {
	_ = a.sess.AppendPrompt("user", line)
	userInput := a.applyActiveSkill(line)
	in := runtime.RunInput{UserInput: userInput}
	if len(a.history) == 0 {
		in.SystemPrompt = a.opts.SystemPrompt
	} else {
		in.History = a.history
	}

	fmt.Fprintf(a.opts.Out, "%s\n", helpStyle.Render("["+composePromptStatusLine(a.bannerInfo())+"]"))
	turnCtx, cancelTurn := context.WithCancel(ctx)
	defer cancelTurn()
	done := make(chan struct{})
	forceExit := make(chan struct{})
	go func() {
		interrupts := 0
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				cancelTurn()
				return
			case sig := <-a.sigCh:
				if sig == syscall.SIGTERM {
					cancelTurn()
					safeClose(forceExit)
					return
				}
				interrupts++
				cancelTurn()
				if interrupts == 1 {
					fmt.Fprintln(a.opts.Err)
					fmt.Fprintln(a.opts.Err, warnStyle.Render("[interrupting; press Ctrl+C again to quit]"))
					continue
				}
				safeClose(forceExit)
				return
			}
		}
	}()

	res, runErr := a.opts.Runtime.Run(turnCtx, in)
	close(done)
	cancelTurn()

	if res != nil {
		a.history = res.Messages
		if a.persistedUpTo < len(a.history) {
			_ = a.sess.AppendMessages(a.history[a.persistedUpTo:])
			a.persistedUpTo = len(a.history)
		}
	}
	if runErr != nil {
		if errors.Is(runErr, context.Canceled) {
			fmt.Fprintln(a.opts.Err, warnStyle.Render("[interrupted]"))
		} else {
			a.printError("openmelon", runErr.Error())
		}
	} else if res != nil && res.Finished {
		fmt.Fprintln(a.opts.Out, helpStyle.Render("[turn complete]"))
	}
	select {
	case <-forceExit:
		return true, nil
	default:
		return false, nil
	}
}

func (a *app) applyActiveSkill(text string) string {
	if a.activeSkill == "" {
		return text
	}
	skill := a.activeSkill
	a.activeSkill = ""
	return fmt.Sprintf(
		"Apply the skill %q to this request: first call compile_skill with skill=%q (BARE slug, no 'skillplus:' prefix) to fetch the package's prompt + output schema, then proceed.\n\n%s",
		skill, skill, text,
	)
}

func (a *app) recordInput(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if n := len(a.inputHistory); n > 0 && a.inputHistory[n-1] == text {
		return
	}
	a.inputHistory = append(a.inputHistory, text)
	if len(a.inputHistory) > 200 {
		a.inputHistory = a.inputHistory[len(a.inputHistory)-200:]
	}
}

func (a *app) handleSlash(ctx context.Context, line string) (bool, error) {
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return false, nil
	}
	cmd := parts[0]
	switch cmd {
	case "/exit", "/quit", "/q":
		fmt.Fprintln(a.opts.Out, "bye.")
		return true, nil
	case "/help", "/?":
		a.printHelp()
	case "/clear":
		a.history = nil
		a.persistedUpTo = 0
		if a.interactive {
			clearScreen(a.opts.Out)
			printBanner(a.opts.Out, a.bannerInfo())
		}
		fmt.Fprintln(a.opts.Out, helpStyle.Render("(history cleared)"))
	case "/history":
		if len(a.history) == 0 {
			fmt.Fprintln(a.opts.Out, helpStyle.Render("(no conversation history)"))
			break
		}
		renderHistory(a.opts.Out, a.history)
	case "/save":
		return false, a.saveHistory(parts)
	case "/session":
		fmt.Fprintln(a.opts.Out, a.sess.Dir)
	case "/events":
		return false, a.printEvents()
	case "/space":
		return false, a.printSpace(parts)
	case "/compact":
		return false, a.printCompact(parts)
	case "/copy":
		return false, a.copyTranscript()
	case "/model":
		return false, a.switchModel(parts)
	case "/model-image":
		return false, a.switchImageModel(parts)
	case "/settings", "/config":
		return false, a.handleSettings(parts)
	case "/skill":
		return false, a.handleSkill(ctx, parts)
	default:
		return false, fmt.Errorf("unknown command: %s (try /help)", cmd)
	}
	return false, nil
}

func (a *app) printHelp() {
	for _, c := range slashCommands {
		fmt.Fprintf(a.opts.Out, "  %-13s %s\n", c.name, helpStyle.Render(c.help))
	}
	fmt.Fprintln(a.opts.Out, helpStyle.Render("Input: Enter submit · Ctrl+J newline · ↑/↓ history · Tab completes slash commands · Ctrl+C twice quits."))
}

func (a *app) saveHistory(parts []string) error {
	if len(parts) < 2 {
		return errors.New("/save: usage: /save <path>")
	}
	f, err := os.Create(parts[1])
	if err != nil {
		return fmt.Errorf("/save: %w", err)
	}
	defer f.Close()
	enc := newJSONLEncoder(f)
	for _, m := range a.history {
		if err := enc.encode(m); err != nil {
			return err
		}
	}
	fmt.Fprintf(a.opts.Out, "saved %d messages -> %s\n", len(a.history), parts[1])
	return nil
}

func (a *app) printEvents() error {
	events, err := session.LoadEvents(a.opts.Workdir, a.sess.ID, 20)
	if err != nil {
		return fmt.Errorf("/events: %w", err)
	}
	if len(events) == 0 {
		fmt.Fprintln(a.opts.Out, helpStyle.Render("(no events recorded yet)"))
		return nil
	}
	for _, e := range events {
		a.printWrapped(fmt.Sprintf("%s step=%d tool=%s space=%s status=%s", e.Type, e.Step, e.Tool, e.SpaceID, e.Status), "  ")
	}
	return nil
}

func (a *app) printSpace(parts []string) error {
	if len(parts) != 2 {
		return errors.New("/space: usage: /space <id>")
	}
	p, err := continuity.BuildContextPacket(a.opts.Workdir, a.opts.Project.ID, parts[1])
	if err != nil {
		return fmt.Errorf("/space: %w", err)
	}
	a.printWrapped(fmt.Sprintf("%s (%s): %s", p.Space.ID, p.Space.Status, p.Space.Name), "")
	a.printWrapped(fmt.Sprintf("%d decisions · %d feedback · %d episodes · %d assets", len(p.RecentDecisions), len(p.RecentFeedback), len(p.RecentEpisodes), len(p.Assets)), "  ")
	return nil
}

func (a *app) printCompact(parts []string) error {
	if len(parts) != 2 {
		return errors.New("/compact: usage: /compact <space-id>")
	}
	body, err := continuity.BuildCompactionDraft(a.opts.Workdir, a.opts.Project.ID, parts[1])
	if err != nil {
		return fmt.Errorf("/compact: %w", err)
	}
	a.printWrapped(body, "")
	return nil
}

func (a *app) copyTranscript() error {
	text := plainTranscript(a.history)
	if strings.TrimSpace(text) == "" {
		return errors.New("/copy: nothing to copy")
	}
	seq := osc52.New(text)
	if os.Getenv("TMUX") != "" {
		seq = seq.Tmux()
	}
	if _, err := seq.WriteTo(a.opts.Err); err != nil {
		return fmt.Errorf("/copy: %w", err)
	}
	fmt.Fprintf(a.opts.Out, "%s\n", helpStyle.Render(fmt.Sprintf("copied transcript (%d chars)", len([]rune(text)))))
	return nil
}

func (a *app) switchModel(parts []string) error {
	if a.opts.RebuildLLM == nil {
		return errors.New("/model: model switching is not wired")
	}
	if len(parts) < 2 {
		a.printModelPresets(false)
		fmt.Fprintln(a.opts.Out, helpStyle.Render("usage: /model <model-id>"))
		return nil
	}
	model := strings.TrimSpace(parts[1])
	tag, err := a.opts.RebuildLLM(model)
	if err != nil {
		return fmt.Errorf("/model: %w", err)
	}
	a.llmModel = model
	a.llmTag = tag
	if tag == "" {
		a.llmTag = composeModelTag(a.provider, model)
	}
	fmt.Fprintln(a.opts.Out, helpStyle.Render("(LLM: "+a.llmTag+")"))
	return nil
}

func (a *app) switchImageModel(parts []string) error {
	if a.opts.RebuildImageModel == nil {
		return errors.New("/model-image: image model switching is not wired")
	}
	if len(parts) < 2 {
		a.printModelPresets(true)
		fmt.Fprintln(a.opts.Out, helpStyle.Render("usage: /model-image <model-id> | /model-image <provider> <model-id> | /model-image off"))
		return nil
	}
	if parts[1] == "off" || parts[1] == "disable" || parts[1] == "none" {
		tag, err := a.opts.RebuildImageModel("", "")
		if err != nil {
			return fmt.Errorf("/model-image: %w", err)
		}
		a.imageProvider, a.imageModel, a.imageTag = "", "", tag
		fmt.Fprintln(a.opts.Out, helpStyle.Render("(image generation disabled)"))
		return nil
	}
	provider := a.imageProvider
	model := parts[1]
	if len(parts) >= 3 {
		provider = parts[1]
		model = parts[2]
	}
	if provider == "" {
		provider = a.provider
	}
	tag, err := a.opts.RebuildImageModel(provider, model)
	if err != nil {
		return fmt.Errorf("/model-image: %w", err)
	}
	a.imageProvider, a.imageModel, a.imageTag = provider, model, tag
	if a.imageTag == "" {
		a.imageTag = composeModelTag(provider, model)
	}
	fmt.Fprintln(a.opts.Out, helpStyle.Render("(image model: "+a.imageTag+")"))
	return nil
}

func (a *app) printModelPresets(image bool) {
	provider := a.provider
	if image && a.imageProvider != "" {
		provider = a.imageProvider
	}
	info, ok := onboard.ProviderBySlug(provider)
	if !ok {
		fmt.Fprintf(a.opts.Out, "%s\n", helpStyle.Render("no presets for provider "+provider))
		return
	}
	presets := info.LLMPresets
	if image {
		presets = info.ImagePresets
	}
	for _, p := range presets {
		a.printWrapped(fmt.Sprintf("%s  %s", p.ID, p.Subtitle), "  ")
	}
}

func (a *app) handleSettings(parts []string) error {
	if len(parts) == 1 {
		fmt.Fprintf(a.opts.Out, "bash_permission_mode: %s\n", a.bashMode)
		if a.reasoningEffort == "" {
			fmt.Fprintln(a.opts.Out, "reasoning_effort: auto")
		} else {
			fmt.Fprintf(a.opts.Out, "reasoning_effort: %s\n", a.reasoningEffort)
		}
		fmt.Fprintln(a.opts.Out, helpStyle.Render("usage: /settings bash strict|auto|trusted"))
		fmt.Fprintln(a.opts.Out, helpStyle.Render("usage: /settings reasoning auto|medium|high|xhigh"))
		return nil
	}
	if a.opts.SaveSettings == nil {
		return errors.New("/settings: saving settings is not wired")
	}
	next := projectx.Settings{BashPermissionMode: a.bashMode, ReasoningEffort: a.reasoningEffort}
	switch parts[1] {
	case "bash":
		if len(parts) < 3 {
			return errors.New("/settings bash: expected strict|auto|trusted")
		}
		switch projectx.BashPermissionMode(parts[2]) {
		case projectx.BashModeStrict, projectx.BashModeAuto, projectx.BashModeTrusted:
			next.BashPermissionMode = projectx.BashPermissionMode(parts[2])
		default:
			return errors.New("/settings bash: expected strict|auto|trusted")
		}
	case "reasoning":
		if len(parts) < 3 {
			return errors.New("/settings reasoning: expected auto|medium|high|xhigh")
		}
		if parts[2] == "auto" {
			next.ReasoningEffort = ""
		} else {
			switch parts[2] {
			case "medium", "high", "xhigh":
				next.ReasoningEffort = parts[2]
			default:
				return errors.New("/settings reasoning: expected auto|medium|high|xhigh")
			}
		}
	default:
		return errors.New("/settings: expected bash or reasoning")
	}
	if err := a.opts.SaveSettings(next); err != nil {
		return fmt.Errorf("/settings: %w", err)
	}
	a.bashMode = next.EffectiveBashMode()
	a.reasoningEffort = next.EffectiveReasoningEffort()
	fmt.Fprintf(a.opts.Out, "%s\n", helpStyle.Render(fmt.Sprintf("(settings: bash=%s reasoning=%s)", a.bashMode, emptyAsAuto(a.reasoningEffort))))
	return nil
}

func (a *app) handleSkill(ctx context.Context, parts []string) error {
	if len(parts) == 1 {
		skills, err := skillplus.ListSkills(ctx)
		if err != nil {
			return fmt.Errorf("/skill: %w", err)
		}
		if len(skills) == 0 {
			fmt.Fprintln(a.opts.Out, helpStyle.Render("(no skillplus packages found)"))
			return nil
		}
		for _, s := range skills {
			a.printWrapped(fmt.Sprintf("%s  %s", s.ID, s.Description), "  ")
		}
		fmt.Fprintln(a.opts.Out, helpStyle.Render("usage: /skill <id> or /skill clear"))
		return nil
	}
	arg := parts[1]
	if arg == "clear" || arg == "off" || arg == "none" {
		if a.activeSkill == "" {
			fmt.Fprintln(a.opts.Out, helpStyle.Render("(no active skill)"))
		} else {
			fmt.Fprintln(a.opts.Out, helpStyle.Render("(skill cleared: "+a.activeSkill+")"))
			a.activeSkill = ""
		}
		return nil
	}
	a.activeSkill = arg
	fmt.Fprintln(a.opts.Out, helpStyle.Render("(skill: "+arg+") applies to your next message"))
	return nil
}

func (a *app) bannerInfo() bannerInfo {
	return bannerInfo{
		Workdir:         a.opts.Workdir,
		Project:         a.opts.Project,
		Session:         a.sess,
		ResumedFrom:     a.opts.ResumedFrom,
		LLMTag:          a.llmTag,
		ImageTag:        a.imageTag,
		BashMode:        a.bashMode,
		ReasoningEffort: a.reasoningEffort,
	}
}

func (a *app) printWrapped(text, prefix string) {
	if isTerminalWriter(a.opts.Out) {
		for _, line := range strings.Split(text, "\n") {
			fmt.Fprintln(a.opts.Out, prefix+line)
		}
		return
	}
	width := terminalWidth(a.opts.Out) - len(prefix)
	for _, line := range strings.Split(text, "\n") {
		for _, wrapped := range wrapDisplayLine(line, width) {
			fmt.Fprintln(a.opts.Out, prefix+wrapped)
		}
	}
}

func (a *app) printError(label, text string) {
	width := terminalWidth(a.opts.Err) - len(label) - 2
	first := true
	for _, line := range strings.Split(text, "\n") {
		for _, wrapped := range wrapDisplayLine(line, width) {
			if first {
				fmt.Fprintf(a.opts.Err, "%s: %s\n", errorStyle.Render(label), errorStyle.Render(wrapped))
				first = false
			} else {
				fmt.Fprintf(a.opts.Err, "%s  %s\n", strings.Repeat(" ", len(label)), errorStyle.Render(wrapped))
			}
		}
	}
}

func isInteractive(in io.Reader, out io.Writer) bool {
	inFile, ok := in.(*os.File)
	if !ok {
		return false
	}
	outFile, ok := out.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(inFile.Fd())) && term.IsTerminal(int(outFile.Fd()))
}

func composeModelTag(provider, model string) string {
	switch {
	case provider != "" && model != "":
		return provider + ":" + model
	case model != "":
		return model
	case provider != "":
		return provider
	default:
		return ""
	}
}

func emptyAsAuto(v string) string {
	if v == "" {
		return "auto"
	}
	return v
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func plainTranscript(history []llm.Message) string {
	var b strings.Builder
	for _, m := range history {
		switch m.Role {
		case llm.RoleSystem:
			continue
		case llm.RoleUser:
			fmt.Fprintf(&b, "> %s\n\n", m.Content)
		case llm.RoleAssistant:
			if strings.TrimSpace(m.Content) != "" {
				fmt.Fprintf(&b, "%s\n\n", strings.TrimSpace(m.Content))
			}
			for _, tc := range m.ToolCalls {
				fmt.Fprintf(&b, "tool %s(%s)\n", tc.Name, prettyArgs(tc.Arguments))
			}
		case llm.RoleTool:
			fmt.Fprintf(&b, "result %s\n\n", m.Content)
		}
	}
	return strings.TrimSpace(b.String())
}

func newApprovalPrompt(scanner *bufio.Scanner, out io.Writer) func(tools.ApprovalRequest) tools.ApprovalDecision {
	return func(req tools.ApprovalRequest) tools.ApprovalDecision {
		renderApprovalRequest(out, req)
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				fmt.Fprintf(out, "approval failed: %v\n", err)
			}
			return tools.ApprovalDecision{}
		}
		return parseApprovalAnswer(scanner.Text())
	}
}

func newApprovalPromptReader(in io.Reader, out io.Writer) func(tools.ApprovalRequest) tools.ApprovalDecision {
	reader := bufio.NewReader(in)
	return func(req tools.ApprovalRequest) tools.ApprovalDecision {
		renderApprovalRequest(out, req)
		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			fmt.Fprintf(out, "approval failed: %v\n", err)
			return tools.ApprovalDecision{}
		}
		return parseApprovalAnswer(line)
	}
}

func renderApprovalRequest(out io.Writer, req tools.ApprovalRequest) {
	fmt.Fprintln(out)
	fmt.Fprintln(out, warnStyle.Render("Do you want to proceed?"))
	if req.Description != "" {
		for _, line := range wrapDisplayLine(req.Description, terminalWidth(out)-10) {
			fmt.Fprintf(out, "  Reason:  %s\n", line)
		}
	}
	if req.Command != "" {
		first := true
		for _, line := range wrapDisplayLine(req.Command, terminalWidth(out)-11) {
			if first {
				fmt.Fprintf(out, "  Command: %s\n", line)
				first = false
			} else {
				fmt.Fprintf(out, "           %s\n", line)
			}
		}
	}
	if req.Binary != "" {
		fmt.Fprintf(out, "Approve? [y]es / [a]lways allow %s this session / [N]o: ", req.Binary)
	} else {
		fmt.Fprint(out, "Approve? [y]es / [N]o: ")
	}
	if f, ok := out.(*os.File); ok {
		_ = f.Sync()
	}
}

func parseApprovalAnswer(raw string) tools.ApprovalDecision {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "y", "yes":
		return tools.ApprovalDecision{Approved: true}
	case "a", "always":
		return tools.ApprovalDecision{Approved: true, Always: true}
	default:
		return tools.ApprovalDecision{}
	}
}

func safeClose(ch chan struct{}) {
	defer func() { _ = recover() }()
	close(ch)
}
