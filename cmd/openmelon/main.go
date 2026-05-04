package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/eight-acres-lab/openmelon/internal/agent"
	"github.com/eight-acres-lab/openmelon/internal/generation"
	"github.com/eight-acres-lab/openmelon/internal/imagegen"
	"github.com/eight-acres-lab/openmelon/internal/llm"
	"github.com/eight-acres-lab/openmelon/internal/project"
	"github.com/eight-acres-lab/openmelon/internal/skillplus"
	"github.com/eight-acres-lab/openmelon/internal/workflow"
)

// openmelon dispatches between two execution modes:
//
//   - Agent (the new primary, 0.2): triggered by -p "<intent>". Runs the
//     one-shot agent loop — compile a Skill-Plus package, send to LLM,
//     optionally generate image, save artifacts + provenance.
//
//   - Workflow (the legacy 0.1 entry, kept for backward compatibility):
//     triggered by --project <project.json>. Runs the declarative
//     workflow engine.
//
// Future modes (REPL, MCP server, HTTP serve) become subcommands once
// the surface stabilizes.

func main() {
	fs := flag.NewFlagSet("openmelon", flag.ExitOnError)

	// Agent-mode flags (0.2).
	prompt := fs.String("p", "", "One-shot intent (triggers agent mode)")
	skillSpec := fs.String("skill", "skillplus:food-street-realism", "Skill spec: skillplus:<name>, path:<dir>, or a bare path")
	llmProvider := fs.String("llm", "auto", "LLM provider for prompt structuring (auto|anthropic|openai|openrouter). 'auto' picks based on which *_API_KEY is set, preferring Anthropic.")
	llmModel := fs.String("llm-model", "", "Override LLM default model")
	llmBaseURL := fs.String("llm-base-url", "", "Override LLM base URL — useful for proxies / relays. Default reads OPENAI_BASE_URL or OPENROUTER_BASE_URL env per provider.")
	imgEnabled := fs.Bool("image", true, "Generate an image from the structured generation_prompt")
	imgModel := fs.String("image-model", "", "Override image generator default model (gpt-image-1 / etc.)")
	imgBaseURL := fs.String("image-base-url", "", "Override OpenAI image API base URL — useful for relays. Default reads OPENAI_BASE_URL env.")
	publish := fs.String("publish", "", "Publish the result after generation: vbox (requires vbox-cli on PATH and VBOX_API_KEY)")
	postText := fs.String("post-text", "", "Override post text when publishing (default: the user's intent)")
	skillRoot := fs.String("skill-root", "", "Directory under which skillplus:<name> resolves to <root>/examples/<name>.skillplus (also: $SKILLPLUS_EXAMPLES_ROOT)")

	// Workflow-mode flags (0.1, legacy).
	projectFlag := fs.String("project", "", "Path to project.json (workflow mode)")
	workflowFlag := fs.String("workflow", "", "Workflow ID (workflow mode)")
	intentFlag := fs.String("intent", "", "Intent for workflow mode (deprecated, use -p)")
	doGenerate := fs.Bool("generate", false, "Workflow mode: execute generation step (requires --generate-cmd)")
	generateCmd := fs.String("generate-cmd", "", "Workflow mode: shell command for generation")

	// Shared flags.
	artifactDir := fs.String("artifact-dir", ".openmelon/artifacts", "Output directory for artifacts + provenance")
	compilerPath := fs.String("compiler", "", "PYTHONPATH for editable Skill-Plus compiler (default: prefer `skillplus` console script on PATH)")
	timeoutSec := fs.Int("timeout", 300, "Total execution timeout in seconds")
	locale := fs.String("locale", "zh-CN", "Locale for skill compilation")
	modelProfile := fs.String("model-profile", "gpt-image-family", "Skill compile model profile")
	provenancePath := fs.String("provenance", "", "Override provenance JSONL path (default: <artifact-dir>/provenance.jsonl)")
	jsonOut := fs.Bool("json", false, "Print final result as JSON to stdout (agent mode)")

	if err := fs.Parse(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*timeoutSec)*time.Second)
	defer cancel()

	switch {
	case *prompt != "":
		err := runAgent(ctx, agentOpts{
			intent:       *prompt,
			skillSpec:    *skillSpec,
			llmProvider:  *llmProvider,
			llmModel:     *llmModel,
			llmBaseURL:   *llmBaseURL,
			imageEnabled: *imgEnabled,
			imageModel:   *imgModel,
			imageBaseURL: *imgBaseURL,
			publish:      *publish,
			postText:     *postText,
			locale:       *locale,
			modelProfile: *modelProfile,
			compilerPath: *compilerPath,
			artifactDir:  *artifactDir,
			skillRoot:    *skillRoot,
			jsonOut:      *jsonOut,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "[openmelon] error: %v\n", err)
			os.Exit(1)
		}
	case *projectFlag != "":
		err := runWorkflow(ctx, workflowOpts{
			projectPath:    *projectFlag,
			workflowID:     *workflowFlag,
			intent:         *intentFlag,
			artifactDir:    *artifactDir,
			compilerPath:   compilerPathOrDefault(*compilerPath),
			doGenerate:     *doGenerate,
			generateCmd:    *generateCmd,
			provenancePath: *provenancePath,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "[openmelon] error: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintln(os.Stderr, "openmelon — content-creation agent for the terminal")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Usage:")
		fmt.Fprintln(os.Stderr, `  openmelon -p "<intent>" [--skill skillplus:<name>] [--publish vbox]`)
		fmt.Fprintln(os.Stderr, `  openmelon --project examples/food-exploration/project.json   # legacy workflow mode`)
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Flags:")
		fs.PrintDefaults()
		os.Exit(1)
	}
}

