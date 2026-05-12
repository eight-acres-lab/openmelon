package main

// cmd_repl.go — `openmelon repl` (and the no-args entry inside a
// project) launches the interactive read-eval-print loop.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/eight-acres-lab/openmelon/internal/hooks"
	"github.com/eight-acres-lab/openmelon/internal/imagegen"
	"github.com/eight-acres-lab/openmelon/internal/llm"
	"github.com/eight-acres-lab/openmelon/internal/onboard"
	"github.com/eight-acres-lab/openmelon/internal/projectx"
	"github.com/eight-acres-lab/openmelon/internal/repl"
	"github.com/eight-acres-lab/openmelon/internal/runtime"
	"github.com/eight-acres-lab/openmelon/internal/session"
	"github.com/eight-acres-lab/openmelon/internal/skillplus"
	"github.com/eight-acres-lab/openmelon/internal/tools"
	"github.com/eight-acres-lab/openmelon/internal/tui"
	"github.com/eight-acres-lab/openmelon/internal/userconfig"
)

func runRepl(_ []string) error {
	// Onboarding: trust → auth → project init. Each step is a no-op
	// when its precondition is already met.
	res, err := onboard.Ensure()
	if err != nil {
		return err
	}
	if res.Quit {
		return nil
	}
	wd := res.Workdir
	proj, err := projectx.Load(wd)
	if err != nil {
		return err
	}
	// Best-effort retrofit of the .gitignore on existing projects so
	// credentials.json and sessions/ are never accidentally committed.
	// Non-fatal — a failure here shouldn't block the user from working.
	if err := projectx.EnsureGitignore(wd); err != nil {
		fmt.Fprintf(os.Stderr, "openmelon: warning: could not write .gitignore: %v\n", err)
	}

	llmProvider, llmModel, imageProvider, imageModel := resolveDefaults(proj)
	if llmProvider == "" {
		llmProvider = "auto"
	}
	if imageProvider == "" {
		imageProvider = "openrouter"
	}
	// Resolve provider config with project-overrides-global semantics.
	apiKey := ""
	llmBaseURL := ""
	if llmProvider != "auto" {
		resolved := userconfig.ResolveProvider(wd, llmProvider)
		apiKey = resolved.APIKey
		llmBaseURL = resolved.BaseURL
	}

	llmClient, err := llm.New(llmProvider, apiKey, llmBaseURL, llmModel)
	if err != nil {
		switch {
		case errors.Is(err, llm.ErrNoAPIKey):
			return fmt.Errorf("no API key for %s — run `openmelon setup` to configure", llmProvider)
		case errors.Is(err, llm.ErrModelRequired):
			return fmt.Errorf("no LLM model — run `openmelon setup` to configure")
		}
		return fmt.Errorf("init LLM: %w", err)
	}
	tc, ok := llmClient.(llm.ToolCaller)
	if !ok {
		return fmt.Errorf("provider %q does not support tool calls — switch to --llm openai or --llm openrouter, or set defaults.llm_provider in project.json", llmClient.Provider())
	}
	llmProvider = llmClient.Provider()
	llmModel = llmClient.Model()

	var imgGen imagegen.Generator
	if imageModel != "" {
		imgResolved := userconfig.ResolveProvider(wd, imageProvider)
		imgGen, err = imagegen.New(imageProvider, imgResolved.APIKey, imgResolved.BaseURL, imageModel)
		if err != nil {
			fmt.Fprintf(os.Stderr, "openmelon: image generation disabled (%v)\n", err)
		}
	}

	rt := &runtime.Runtime{
		LLM:             tc,
		MaxSteps:        24,
		ReasoningEffort: resolveReasoningEffort(proj, llmProvider, llmModel),
	}

	// rebuildToolsEnv composes a tools.Env from the current state and
	// installs a fresh tools.Registry on rt. Called from WireSession
	// (initial wire-up after the TUI creates the session) AND from
	// the /model-image hot-swap closure below — both need the same
	// "compose env, register, assign" sequence with whatever the
	// latest imgGen + sessionDir are.
	//
	// approveHolder.fn is what tools.Env.Approve indirects through.
	// allowedBinaries is the per-session "yes-always" set; both fields
	// survive wireSession + /model-image rebuilds because env captures
	// the holder by pointer.
	var sessionDir string
	var sessionOutputDir string
	approveHolder := &struct {
		fn              func(req tools.ApprovalRequest) tools.ApprovalDecision
		allowedBinaries map[string]bool
	}{allowedBinaries: map[string]bool{}}
	rebuildToolsEnv := func() {
		reg := tools.NewRegistry()
		tools.RegisterAll(reg, &tools.Env{
			Workdir:    wd,
			Project:    proj,
			SessionDir: sessionDir,
			OutputDir:  sessionOutputDir,
			Compiler:   &skillplus.Compiler{},
			ImageGen:   imgGen,
			Approve: func(req tools.ApprovalRequest) tools.ApprovalDecision {
				if approveHolder.fn == nil {
					return tools.ApprovalDecision{}
				}
				return approveHolder.fn(req)
			},
			JudgeBash: tools.JudgeBashWithLLM(rt.LLM),
			IsBashAllowed: func(binary string) bool {
				return approveHolder.allowedBinaries[binary]
			},
			AllowBash: func(binary string) {
				approveHolder.allowedBinaries[binary] = true
			},
			BashMode: string(proj.Settings.EffectiveBashMode()),
			Hooks:    rt.Hooks,
		})
		rt.Registry = reg
	}
	wireSession := func(sd string) {
		sessionDir = sd
		sessionOutputDir = projectx.SessionOutputDir(wd, filepath.Base(sd))
		if rt.Hooks == nil {
			rt.Hooks = hooks.NoopManager{}
		}
		rebuildToolsEnv()
	}

	// Build a placeholder registry just to compute tool names for the
	// system prompt; the real registry is rebuilt with SessionDir
	// inside WireSession.
	probe := tools.NewRegistry()
	tools.RegisterAll(probe, &tools.Env{
		Workdir:   wd,
		Project:   proj,
		OutputDir: projectx.OutputDir(wd),
		Compiler:  &skillplus.Compiler{},
		ImageGen:  imgGen,
	})
	systemPrompt := buildProjectSystemPrompt(proj, probe.Names())

	// Resume support: if `openmelon resume <id>` set resumeID, load
	// that session's transcript so the new TUI starts pre-populated.
	var resumedHistory []llm.Message
	if resumeID != "" {
		h, err := session.LoadHistory(wd, resumeID)
		if err != nil {
			return fmt.Errorf("resume: %w", err)
		}
		resumedHistory = h
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	intent := fmt.Sprintf("interactive REPL %s", time.Now().UTC().Format("2006-01-02 15:04"))
	if resumeID != "" {
		intent = fmt.Sprintf("resumed from %s · %s", resumeID, intent)
	}

	llmTag := fmt.Sprintf("%s:%s", llmClient.Provider(), llmClient.Model())
	imageTag := ""
	if imgGen != nil {
		imageTag = fmt.Sprintf("%s:%s", imgGen.Provider(), imgGen.Model())
	}
	// Hot-swap closures used by /model and /model-image. They rebuild
	// the clients against the same provider config, swap them into the
	// runtime, and persist the project defaults.
	rebuildLLM := func(modelID string) (string, error) {
		resolved := userconfig.ResolveProvider(wd, llmProvider)
		c, err := llm.New(llmProvider, resolved.APIKey, resolved.BaseURL, modelID)
		if err != nil {
			return "", err
		}
		tc, ok := c.(llm.ToolCaller)
		if !ok {
			return "", fmt.Errorf("provider %q does not support tool calls", llmProvider)
		}
		rt.LLM = tc
		rt.ReasoningEffort = resolveReasoningEffort(proj, llmProvider, modelID)
		proj.Defaults.LLMProvider = llmProvider
		proj.Defaults.LLMModel = modelID
		if err := projectx.Save(wd, proj); err != nil {
			return "", err
		}
		return fmt.Sprintf("%s:%s", llmProvider, modelID), nil
	}
	rebuildImageModel := func(provider, modelID string) (string, error) {
		if provider == "" || modelID == "" {
			imgGen = nil
			rebuildToolsEnv()
			proj.Defaults.ImageProvider = ""
			proj.Defaults.ImageModel = ""
			if err := projectx.Save(wd, proj); err != nil {
				return "", err
			}
			return "", nil
		}
		resolved := userconfig.ResolveProvider(wd, provider)
		g, err := imagegen.New(provider, resolved.APIKey, resolved.BaseURL, modelID)
		if err != nil {
			return "", err
		}
		imgGen = g
		rebuildToolsEnv()
		proj.Defaults.ImageProvider = provider
		proj.Defaults.ImageModel = modelID
		if err := projectx.Save(wd, proj); err != nil {
			return "", err
		}
		return fmt.Sprintf("%s:%s", provider, modelID), nil
	}
	saveSettings := func(s projectx.Settings) error {
		proj.Settings = s
		rt.ReasoningEffort = s.EffectiveReasoningEffort()
		if rt.ReasoningEffort == "" {
			rt.ReasoningEffort = defaultReasoningEffort(llmProvider, llmModel)
		}
		if err := projectx.Save(wd, proj); err != nil {
			return err
		}
		rebuildToolsEnv()
		return nil
	}

	useFullTUI := envBool("OPENMELON_TUI") && term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
	if useFullTUI {
		return tui.Run(ctx, tui.Options{
			Workdir:           wd,
			Project:           proj,
			Runtime:           rt,
			WireSession:       wireSession,
			SystemPrompt:      systemPrompt,
			SessionIntent:     intent,
			ResumedFrom:       resumeID,
			InitialHistory:    resumedHistory,
			LLMTag:            llmTag,
			ImageTag:          imageTag,
			Provider:          llmProvider,
			ImageProvider:     imageProvider,
			LLMModel:          llmModel,
			ImageModel:        imageModel,
			RebuildLLM:        rebuildLLM,
			RebuildImageModel: rebuildImageModel,
			InstallApprove: func(approve func(req tools.ApprovalRequest) tools.ApprovalDecision) {
				approveHolder.fn = approve
			},
			BashMode:        proj.Settings.EffectiveBashMode(),
			ReasoningEffort: rt.ReasoningEffort,
			SaveSettings:    saveSettings,
		})
	}

	return repl.Run(ctx, repl.Options{
		Workdir:           wd,
		Project:           proj,
		Runtime:           rt,
		WireSession:       wireSession,
		SystemPrompt:      systemPrompt,
		SessionIntent:     intent,
		ResumedFrom:       resumeID,
		InitialHistory:    resumedHistory,
		Provider:          llmProvider,
		Model:             llmModel,
		ModelTag:          llmTag,
		ImageProvider:     imageProvider,
		ImageModel:        imageModel,
		ImageTag:          imageTag,
		RebuildLLM:        rebuildLLM,
		RebuildImageModel: rebuildImageModel,
		BashMode:          proj.Settings.EffectiveBashMode(),
		ReasoningEffort:   rt.ReasoningEffort,
		SaveSettings:      saveSettings,
		InstallApprove: func(approve func(req tools.ApprovalRequest) tools.ApprovalDecision) {
			approveHolder.fn = approve
		},
	})
}

func envBool(name string) bool {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return false
	}
	if b, err := strconv.ParseBool(raw); err == nil {
		return b
	}
	switch strings.ToLower(raw) {
	case "1", "yes", "y", "on":
		return true
	default:
		return false
	}
}

