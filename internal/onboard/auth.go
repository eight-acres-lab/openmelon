package onboard

// auth.go — provider + API key + model wizard.
//
// Single bubbletea program with three internal states:
//
//	stateProvider   — pick openrouter / openai / anthropic
//	stateKey        — masked API-key input (or "use detected env var")
//	stateLLMModel   — confirm / edit the LLM model id
//	stateImageModel — confirm / edit / skip image model id
//
// On finish: writes credentials.json + config.json defaults.

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/eight-acres-lab/openmelon/internal/userconfig"
)

// modelPreset is one curated model option offered in the model-picker
// step. Subtitles are short ("recommended", "cheap, fast", etc.) — the
// list is meant to fit in one screen.
type modelPreset struct {
	id       string
	subtitle string
}

// providerOption is one row in the provider menu.
type providerOption struct {
	slug            string // "openrouter" / "openai" / "anthropic"
	title           string
	subtitle        string
	envVar          string // OPENROUTER_API_KEY etc.
	defaultLLMModel string
	defaultImgModel string
	imgProvider     string // empty = no image support

	// llmPresets / imagePresets are the curated model options shown in
	// the model-picker steps. The orchestrator appends a "Custom" item
	// at the end so users can type their own id.
	llmPresets   []modelPreset
	imagePresets []modelPreset
}

var providerOptions = []providerOption{
	{
		slug: "openrouter", title: "Use OpenRouter",
		subtitle:        "Recommended. Routes to GPT, Claude, Gemini, Grok, etc. Has both LLM and image models.",
		envVar:          "OPENROUTER_API_KEY",
		defaultLLMModel: "openai/gpt-5.5",
		defaultImgModel: "google/gemini-2.5-flash-image",
		imgProvider:     "openrouter",
		// Top 10 text models on OR (May 2026), curated from
		// openrouter.ai/rankings + popularity. Custom row appended by
		// the selector UI.
		llmPresets: []modelPreset{
			{id: "openai/gpt-5.5", subtitle: "Recommended. OpenAI's latest unified agentic flagship (Apr 2026)."},
			{id: "anthropic/claude-opus-4.7", subtitle: "Anthropic's max model. Best for long-running agents."},
			{id: "anthropic/claude-sonnet-4.6", subtitle: "Balanced reasoning + speed. Highest non-OpenAI usage on OR."},
			{id: "google/gemini-3-flash-preview", subtitle: "Fast thinking, near-Pro reasoning, agentic workflows."},
			{id: "google/gemini-3-pro-preview", subtitle: "Google flagship. Best multimodal reasoning."},
			{id: "x-ai/grok-4.1-fast", subtitle: "Best agentic tool calling on OR. 2M context."},
			{id: "openai/gpt-5-mini", subtitle: "Cheap + fast OpenAI. Good for iteration."},
			{id: "anthropic/claude-haiku-4.5", subtitle: "Cheap + fast Claude."},
			{id: "google/gemini-2.5-pro", subtitle: "Long context fallback. Solid all-rounder."},
			{id: "x-ai/grok-4", subtitle: "Premium creative outputs."},
		},
		// Top 5 image models on OR (May 2026), curated from
		// openrouter.ai/collections/image-models. Mix of vendors.
		imagePresets: []modelPreset{
			{id: "google/gemini-2.5-flash-image", subtitle: "Recommended. Cheapest good model on OR (\"Nano Banana\")."},
			{id: "google/gemini-3-pro-image-preview", subtitle: "Premium quality, 2K/4K (\"Nano Banana Pro\")."},
			{id: "openai/gpt-5-image-mini", subtitle: "Balanced cost/quality. Strong text rendering."},
			{id: "openai/gpt-5.4-image-2", subtitle: "Premium OpenAI. GPT-5.4 reasoning + image."},
			{id: "black-forest-labs/flux.2-pro", subtitle: "Alt vendor. Strong prompt adherence, sharp textures, 4MP."},
		},
	},
	{
		slug: "openai", title: "Use OpenAI",
		subtitle:        "Direct OpenAI API. Has both chat and image.",
		envVar:          "OPENAI_API_KEY",
		defaultLLMModel: "gpt-5.5",
		defaultImgModel: "gpt-5.4-image-2",
		imgProvider:     "openai",
		llmPresets: []modelPreset{
			{id: "gpt-5.5", subtitle: "Recommended. Newest unified agentic flagship (Apr 2026)."},
			{id: "gpt-5", subtitle: "Previous flagship. Mature, well-tested."},
			{id: "gpt-5-mini", subtitle: "Cheaper / faster."},
			{id: "gpt-4o", subtitle: "Legacy multimodal."},
		},
		imagePresets: []modelPreset{
			{id: "gpt-5.4-image-2", subtitle: "Recommended. GPT-5.4 reasoning + image."},
			{id: "gpt-5-image", subtitle: "Premium quality."},
			{id: "gpt-5-image-mini", subtitle: "Balanced cost/quality."},
			{id: "gpt-image-1", subtitle: "Standard."},
			{id: "dall-e-3", subtitle: "Legacy."},
		},
	},
	{
		slug: "anthropic", title: "Use Anthropic",
		subtitle:        "Claude only. No image generation — pair with OpenAI/OpenRouter for images.",
		envVar:          "ANTHROPIC_API_KEY",
		defaultLLMModel: "claude-sonnet-4.6",
		llmPresets: []modelPreset{
			{id: "claude-opus-4.7", subtitle: "Recommended. Most capable. Best for complex / long-running agents."},
			{id: "claude-sonnet-4.6", subtitle: "Balanced reasoning + speed."},
			{id: "claude-haiku-4.5", subtitle: "Cheap, fast."},
		},
	},
}

