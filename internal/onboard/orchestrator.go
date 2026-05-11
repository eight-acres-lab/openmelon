package onboard

// orchestrator.go — single bubbletea Program that hosts every onboarding
// wizard in sequence. Replaces the previous N-separate-Programs design,
// which left each wizard's output on screen because none used alt-screen
// and there was no way to reset between Programs.
//
// One Program → one alt-screen → smooth transitions: each wizard
// renders full-screen, the user picks, the screen updates in place to
// the next wizard. No flash, no scroll.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/eight-acres-lab/openmelon/internal/projectx"
	"github.com/eight-acres-lab/openmelon/internal/userconfig"
)

// State enumerates the ordered wizard screens. Init() picks the first
// state whose precondition is unmet; transitions advance to the next
// applicable state until StateDone.
type State int

const (
	stateTrust State = iota
	stateAuthProvider
	stateAuthReuseEnv
	stateAuthKey
	stateAuthLLMModel
	stateAuthImageModel
	stateProjectConfirm
	stateProjectID
	stateProjectName
	stateProjectDesc
	stateDone
)

// Result is what the orchestrator returns from Run.
type Result struct {
	// Workdir is the project root (existing or freshly initialized).
	// Empty when Quit is true.
	Workdir string

	// Quit is true when the user explicitly cancelled at any step. The
	// caller should exit cleanly with no error.
	Quit bool
}

// orchestrator hosts one wizard at a time and steps through the state
// machine. It implements tea.Model so it can be passed to tea.NewProgram.
type orchestrator struct {
	state  State
	cwd    string
	inner  tea.Model
	width  int
	height int

	// preconditions cached on Init.
	cfg         *userconfig.Config
	creds       *userconfig.Credentials
	skipTrust   bool
	skipAuth    bool
	skipProject bool

	// accumulated answers carried forward.
	chosenProvider int
	envKey         string // set when we detect an env var for the chosen provider
	apiKey         string
	llmModel       string
	imageModel     string

	projectID   string
	projectName string
	projectDesc string

	// workdir for the resulting project. Set in stateProjectConfirm
	// (existing project) or after stateProjectDesc (newly created).
	workdir string

	cancelled bool
}

// Run launches the orchestrator. Blocks until the user finishes or
// cancels. Returns the project workdir.
func Run() (Result, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return Result{}, fmt.Errorf("onboard: cwd: %w", err)
	}

	o := &orchestrator{cwd: cwd}
	if err := o.preflight(); err != nil {
		return Result{}, err
	}
	if o.skipTrust && o.skipAuth && o.skipProject {
		// All preconditions already met — nothing to do, no need to
		// even paint the alt-screen.
		return Result{Workdir: o.workdir}, nil
	}

	if _, err := tea.NewProgram(o, tea.WithAltScreen()).Run(); err != nil {
		return Result{}, err
	}
	if o.cancelled {
		return Result{Quit: true}, nil
	}
	return Result{Workdir: o.workdir}, nil
}

// preflight computes which states need to run + caches loaded state.
//
// Trust:    skipped if cwd is in cfg.TrustedDirs (or under one)
// Auth:     skipped if any global API key is configured
// Project:  skipped if cwd is in (or under) an existing project
func (o *orchestrator) preflight() error {
	cfg, err := userconfig.LoadConfig()
	if err != nil {
		return err
	}
	o.cfg = cfg
	o.skipTrust = cfg.IsTrusted(o.cwd)

	wd, err := projectx.Discover(o.cwd)
	if err != nil {
		return err
	}
	if wd != "" {
		o.skipProject = true
		o.workdir = wd
	}

	creds, err := userconfig.LoadCredentials()
	if err != nil {
		return err
	}
	o.creds = creds
	o.skipAuth = len(creds.APIKeys) > 0
	if !o.skipAuth && wd != "" {
		o.skipAuth = projectHasProviderConfig(wd)
	}
	return nil
}

func projectHasProviderConfig(workdir string) bool {
	p, err := projectx.Load(workdir)
	if err != nil {
		return false
	}
	provider := p.Defaults.LLMProvider
	model := p.Defaults.LLMModel
	if provider == "" || provider == "auto" || model == "" {
		return false
	}
	resolved := userconfig.ResolveProvider(workdir, provider)
	return resolved.APIKey != ""
}

// Init picks the first state that needs attention.
func (o *orchestrator) Init() tea.Cmd {
	o.state = o.firstState()
	if o.state == stateDone {
		return tea.Quit
	}
	o.inner = o.makeFor(o.state)
	if o.inner == nil {
		return tea.Quit
	}
	return o.inner.Init()
}

// firstState scans the preconditions and returns the first unmet one.
func (o *orchestrator) firstState() State {
	if !o.skipTrust {
		return stateTrust
	}
	if !o.skipAuth {
		return stateAuthProvider
	}
	if !o.skipProject {
		return stateProjectConfirm
	}
	return stateDone
}