// compilerPathOrDefault preserves the workflow-mode legacy default for
// backward compatibility with existing `openmelon --project ...` invocations
// that did not pass --compiler.
func compilerPathOrDefault(p string) string {
	if p != "" {
		return p
	}
	return "../skillplus/src"
}

// =====================================================================
// Agent mode (0.2)
// =====================================================================

type agentOpts struct {
	intent       string
	skillSpec    string
	llmProvider  string
	llmModel     string
	llmBaseURL   string
	imageEnabled bool
	imageModel   string
	imageBaseURL string
	publish      string
	postText     string
	locale       string
	modelProfile string
	compilerPath string
	artifactDir  string
	skillRoot    string
	jsonOut      bool
}

func runAgent(ctx context.Context, opts agentOpts) error {
	llmClient, err := llm.New(opts.llmProvider, "", opts.llmBaseURL, opts.llmModel)
	if err != nil {
		if errors.Is(err, llm.ErrNoAPIKey) {
			return fmt.Errorf("no API key for %s — set %s in your environment",
				opts.llmProvider, envVarFor(opts.llmProvider))
		}
		return fmt.Errorf("init LLM client: %w", err)
	}

	var imgGen imagegen.Generator
	if opts.imageEnabled {
		imgGen, err = imagegen.NewOpenAI("", opts.imageBaseURL, opts.imageModel)
		if err != nil {
			if errors.Is(err, imagegen.ErrNoAPIKey) {
				return fmt.Errorf("image generation requires OPENAI_API_KEY (or pass --image=false to skip)")
			}
			return fmt.Errorf("init image generator: %w", err)
		}
	}

	a := &agent.Agent{
		LLM:      llmClient,
		ImageGen: imgGen,
		Compiler: &skillplus.Compiler{CompilerPath: opts.compilerPath},
		// Stream LLM tokens to stderr in agent mode so the user sees the
		// model thinking instead of staring at a blank terminal.
		StreamTo: os.Stderr,
	}

	stamp := time.Now().UTC().Format("2006-01-02 15:04:05Z")
	fmt.Fprintf(os.Stderr, "[openmelon %s] skill=%s llm=%s/%s",
		stamp, opts.skillSpec, llmClient.Provider(), llmClient.Model())
	if imgGen != nil {
		fmt.Fprintf(os.Stderr, " image=%s/%s", imgGen.Provider(), imgGen.Model())
	}
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "[openmelon] intent: %s\n", opts.intent)

	res, err := a.RunOneShot(ctx, agent.RunInput{
		Intent:            opts.intent,
		SkillSpec:         opts.skillSpec,
		Locale:            opts.locale,
		ModelProfile:      opts.modelProfile,
		OutputDir:         opts.artifactDir,
		PackageSearchRoot: opts.skillRoot,
	})
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "[openmelon] skill compiled: %s@%s\n", res.SkillID, res.SkillVersion)
	if res.GenerationPrompt != "" {
		fmt.Fprintf(os.Stderr, "[openmelon] generation prompt: %s\n", truncate(res.GenerationPrompt, 240))
	}
	if res.ImagePath != "" {
		fmt.Fprintf(os.Stderr, "[openmelon] image: %s (sha256=%s)\n", res.ImagePath, res.ImageSHA256[:12])
	}
	fmt.Fprintf(os.Stderr, "[openmelon] provenance: %s\n", res.ProvenancePath)
	fmt.Fprintf(os.Stderr, "[openmelon] duration: %v\n", res.FinishedAt.Sub(res.StartedAt))

	// Optional publish step.
	if opts.publish == "vbox" {
		if err := publishToVBox(ctx, res, opts); err != nil {
			fmt.Fprintf(os.Stderr, "[openmelon] publish failed (artifact still saved locally): %v\n", err)
			// Non-fatal: the local artifact is the primary deliverable.
		}
	}

	if opts.jsonOut {
		summary, _ := json.MarshalIndent(res, "", "  ")
		fmt.Println(string(summary))
	}

	return nil
}

