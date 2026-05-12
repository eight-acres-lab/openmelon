// Package agent is OpenMelon's content-creation agent loop.
//
// Today this package implements one-shot mode only: a single intent goes
// in, a single set of artifacts comes out. Multi-turn / interactive REPL
// mode lands in 0.3 (see ROADMAP.md). The interfaces here are written so
// the REPL is an additional entry point on top of the same primitives,
// not a parallel rewrite.
//
// One-shot flow:
//
//  1. Resolve the skill spec to a package directory on disk.
//  2. Compile the package via the Skill-Plus reference compiler. Use the
//     full raw JSON output — the LLM call needs the output_schema to
//     produce a valid structured response.
//  3. Send the compiled prompt + the user intent to the LLM. The LLM is
//     asked to return a single JSON object matching the package's
//     output_schema.
//  4. Parse the structured output. Fail loudly if it doesn't parse — the
//     whole point of Skill-Plus is reproducible structured outputs, so a
//     malformed response is a real bug, not something to silently retry.
//  5. If the structured output contains a `generation_prompt` AND an
//     image generator is configured, produce one image and write it to
//     the output directory.
//  6. Append a provenance line covering everything: skill, intent,
//     locale, model profile, LLM provider+model, image provider+model,
//     image hash if produced, file path, and a UTC timestamp.
package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/eight-acres-lab/openmelon/internal/imagegen"
	"github.com/eight-acres-lab/openmelon/internal/llm"
	"github.com/eight-acres-lab/openmelon/internal/skillplus"
	"github.com/eight-acres-lab/openmelon/internal/version"
)

// Agent runs one-shot content-creation requests.
type Agent struct {
	// LLM does the prompt-structuring step. Required.
	LLM llm.Client

	// ImageGen produces images from the structured generation_prompt.
	// Optional — if nil, the agent skips image generation and just
	// returns the structured output.
	ImageGen imagegen.Generator

	// Compiler is the Skill-Plus compiler subprocess wrapper.
	// Required.
	Compiler *skillplus.Compiler

	// StreamTo, if non-nil, receives the LLM's text deltas as they
	// arrive from the network. cmd/openmelon sets this to os.Stderr in
	// agent mode so the user sees progress while the model is still
	// generating. Tests and the MCP server leave it nil to use the
	// non-streaming path (simpler buffering, easier to assert on).
	StreamTo io.Writer
}

// RunInput describes a one-shot run.
type RunInput struct {
	// Intent is the user's free-text creation request. Required.
	Intent string

	// SkillSpec selects which Skill-Plus package to compile. Format:
	//   "skillplus:<name>"  — searched under PackageSearchRoot et al.
	//   "path:/abs/or/rel"  — direct path to a .skillplus directory
	//   "<bare path>"       — treated as a path (no scheme prefix)
	// Required.
	SkillSpec string

	// Locale to compile for. Default "zh-CN".
	Locale string

	// ModelProfile is the package's per-vendor prompt overlay slug.
	// Default "gpt-image-family".
	ModelProfile string

	// Vars are runtime overrides passed to the compiler as `--var k=v`.
	Vars map[string]string

	// OutputDir is where visible artifacts (image, provenance) are written.
	// Default "outputs/artifacts".
	OutputDir string

	// ImageSize is passed to the image generator (WxH). Empty → vendor default.
	ImageSize string

	// PackageSearchRoot is the directory under which skill specs of the
	// form "skillplus:<name>" are resolved as
	// "<root>/examples/<name>.skillplus".
	PackageSearchRoot string
}

// RunResult captures everything produced by one run.
type RunResult struct {
	SkillID          string          `json:"skill_id"`
	SkillVersion     string          `json:"skill_version"`
	Intent           string          `json:"intent"`
	Compiled         json.RawMessage `json:"-"` // verbose; not in summary JSON
	Structured       json.RawMessage `json:"structured"`
	GenerationPrompt string          `json:"generation_prompt,omitempty"`
	ArtifactPath     string          `json:"artifact_path,omitempty"`
	ImagePath        string          `json:"image_path,omitempty"`
	ImageSHA256      string          `json:"image_sha256,omitempty"`
	ProvenancePath   string          `json:"provenance_path"`
	StartedAt        time.Time       `json:"started_at"`
	FinishedAt       time.Time       `json:"finished_at"`
}