// Update routes input messages to the active inner model. When inner
// emits a wizardDoneMsg, capture the answer + advance state.
func (o *orchestrator) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if w, ok := msg.(tea.WindowSizeMsg); ok {
		o.width, o.height = w.Width, w.Height
	}
	if d, ok := msg.(wizardDoneMsg); ok {
		if d.Cancelled {
			o.cancelled = true
			return o, tea.Quit
		}
		next, err := o.captureAndAdvance(d.Payload)
		if err != nil {
			fmt.Fprintf(os.Stderr, "onboard: %v\n", err)
			o.cancelled = true
			return o, tea.Quit
		}
		o.state = next
		if next == stateDone {
			return o, tea.Quit
		}
		o.inner = o.makeFor(next)
		if o.inner == nil {
			return o, tea.Quit
		}
		return o, o.inner.Init()
	}
	if o.inner == nil {
		return o, nil
	}
	var cmd tea.Cmd
	o.inner, cmd = o.inner.Update(msg)
	return o, cmd
}

func (o *orchestrator) View() string {
	if o.inner == nil {
		return ""
	}
	return o.inner.View()
}

// captureAndAdvance stores the wizard's answer + decides what state
// comes next. Returns stateDone when the orchestrator is finished.
func (o *orchestrator) captureAndAdvance(payload any) (State, error) {
	switch o.state {
	case stateTrust:
		idx, _ := payload.(int)
		if idx != 0 {
			o.cancelled = true
			return stateDone, nil
		}
		o.cfg.AddTrusted(o.cwd)
		if err := userconfig.SaveConfig(o.cfg); err != nil {
			return stateDone, fmt.Errorf("trust: save: %w", err)
		}
		// Re-evaluate next preconditions.
		if !o.skipAuth {
			return stateAuthProvider, nil
		}
		if !o.skipProject {
			return stateProjectConfirm, nil
		}
		return stateDone, nil

	case stateAuthProvider:
		idx, _ := payload.(int)
		o.chosenProvider = idx
		// If the env var for this provider is set, jump to "reuse?".
		envVar := providerOptions[idx].envVar
		if v := os.Getenv(envVar); v != "" {
			o.envKey = v
			return stateAuthReuseEnv, nil
		}
		return stateAuthKey, nil

	case stateAuthReuseEnv:
		idx, _ := payload.(int)
		if idx == 0 { // "Yes, use it"
			o.apiKey = o.envKey
			return stateAuthLLMModel, nil
		}
		return stateAuthKey, nil

	case stateAuthKey:
		s, _ := payload.(string)
		if s == "" {
			o.cancelled = true
			return stateDone, nil
		}
		o.apiKey = s
		return stateAuthLLMModel, nil

	case stateAuthLLMModel:
		s, _ := payload.(string)
		if s == "" {
			s = providerOptions[o.chosenProvider].defaultLLMModel
		}
		o.llmModel = s
		if providerOptions[o.chosenProvider].imgProvider != "" {
			return stateAuthImageModel, nil
		}
		// Anthropic etc. — no image step. Persist + advance.
		if err := o.persistAuth(); err != nil {
			return stateDone, err
		}
		if !o.skipProject {
			return stateProjectConfirm, nil
		}
		return stateDone, nil

	case stateAuthImageModel:
		o.imageModel, _ = payload.(string)
		if err := o.persistAuth(); err != nil {
			return stateDone, err
		}
		if !o.skipProject {
			return stateProjectConfirm, nil
		}
		return stateDone, nil

	case stateProjectConfirm:
		idx, _ := payload.(int)
		if idx != 0 {
			o.cancelled = true
			return stateDone, nil
		}
		return stateProjectID, nil

	case stateProjectID:
		s, _ := payload.(string)
		if s == "" {
			s = slugify(filepath.Base(o.cwd))
		}
		if err := projectx.ValidateID(s); err != nil {
			return stateDone, err
		}
		o.projectID = s
		return stateProjectName, nil

	case stateProjectName:
		s, _ := payload.(string)
		if s == "" {
			s = o.projectID
		}
		o.projectName = s
		return stateProjectDesc, nil

	case stateProjectDesc:
		o.projectDesc, _ = payload.(string)
		if err := o.persistProject(); err != nil {
			return stateDone, fmt.Errorf("project init: %w", err)
		}
		return stateDone, nil
	}
	return stateDone, nil
}

// persistAuth writes the chosen provider's key to credentials.json +
// the chosen models to config.json.defaults.
func (o *orchestrator) persistAuth() error {
	chosen := providerOptions[o.chosenProvider]
	if err := userconfig.SetAPIKey(chosen.slug, o.apiKey); err != nil {
		return fmt.Errorf("auth: save key: %w", err)
	}
	cfg, err := userconfig.LoadConfig()
	if err != nil {
		return err
	}
	cfg.Defaults.LLMProvider = chosen.slug
	cfg.Defaults.LLMModel = o.llmModel
	if o.imageModel != "" && chosen.imgProvider != "" {
		cfg.Defaults.ImageProvider = chosen.imgProvider
		cfg.Defaults.ImageModel = o.imageModel
	}
	if err := userconfig.SaveConfig(cfg); err != nil {
		return fmt.Errorf("auth: save config: %w", err)
	}
	o.cfg = cfg
	return nil
}

