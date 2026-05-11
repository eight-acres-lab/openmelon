package main

// cmd_repl.go — `openmelon repl` (and the no-args entry inside a
// project) launches the interactive read-eval-print loop.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"golang.org/x/term"

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

	var imgGen imagegen.Generator
	if imageModel != "" {
		imgResolved := userconfig.ResolveProvider(wd, imageProvider)
		imgGen, err = imagegen.New(imageProvider, imgResolved.APIKey, imgResolved.BaseURL, imageModel)
		if err != nil {
			fmt.Fprintf(os.Stderr, "openmelon: image generation disabled (%v)\n", err)
		}
	}

	rt := &runtime.Runtime{
		LLM:      tc,
		MaxSteps: 24,
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
		})
		rt.Registry = reg
	}
	wireSession := func(sd string) {
		sessionDir = sd
		rebuildToolsEnv()
	}

	// Build a placeholder registry just to compute tool names for the
	// system prompt; the real registry is rebuilt with SessionDir
	// inside WireSession.
	probe := tools.NewRegistry()
	tools.RegisterAll(probe, &tools.Env{
		Workdir:  wd,
		Project:  proj,
		Compiler: &skillplus.Compiler{},
		ImageGen: imgGen,
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

	// Use the full TUI when stdin AND stdout are both real terminals.
	// Pipes / CI / scripted runs fall back to the bufio REPL — bubbletea
	// would crash trying to put stdin into raw mode.
	if term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd())) {
		llmTag := fmt.Sprintf("%s:%s", llmClient.Provider(), llmClient.Model())
		imageTag := ""
		if imgGen != nil {
			imageTag = fmt.Sprintf("%s:%s", imgGen.Provider(), imgGen.Model())
		}
		// Hot-swap closures used by /model and /model-image. They
		// rebuild the LLM / image client against the same provider +
		// API key, swap them into the runtime, and persist the new
		// model id into project.json.
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
			BashMode: proj.Settings.EffectiveBashMode(),
			SaveSettings: func(s projectx.Settings) error {
				proj.Settings = s
				if err := projectx.Save(wd, proj); err != nil {
					return err
				}
				// Rebuild tools env so the new mode takes effect this turn.
				rebuildToolsEnv()
				return nil
			},
		})
	}

	return repl.Run(ctx, repl.Options{
		Workdir:       wd,
		Project:       proj,
		Runtime:       rt,
		WireSession:   wireSession,
		SystemPrompt:  systemPrompt,
		SessionIntent: intent,
	})
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