// resolveDefaults reads model + provider preferences from the project,
// falling back to ~/.openmelon/config.json.
func resolveDefaults(p *projectx.Project) (llmProvider, llmModel, imageProvider, imageModel string) {
	llmProvider = p.Defaults.LLMProvider
	llmModel = p.Defaults.LLMModel
	imageProvider = p.Defaults.ImageProvider
	imageModel = p.Defaults.ImageModel
	if cfg, _ := userconfig.LoadConfig(); cfg != nil {
		if llmProvider == "" {
			llmProvider = cfg.Defaults.LLMProvider
		}
		if llmModel == "" {
			llmModel = cfg.Defaults.LLMModel
		}
		if imageProvider == "" {
			imageProvider = cfg.Defaults.ImageProvider
		}
		if imageModel == "" {
			imageModel = cfg.Defaults.ImageModel
		}
	}
	return
}

func resolveReasoningEffort(p *projectx.Project, provider, model string) string {
	if p != nil {
		if effort := p.Settings.EffectiveReasoningEffort(); effort != "" {
			return effort
		}
	}
	if cfg, _ := userconfig.LoadConfig(); cfg != nil {
		if effort := normalizeReasoningEffort(cfg.Defaults.ReasoningEffort); effort != "" {
			return effort
		}
	}
	return defaultReasoningEffort(provider, model)
}

func normalizeReasoningEffort(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "none", "minimal", "low", "medium", "high", "xhigh":
		return strings.ToLower(strings.TrimSpace(v))
	default:
		return ""
	}
}

func defaultReasoningEffort(provider, model string) string {
	p := strings.ToLower(strings.TrimSpace(provider))
	m := strings.ToLower(strings.TrimSpace(model))
	if p != "openai" && p != "openrouter" {
		return ""
	}
	if strings.HasPrefix(m, "gpt-5") || strings.Contains(m, "/gpt-5") {
		return "xhigh"
	}
	return ""
}