// EnsureAuth runs the provider/key/model wizard if no auth is
// configured yet. Returns the chosen provider+key so the caller can
// pass them straight into llm.New.
//
// Skips silently when credentials.json already has at least one key.
func EnsureAuth() (configured bool, err error) {
	creds, err := userconfig.LoadCredentials()
	if err != nil {
		return false, err
	}
	if len(creds.APIKeys) > 0 {
		return true, nil
	}

	// Step 1: pick provider.
	header := headerStyle.Render("Welcome to openmelon") + "\n\n" +
		bodyStyle.Render("openmelon needs an API key to talk to a model.\nPick one provider — you can change later with `openmelon setup`.")
	items := make([]ListItem, len(providerOptions))
	for i, p := range providerOptions {
		items[i] = ListItem{Title: p.title, Subtitle: p.subtitle}
	}
	res, err := RunList(listOpts{
		Title: header,
		Items: items,
		Help:  "↑/↓ to choose · 1/2/3 shortcut · enter to continue · ctrl+c to quit",
	})
	if err != nil {
		return false, err
	}
	if res.Cancelled {
		return false, nil
	}
	chosen := providerOptions[res.Index]

	// Step 2: API key input. Detect env var first.
	envKey := os.Getenv(chosen.envVar)
	apiKey := envKey
	if envKey == "" {
		apiKey, err = runKeyInput(chosen)
		if err != nil {
			return false, err
		}
		if apiKey == "" {
			return false, nil
		}
	} else {
		// Confirm reuse.
		ok, err := runReuseEnvPrompt(chosen, envKey)
		if err != nil {
			return false, err
		}
		if !ok {
			apiKey, err = runKeyInput(chosen)
			if err != nil {
				return false, err
			}
			if apiKey == "" {
				return false, nil
			}
		}
	}

	// Step 3: confirm LLM model.
	llmModel, err := runModelInput("LLM model", chosen.defaultLLMModel)
	if err != nil {
		return false, err
	}
	if llmModel == "" {
		llmModel = chosen.defaultLLMModel
	}

	// Step 4: image model (or skip).
	var imageProvider, imageModel string
	if chosen.imgProvider != "" {
		imageModel, err = runModelInput("Image model (leave blank to skip image generation)", chosen.defaultImgModel)
		if err != nil {
			return false, err
		}
		if imageModel != "" {
			imageProvider = chosen.imgProvider
		}
	}

	// Persist.
	if err := userconfig.SetAPIKey(chosen.slug, apiKey); err != nil {
		return false, fmt.Errorf("auth: save key: %w", err)
	}
	cfg, err := userconfig.LoadConfig()
	if err != nil {
		return false, err
	}
	cfg.Defaults.LLMProvider = chosen.slug
	cfg.Defaults.LLMModel = llmModel
	cfg.Defaults.ImageProvider = imageProvider
	cfg.Defaults.ImageModel = imageModel
	if err := userconfig.SaveConfig(cfg); err != nil {
		return false, fmt.Errorf("auth: save config: %w", err)
	}
	return true, nil
}

// --- key input ---

type keyInputModel struct {
	provider providerOption
	input    textinput.Model
	done     bool
	cancel   bool
}