// RunOneShot executes the end-to-end one-shot flow.
func (a *Agent) RunOneShot(ctx context.Context, in RunInput) (*RunResult, error) {
	if a.LLM == nil {
		return nil, fmt.Errorf("agent: LLM is required")
	}
	if a.Compiler == nil {
		return nil, fmt.Errorf("agent: Compiler is required")
	}
	if in.Intent == "" {
		return nil, fmt.Errorf("agent: Intent is required")
	}
	if in.SkillSpec == "" {
		return nil, fmt.Errorf("agent: SkillSpec is required")
	}

	if in.Locale == "" {
		in.Locale = "zh-CN"
	}
	if in.ModelProfile == "" {
		in.ModelProfile = "gpt-image-family"
	}
	if in.OutputDir == "" {
		in.OutputDir = "outputs/artifacts"
	}

	startedAt := time.Now().UTC()

	// 1. Resolve skill spec to a package directory.
	pkgDir, err := a.resolveSkillSpec(in.SkillSpec, in.PackageSearchRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve skill spec %q: %w", in.SkillSpec, err)
	}

	// 2. Compile the package — full raw JSON.
	compiled, err := a.Compiler.CompileRaw(ctx, &skillplus.CompileRequest{
		PackagePath:  pkgDir,
		Target:       "openmelon",
		ModelProfile: in.ModelProfile,
		Locale:       in.Locale,
		Vars:         in.Vars,
	})
	if err != nil {
		return nil, fmt.Errorf("compile skill: %w", err)
	}

	var compiledMap map[string]any
	if err := json.Unmarshal(compiled, &compiledMap); err != nil {
		return nil, fmt.Errorf("parse compiled output: %w", err)
	}

	skillID := stringField(compiledMap, "package", "id")
	skillVersion := stringField(compiledMap, "package", "version")
	compiledPrompt, _ := compiledMap["compiled_prompt"].(string)
	if compiledPrompt == "" {
		return nil, fmt.Errorf("compiled output is missing compiled_prompt — check the package targets the openmelon target")
	}

	// 3. Build the LLM call.
	systemPrompt, err := buildSystemPrompt(compiledPrompt, compiledMap)
	if err != nil {
		return nil, fmt.Errorf("build system prompt: %w", err)
	}

	llmOpts := llm.CompleteOptions{
		System:   systemPrompt,
		User:     in.Intent,
		JSONOnly: true,
	}
	var rawResponse string
	if a.StreamTo != nil {
		rawResponse, err = a.LLM.Stream(ctx, llmOpts, func(delta string) {
			_, _ = io.WriteString(a.StreamTo, delta)
		})
	} else {
		rawResponse, err = a.LLM.Complete(ctx, llmOpts)
	}
	if err != nil {
		return nil, fmt.Errorf("llm: %w", err)
	}

	// 4. Parse structured output (with fence-stripping fallback).
	structuredJSON, err := parseStructuredJSON(rawResponse)
	if err != nil {
		return nil, fmt.Errorf("llm returned invalid JSON: %w (raw response: %s)", err, rawResponse)
	}

	var structured map[string]any
	_ = json.Unmarshal(structuredJSON, &structured)

	if err := os.MkdirAll(in.OutputDir, 0o755); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}

	result := &RunResult{
		SkillID:      skillID,
		SkillVersion: skillVersion,
		Intent:       in.Intent,
		Compiled:     compiled,
		Structured:   structuredJSON,
		StartedAt:    startedAt,
	}

	// 5. Optional image generation; always persist structured output as artifact.
	ts := startedAt.Format("20060102-150405")
	if genPrompt, ok := structured["generation_prompt"].(string); ok && genPrompt != "" {
		result.GenerationPrompt = genPrompt
		if a.ImageGen != nil {
			img, err := a.ImageGen.Generate(ctx, imagegen.GenerateOptions{
				Prompt: genPrompt,
				Size:   in.ImageSize,
			})
			if err != nil {
				return result, fmt.Errorf("image generation: %w", err)
			}
			imgPath := filepath.Join(in.OutputDir, fmt.Sprintf("%s-%s.png", skillID, ts))
			if err := os.WriteFile(imgPath, img.Data, 0o644); err != nil {
				return result, fmt.Errorf("write image: %w", err)
			}
			sum := sha256.Sum256(img.Data)
			result.ImagePath = imgPath
			result.ArtifactPath = imgPath
			result.ImageSHA256 = hex.EncodeToString(sum[:])
		}
	} else {
		// Text-only skill: persist structured JSON as artifact.
		artPath := filepath.Join(in.OutputDir, fmt.Sprintf("%s-%s.json", skillID, ts))
		if err := os.WriteFile(artPath, structuredJSON, 0o644); err != nil {
			return result, fmt.Errorf("write artifact: %w", err)
		}
		result.ArtifactPath = artPath
	}

	result.FinishedAt = time.Now().UTC()

	// 6. Append provenance line.
	provPath, err := writeProvenance(in.OutputDir, in, a, result)
	if err != nil {
		return result, fmt.Errorf("write provenance: %w", err)
	}
	result.ProvenancePath = provPath

	return result, nil
}

