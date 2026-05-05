package main

// agent_runtime.go — wires the new tool-driven runtime to the `-p` CLI
// flag when invoked inside an openmelon project.
//
// Outside a project, cmd/openmelon/main.go falls back to the legacy
// one-shot agent that compiles a single skillplus package and produces
// a single image. Inside a project, this path takes over: it gives the
// model a full tool box (list_characters, get_character, search,
// generate_image with reference images, save_artifact, finish) and
// records every turn into a session directory under
// .openmelon/sessions/<id>/.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/eight-acres-lab/openmelon/internal/imagegen"
	"github.com/eight-acres-lab/openmelon/internal/llm"
	"github.com/eight-acres-lab/openmelon/internal/projectx"
	"github.com/eight-acres-lab/openmelon/internal/runtime"
	"github.com/eight-acres-lab/openmelon/internal/session"
	"github.com/eight-acres-lab/openmelon/internal/skillplus"
	"github.com/eight-acres-lab/openmelon/internal/tools"
	"github.com/eight-acres-lab/openmelon/internal/userconfig"
)

func runAgentInProject(ctx context.Context, opts agentOpts, workdir string) error {
	proj, err := projectx.Load(workdir)
	if err != nil {
		return fmt.Errorf("project: %w", err)
	}

	// Per-project defaults override per-CLI defaults override env.
	llmProvider := firstNonEmpty(opts.llmProvider, proj.Defaults.LLMProvider)
	llmModel := firstNonEmpty(opts.llmModel, proj.Defaults.LLMModel)
	imageProvider := firstNonEmpty(opts.imageProvider, proj.Defaults.ImageProvider)
	imageModel := firstNonEmpty(opts.imageModel, proj.Defaults.ImageModel)

	// Then user defaults if both per-project and per-CLI are empty.
	cfg, _ := userconfig.LoadConfig()
	if cfg != nil {
		llmProvider = firstNonEmpty(llmProvider, cfg.Defaults.LLMProvider)
		llmModel = firstNonEmpty(llmModel, cfg.Defaults.LLMModel)
		imageProvider = firstNonEmpty(imageProvider, cfg.Defaults.ImageProvider)
		imageModel = firstNonEmpty(imageModel, cfg.Defaults.ImageModel)
	}
	if llmProvider == "" {
		llmProvider = "auto"
	}
	if imageProvider == "" {
		imageProvider = "openrouter"
	}

	llmClient, err := llm.New(llmProvider, "", opts.llmBaseURL, llmModel)
	if err != nil {
		switch {
		case errors.Is(err, llm.ErrNoAPIKey):
			return fmt.Errorf("no API key for %s — set %s in your environment",
				llmProvider, envVarFor(llmProvider))
		case errors.Is(err, llm.ErrModelRequired):
			return fmt.Errorf("--llm-model is required (or set defaults.llm_model in project.json)")
		}
		return fmt.Errorf("init LLM: %w", err)
	}
	tc, ok := llmClient.(llm.ToolCaller)
	if !ok {
		return fmt.Errorf("provider %q does not support tool calls — use --llm openai or --llm openrouter", llmClient.Provider())
	}

	var imgGen imagegen.Generator
	if opts.imageEnabled {
		imgGen, err = imagegen.New(imageProvider, "", opts.imageBaseURL, imageModel)
		if err != nil {
			switch {
			case errors.Is(err, imagegen.ErrNoAPIKey):
				envHint := "OPENAI_API_KEY"
				if imageProvider == "openrouter" {
					envHint = "OPENROUTER_API_KEY"
				}
				return fmt.Errorf("image generation requires %s (or pass --image=false)", envHint)
			case errors.Is(err, imagegen.ErrModelRequired):
				return fmt.Errorf("--image-model is required (or set defaults.image_model in project.json)")
			}
			return fmt.Errorf("init image generator: %w", err)
		}
	}

	// Open a session.
	sess, err := session.New(workdir, proj.ID, opts.intent)
	if err != nil {
		return fmt.Errorf("session: %w", err)
	}
	defer sess.Close()

	// Build the tool registry around the project + session.
	reg := tools.NewRegistry()
	tools.RegisterAll(reg, &tools.Env{
		Workdir:    workdir,
		Project:    proj,
		SessionDir: sess.Dir,
		Compiler:   &skillplus.Compiler{CompilerPath: opts.compilerPath},
		ImageGen:   imgGen,
	})

	rt := &runtime.Runtime{
		LLM:      tc,
		Registry: reg,
		Trace:    os.Stderr,
		MaxSteps: 24,
	}

	systemPrompt := buildProjectSystemPrompt(proj, reg.Names())

	fmt.Fprintf(os.Stderr, "[openmelon] project=%s session=%s llm=%s/%s",
		proj.ID, sess.ID, llmClient.Provider(), llmClient.Model())
	if imgGen != nil {
		fmt.Fprintf(os.Stderr, " image=%s/%s", imgGen.Provider(), imgGen.Model())
	}
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "[openmelon] intent: %s\n", opts.intent)

	res, err := rt.Run(ctx, runtime.RunInput{
		SystemPrompt: systemPrompt,
		UserInput:    opts.intent,
	})
	if res != nil {
		_ = sess.AppendMessages(res.Messages)
		_ = sess.WriteSummary(res.FinishSummary, res.FinishArtifacts, res.Finished)
	}
	if err != nil {
		return err
	}
	if res.FinishSummary != "" {
		fmt.Fprintf(os.Stderr, "\n[openmelon] %s\n", res.FinishSummary)
	}
	for _, p := range res.FinishArtifacts {
		fmt.Fprintf(os.Stderr, "[openmelon] artifact: %s\n", p)
	}
	fmt.Fprintf(os.Stderr, "[openmelon] session: %s\n", sess.Dir)
	return nil
}

// buildProjectSystemPrompt assembles the project-context system prompt.
//
// Sent as the first system message of every run inside a project. Lists
// the available tools so the model knows what it can call without
// re-reading them from the wire schema (which it sees too — but humans
// debugging this benefit from a plain-language list as well).
func buildProjectSystemPrompt(p *projectx.Project, toolNames []string) string {
	var b strings.Builder
	b.WriteString("You are openmelon, a content-creation agent operating inside a creator's project.\n\n")
	fmt.Fprintf(&b, "Project: %s (%s)\n", p.Name, p.ID)
	if p.Description != "" {
		fmt.Fprintf(&b, "Description: %s\n", p.Description)
	}
	if p.Persona != "" {
		fmt.Fprintf(&b, "Voice / persona: %s\n", p.Persona)
	}
	if len(p.Constraints) > 0 {
		b.WriteString("House rules (must respect):\n")
		for _, c := range p.Constraints {
			fmt.Fprintf(&b, "  - %s\n", c)
		}
	}
	b.WriteString("\nWork like a senior creator: before generating, search the project for known characters / references that should appear, fetch their portraits, and pass those as reference_images to keep them visually consistent. When done, call `finish` with a short summary and the final artifact paths.\n")
	b.WriteString("\nAvailable tools: ")
	b.WriteString(strings.Join(toolNames, ", "))
	b.WriteString("\n")
	return b.String()
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