// persistProject creates the project on disk + registers it.
func (o *orchestrator) persistProject() error {
	p, err := projectx.Init(o.cwd, o.projectID, o.projectName)
	if err != nil {
		return err
	}
	if o.projectDesc != "" {
		p.Description = o.projectDesc
		if err := projectx.Save(o.cwd, p); err != nil {
			return err
		}
	}
	if err := userconfig.Register(o.projectID, o.projectName, o.cwd); err != nil {
		return err
	}
	if err := userconfig.SetCurrent(o.projectID); err != nil {
		return err
	}
	o.workdir = o.cwd
	return nil
}

// makeFor builds the inner Model for a given state.
func (o *orchestrator) makeFor(s State) tea.Model {
	switch s {
	case stateTrust:
		header := headerStyle.Render(fmt.Sprintf("> You are in %s", pathStyle.Render(o.cwd)))
		body := bodyStyle.Render(strings.Join([]string{
			"",
			"Do you trust the contents of this directory?",
			"openmelon will read project files, registered characters and references,",
			"and may invoke tools the agent decides to call. Only continue if you trust",
			"the contents here.",
		}, "\n"))
		return &listModel{
			opts: listOpts{
				Title: header + "\n" + body,
				Items: []ListItem{{Title: "Yes, continue"}, {Title: "No, quit"}},
				Help:  "↑/↓ to choose · 1/2 shortcut · enter to continue · ctrl+c to quit",
			},
			chosen: -1,
		}

	case stateAuthProvider:
		header := headerStyle.Render("Welcome to openmelon") + "\n\n" +
			bodyStyle.Render("openmelon needs an API key to talk to a model.\nPick one provider — you can change later with `openmelon setup`.")
		items := make([]ListItem, len(providerOptions))
		for i, p := range providerOptions {
			items[i] = ListItem{Title: p.title, Subtitle: p.subtitle}
		}
		return &listModel{
			opts: listOpts{
				Title: header,
				Items: items,
				Help:  "↑/↓ to choose · 1/2/3 shortcut · enter to continue · ctrl+c to cancel",
			},
			chosen: -1,
		}

	case stateAuthReuseEnv:
		p := providerOptions[o.chosenProvider]
		masked := o.envKey
		if len(masked) > 8 {
			masked = masked[:4] + "…" + masked[len(masked)-4:]
		}
		return &listModel{
			opts: listOpts{
				Title: headerStyle.Render(fmt.Sprintf("Detected %s in your environment", p.envVar)) + "\n\n" +
					bodyStyle.Render(fmt.Sprintf("Use %s as the %s key?", masked, p.title[len("Use "):])),
				Items: []ListItem{
					{Title: "Yes, use it"},
					{Title: "No, paste a different one"},
				},
				Help: "enter to continue · esc to cancel",
			},
			chosen: -1,
		}

	case stateAuthKey:
		return newKeyInputModel(providerOptions[o.chosenProvider])

	case stateAuthLLMModel:
		return newModelInputModel("LLM model", providerOptions[o.chosenProvider].defaultLLMModel)

	case stateAuthImageModel:
		return newModelInputModel(
			"Image model (leave blank to skip image generation)",
			providerOptions[o.chosenProvider].defaultImgModel,
		)

	case stateProjectConfirm:
		return &listModel{
			opts: listOpts{
				Title: headerStyle.Render(fmt.Sprintf("> No openmelon project found in %s", o.cwd)) + "\n\n" +
					bodyStyle.Render("Create one here? It just adds a `.openmelon/` directory with a project.json,\nplus subdirs for characters, references, materials, and sessions."),
				Items: []ListItem{
					{Title: "Yes, initialize a new project here"},
					{Title: "No, quit"},
				},
				Help: "enter to continue · esc to cancel",
			},
			chosen: -1,
		}

	case stateProjectID:
		def := slugify(filepath.Base(o.cwd))
		return newProjectField("Project id (kebab-case, the registry key)", def)

	case stateProjectName:
		return newProjectField("Project name (free text shown in the UI)", o.projectID)

	case stateProjectDesc:
		return newProjectField("One-line description (optional)", "")
	}
	return nil
}

// newProjectField is the constructor formerly inlined in
// runProjectField. Lives here so the orchestrator can build one without
// going through the standalone Run helper.
func newProjectField(prompt, def string) *projectFieldModel {
	ti := textinput.New()
	ti.Placeholder = def
	if def != "" {
		ti.SetValue(def)
	}
	ti.CharLimit = 200
	ti.Width = 60
	ti.Focus()
	return &projectFieldModel{prompt: prompt, input: ti}
}