// resolveSkillSpec turns a spec string into an absolute directory path.
//
// The "skillplus:<name>" prefix looks for <root>/examples/<name>.skillplus
// under several candidate roots, in priority order:
//
//  1. PackageSearchRoot from RunInput (explicit user override)
//  2. The parent of Compiler.CompilerPath, when CompilerPath is set
//     (editable skillplus checkout: <repo>/src → <repo>/examples)
//  3. <cwd>
//  4. <cwd>/../skillplus  (workspace convention: openmelon and skillplus
//     are sibling directories under e8s/)
//  5. $SKILLPLUS_EXAMPLES_ROOT, if set
//
// "path:<dir>" and bare paths are resolved directly without searching.
func (a *Agent) resolveSkillSpec(spec, searchRoot string) (string, error) {
	if strings.HasPrefix(spec, "path:") {
		return filepath.Abs(strings.TrimPrefix(spec, "path:"))
	}
	if strings.HasPrefix(spec, "skillplus:") {
		name := strings.TrimPrefix(spec, "skillplus:")
		var roots []string
		if searchRoot != "" {
			roots = append(roots, searchRoot)
		}
		if a.Compiler != nil && a.Compiler.CompilerPath != "" {
			roots = append(roots, filepath.Dir(a.Compiler.CompilerPath))
		}
		if cwd, err := os.Getwd(); err == nil {
			roots = append(roots, cwd)
			roots = append(roots, filepath.Join(cwd, "..", "skillplus"))
		}
		if env := os.Getenv("SKILLPLUS_EXAMPLES_ROOT"); env != "" {
			roots = append(roots, env)
		}

		var tried []string
		for _, root := range roots {
			for _, subdir := range []string{"examples", "skills"} {
				candidate := filepath.Join(root, subdir, name+".skillplus")
				tried = append(tried, candidate)
				if info, err := os.Stat(candidate); err == nil && info.IsDir() {
					abs, err := filepath.Abs(candidate)
					if err == nil {
						return abs, nil
					}
					return candidate, nil
				}
			}
		}
		return "", fmt.Errorf("skill %q not found.\nLooked in:\n  %s\nPass --skill-root <dir-containing-examples/>, or set $SKILLPLUS_EXAMPLES_ROOT", spec, strings.Join(tried, "\n  "))
	}
	// Bare path.
	abs, err := filepath.Abs(spec)
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(abs); err != nil || !info.IsDir() {
		return "", fmt.Errorf("skill path %q does not exist or is not a directory", abs)
	}
	return abs, nil
}