func envVarFor(provider string) string {
	switch provider {
	case "anthropic":
		return "ANTHROPIC_API_KEY"
	case "openai":
		return "OPENAI_API_KEY"
	case "openrouter":
		return "OPENROUTER_API_KEY"
	}
	return strings.ToUpper(provider) + "_API_KEY"
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// publishToVBox shells to vbox-cli to upload the image and post.
//
// Failure mode: if vbox-cli is not installed, or VBOX_API_KEY is not set,
// or the post is rejected, the error is reported but the local artifact
// remains. The agent does not retry — it reports and stops.
func publishToVBox(ctx context.Context, res *agent.RunResult, opts agentOpts) error {
	// Wired in cmd/openmelon/publish.go. Stubbed inline for now to keep
	// the import surface tight; the real implementation lives next to
	// the rest of agent-mode wiring.
	return runPublishToVBox(ctx, res, opts)
}

// =====================================================================
// Workflow mode (0.1, legacy)
// =====================================================================

type workflowOpts struct {
	projectPath    string
	workflowID     string
	intent         string
	artifactDir    string
	compilerPath   string
	doGenerate     bool
	generateCmd    string
	provenancePath string
}

func runWorkflow(ctx context.Context, opts workflowOpts) error {
	fmt.Printf("[openmelon] loading project: %s\n", opts.projectPath)
	proj, err := project.Load(opts.projectPath)
	if err != nil {
		return fmt.Errorf("load project: %w", err)
	}
	fmt.Printf("[openmelon] project: %s (%s)\n", proj.Name, proj.Platform)

	workflows, err := workflow.LoadWorkflows(opts.projectPath)
	if err != nil {
		return fmt.Errorf("load workflows: %w", err)
	}

	var wfDef *workflow.WorkflowDefinition
	if opts.workflowID != "" {
		var ok bool
		wfDef, ok = workflows[opts.workflowID]
		if !ok {
			return fmt.Errorf("workflow %q not found in project", opts.workflowID)
		}
	} else {
		for _, wf := range workflows {
			wfDef = wf
			break
		}
	}
	fmt.Printf("[openmelon] workflow: %s (%d stages)\n", wfDef.ID, len(wfDef.Stages))

	compiler := &skillplus.Compiler{CompilerPath: opts.compilerPath}

	var provider generation.Provider
	if opts.doGenerate && opts.generateCmd != "" {
		provider = &generation.ShellProvider{Command: opts.generateCmd}
	}

	provPath := opts.provenancePath
	if provPath == "" {
		provPath = filepath.Join(opts.artifactDir, "provenance.jsonl")
	}

	engine := &workflow.Engine{}
	req := &workflow.RunRequest{
		Project:        proj,
		WorkflowDef:    wfDef,
		Intent:         opts.intent,
		ArtifactDir:    opts.artifactDir,
		CompilerPath:   opts.compilerPath,
		ProvenancePath: provPath,
		Compiler:       compiler,
		Provider:       provider,
		Generate:       opts.doGenerate,
	}

	fmt.Printf("[openmelon] running workflow stages...\n")
	results, err := engine.Run(ctx, req)
	if err != nil {
		return fmt.Errorf("engine run: %w", err)
	}
	for _, r := range results {
		fmt.Printf("[openmelon] stage %q → artifact %s written to %s\n",
			r.Stage, r.Artifact.ID, opts.artifactDir)
	}
	fmt.Printf("[openmelon] done. %d stage(s) completed.\n", len(results))
	return nil
}
