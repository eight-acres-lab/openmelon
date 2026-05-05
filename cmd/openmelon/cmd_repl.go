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
	// Resolve the API key with project-overrides-global semantics.
	apiKey := ""
	if llmProvider != "auto" {
		apiKey, _ = userconfig.ResolveAPIKey(wd, llmProvider)
	}

	llmClient, err := llm.New(llmProvider, apiKey, "", llmModel)
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
		imgKey, _ := userconfig.ResolveAPIKey(wd, imageProvider)
		imgGen, err = imagegen.New(imageProvider, imgKey, "", imageModel)
		if err != nil {
			fmt.Fprintf(os.Stderr, "openmelon: image generation disabled (%v)\n", err)
		}
	}

	rt := &runtime.Runtime{
		LLM:      tc,
		MaxSteps: 24,
	}

	wireSession := func(sessionDir string) {
		reg := tools.NewRegistry()
		tools.RegisterAll(reg, &tools.Env{
			Workdir:    wd,
			Project:    proj,
			SessionDir: sessionDir,
			Compiler:   &skillplus.Compiler{},
			ImageGen:   imgGen,
		})
		rt.Registry = reg
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	intent := fmt.Sprintf("interactive REPL %s", time.Now().UTC().Format("2006-01-02 15:04"))

	// Use the full TUI when stdin AND stdout are both real terminals.
	// Pipes / CI / scripted runs fall back to the bufio REPL — bubbletea
	// would crash trying to put stdin into raw mode.
	if term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd())) {
		llmTag := fmt.Sprintf("%s:%s", llmClient.Provider(), llmClient.Model())
		imageTag := ""
		if imgGen != nil {
			imageTag = fmt.Sprintf("%s:%s", imgGen.Provider(), imgGen.Model())
		}
		return tui.Run(ctx, tui.Options{
			Workdir:       wd,
			Project:       proj,
			Runtime:       rt,
			WireSession:   wireSession,
			SystemPrompt:  systemPrompt,
			SessionIntent: intent,
			LLMTag:        llmTag,
			ImageTag:      imageTag,
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