// buildSystemPrompt concatenates the package's compiled prompt with an
// explicit instruction to respond in JSON matching the package's output
// schema. The LLM sees: "<skill prompt>\n\n# Output Schema\n<schema JSON>\n
// # Response Format\n<one-paragraph instruction>".
func buildSystemPrompt(compiledPrompt string, compiledMap map[string]any) (string, error) {
	var b strings.Builder
	b.WriteString(strings.TrimRight(compiledPrompt, "\n"))
	b.WriteString("\n\n# Output Schema\n\n")
	b.WriteString("Respond with a single JSON object matching this schema. ")
	b.WriteString("Required fields must be present. Optional fields can be omitted if not applicable. ")
	b.WriteString("Do NOT wrap the JSON in markdown fences. Do NOT add prose before or after the JSON.\n\n```json\n")

	schema, ok := compiledMap["output_schema"]
	if !ok {
		return "", fmt.Errorf("compiled output has no output_schema")
	}
	schemaBytes, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal output_schema: %w", err)
	}
	b.Write(schemaBytes)
	b.WriteString("\n```\n")
	return b.String(), nil
}

// parseStructuredJSON tries to parse the LLM response as JSON.
// Falls back to stripping markdown fences if the model added them.
func parseStructuredJSON(raw string) (json.RawMessage, error) {
	trimmed := strings.TrimSpace(raw)
	if json.Valid([]byte(trimmed)) {
		return json.RawMessage(trimmed), nil
	}
	// Try fenced code block.
	if strings.HasPrefix(trimmed, "```") {
		lines := strings.Split(trimmed, "\n")
		if len(lines) > 2 {
			// Drop first line (```json or ```), drop last fence.
			body := strings.Join(lines[1:len(lines)-1], "\n")
			body = strings.TrimSpace(body)
			if json.Valid([]byte(body)) {
				return json.RawMessage(body), nil
			}
		}
	}
	// Try to extract the first {...} balanced span.
	if start := strings.Index(trimmed, "{"); start >= 0 {
		depth := 0
		for i := start; i < len(trimmed); i++ {
			switch trimmed[i] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					candidate := trimmed[start : i+1]
					if json.Valid([]byte(candidate)) {
						return json.RawMessage(candidate), nil
					}
				}
			}
		}
	}
	return nil, fmt.Errorf("could not extract JSON object from response")
}

// stringField walks a path of nested map keys and returns the string
// at that path, or "" if anything along the way is missing or the
// wrong type. Convenience for the small handful of fields the agent
// loop reads from the compiled map.
func stringField(m map[string]any, keys ...string) string {
	cur := any(m)
	for _, k := range keys {
		mm, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = mm[k]
	}
	s, _ := cur.(string)
	return s
}

// writeProvenance appends a single JSONL line covering this run.
//
// Path: <outputDir>/provenance.jsonl. Created if missing.
//
// The provenance line is intentionally rich — anyone trying to reproduce
// this generation later needs the skill version, locale, model profile,
// LLM provider+model, image provider+model, and content hashes. Every
// field listed below is recorded.
func writeProvenance(outputDir string, in RunInput, a *Agent, r *RunResult) (string, error) {
	provPath := filepath.Join(outputDir, "provenance.jsonl")

	entry := map[string]any{
		"ts":            r.FinishedAt.Format(time.RFC3339),
		"agent":         "openmelon",
		"agent_version": version.Version,
		"skill": map[string]any{
			"id":            r.SkillID,
			"version":       r.SkillVersion,
			"locale":        in.Locale,
			"model_profile": in.ModelProfile,
			"vars":          in.Vars,
			"spec":          in.SkillSpec,
		},
		"intent": in.Intent,
		"llm": map[string]any{
			"provider": a.LLM.Provider(),
			"model":    a.LLM.Model(),
		},
		"duration_ms": r.FinishedAt.Sub(r.StartedAt).Milliseconds(),
	}
	if a.ImageGen != nil && r.ImagePath != "" {
		entry["image"] = map[string]any{
			"provider":     a.ImageGen.Provider(),
			"model":        a.ImageGen.Model(),
			"path":         r.ImagePath,
			"sha256":       r.ImageSHA256,
			"prompt_chars": len(r.GenerationPrompt),
		}
	}

	line, err := json.Marshal(entry)
	if err != nil {
		return "", err
	}

	f, err := os.OpenFile(provPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return "", err
	}
	return provPath, nil
}