func newKeyInputModel(p providerOption) *keyInputModel {
	ti := textinput.New()
	ti.Placeholder = p.envVar
	ti.EchoMode = textinput.EchoPassword
	ti.EchoCharacter = '•'
	ti.CharLimit = 500
	ti.Width = 60
	ti.Focus()
	return &keyInputModel{provider: p, input: ti}
}

func (m *keyInputModel) Init() tea.Cmd { return textinput.Blink }

func (m *keyInputModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch {
		case key.Matches(k, key.NewBinding(key.WithKeys("ctrl+c", "esc"))):
			m.cancel = true
			return m, finishCancelled()
		case key.Matches(k, key.NewBinding(key.WithKeys("enter"))):
			val := strings.TrimSpace(m.input.Value())
			if val != "" {
				m.done = true
				return m, finishWith(val)
			}
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *keyInputModel) View() string {
	var b strings.Builder
	b.WriteString(headerStyle.Render(fmt.Sprintf("Paste your %s API key", m.provider.title[len("Use "):])))
	b.WriteString("\n\n")
	b.WriteString(bodyStyle.Render("It will be stored at ~/.openmelon/credentials.json (mode 0600)."))
	b.WriteString("\n\n")
	b.WriteString(m.input.View())
	b.WriteString("\n\n")
	b.WriteString(helpStyle.Render("enter to continue · esc to cancel"))
	b.WriteString("\n")
	return b.String()
}

func runKeyInput(p providerOption) (string, error) {
	m := newKeyInputModel(p)
	runner := &singleShotRunner{inner: m}
	if _, err := tea.NewProgram(runner, tea.WithAltScreen()).Run(); err != nil {
		return "", err
	}
	if runner.cancelled {
		return "", nil
	}
	if s, ok := runner.payload.(string); ok {
		return s, nil
	}
	return strings.TrimSpace(m.input.Value()), nil
}

// --- reuse-env prompt ---

func runReuseEnvPrompt(p providerOption, envKey string) (bool, error) {
	masked := envKey
	if len(masked) > 8 {
		masked = masked[:4] + "…" + masked[len(masked)-4:]
	}
	res, err := RunList(listOpts{
		Title: headerStyle.Render(fmt.Sprintf("Detected %s in your environment", p.envVar)) + "\n\n" +
			bodyStyle.Render(fmt.Sprintf("Use %s as the %s key?", masked, p.title[len("Use "):])),
		Items: []ListItem{
			{Title: "Yes, use it"},
			{Title: "No, paste a different one"},
		},
		Help: "enter to continue · esc to cancel",
	})
	if err != nil {
		return false, err
	}
	if res.Cancelled {
		return false, nil
	}
	return res.Index == 0, nil
}

// --- model id input ---

type modelInputModel struct {
	prompt  string
	defVal  string
	input   textinput.Model
	done    bool
	cancel  bool
	cleared bool // true if user explicitly cleared (returned empty)
}

func newModelInputModel(prompt, def string) *modelInputModel {
	ti := textinput.New()
	ti.Placeholder = def
	ti.SetValue(def)
	ti.CharLimit = 200
	ti.Width = 60
	ti.Focus()
	return &modelInputModel{prompt: prompt, defVal: def, input: ti}
}

func (m *modelInputModel) Init() tea.Cmd { return textinput.Blink }

func (m *modelInputModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch {
		case key.Matches(k, key.NewBinding(key.WithKeys("ctrl+c", "esc"))):
			m.cancel = true
			return m, finishCancelled()
		case key.Matches(k, key.NewBinding(key.WithKeys("enter"))):
			m.done = true
			return m, finishWith(strings.TrimSpace(m.input.Value()))
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *modelInputModel) View() string {
	var b strings.Builder
	b.WriteString(headerStyle.Render(m.prompt))
	b.WriteString("\n\n")
	b.WriteString(bodyStyle.Render(fmt.Sprintf("Default: %s. Enter to accept, or edit the line.", m.defVal)))
	b.WriteString("\n\n")
	b.WriteString(m.input.View())
	b.WriteString("\n\n")
	b.WriteString(helpStyle.Render("enter to continue · esc to cancel"))
	b.WriteString("\n")
	return b.String()
}

func runModelInput(prompt, def string) (string, error) {
	m := newModelInputModel(prompt, def)
	runner := &singleShotRunner{inner: m}
	if _, err := tea.NewProgram(runner, tea.WithAltScreen()).Run(); err != nil {
		return "", err
	}
	if runner.cancelled {
		return "", nil
	}
	if s, ok := runner.payload.(string); ok {
		return s, nil
	}
	return strings.TrimSpace(m.input.Value()), nil
}
