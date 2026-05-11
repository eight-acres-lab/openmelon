package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eight-acres-lab/openmelon/internal/llm"
	"github.com/eight-acres-lab/openmelon/internal/skillplus"
)

// fakeCompilerOutput mimics what `skillplus … --target openmelon` returns.
const fakeCompilerOutput = `{
  "target": "openmelon",
  "package": {"id": "food-street-realism", "version": "0.1.0"},
  "compiled_prompt": "You are a visual prompt director. Convert intent into observable visual evidence.",
  "runtime_vars": {"realism_level": "high"},
  "model_profile": "gpt-image-family",
  "evaluation": {"checklist": ["a", "b"]},
  "output_schema": {
    "type": "object",
    "required": ["scene_interpretation", "generation_prompt"],
    "properties": {
      "scene_interpretation": {"type": "object"},
      "generation_prompt": {"type": "string"}
    }
  },
  "stage_contract": {"stage": "visual_prompt_concretization"}
}`

// makeFakeCompiler writes a fake skillplus binary that always emits
// fakeCompilerOutput, and returns a Compiler configured to use it.
func makeFakeCompiler(t *testing.T) *skillplus.Compiler {
	t.Helper()
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "skillplus-fake")
	script := "#!/bin/sh\ncat << 'ENDJSON'\n" + fakeCompilerOutput + "\nENDJSON\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return &skillplus.Compiler{SkillplusBinary: bin}
}

// fakeLLM serves a fixed structured response.
func fakeLLMServer(t *testing.T, response string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Emit a minimal Anthropic-shaped response.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{{"type": "text", "text": response}},
		})
	}))
}

func TestRunOneShot_HappyPath_NoImage(t *testing.T) {
	llmSrv := fakeLLMServer(t, `{"scene_interpretation":{"camera":"phone"},"generation_prompt":"a noodle shop, ordinary lighting"}`)
	defer llmSrv.Close()

	llmClient, err := buildAnthropic(llmSrv.URL)
	if err != nil {
		t.Fatal(err)
	}

	// Make a fake .skillplus dir that the agent can resolve via "path:" spec.
	pkgDir := t.TempDir()
	a := &Agent{
		LLM:      llmClient,
		Compiler: makeFakeCompiler(t),
	}

	out := t.TempDir()
	res, err := a.RunOneShot(context.Background(), RunInput{
		Intent:    "下班吃一碗牛肉面",
		SkillSpec: "path:" + pkgDir,
		OutputDir: out,
	})
	if err != nil {
		t.Fatalf("RunOneShot: %v", err)
	}
	if res.SkillID != "food-street-realism" {
		t.Errorf("SkillID = %q", res.SkillID)
	}
	if res.GenerationPrompt == "" {
		t.Errorf("expected generation_prompt to be extracted from structured output")
	}
	if res.ImagePath != "" {
		t.Errorf("ImagePath should be empty when no ImageGen configured, got %q", res.ImagePath)
	}
	if res.ProvenancePath == "" {
		t.Errorf("ProvenancePath should be set")
	}

	// Verify the provenance file actually has a line.
	data, err := os.ReadFile(res.ProvenancePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "food-street-realism") {
		t.Errorf("provenance does not include skill id: %s", data)
	}
	if !strings.Contains(string(data), "下班吃一碗牛肉面") {
		t.Errorf("provenance does not include intent")
	}
}

func TestRunOneShot_HandlesFencedJSON(t *testing.T) {
	fenced := "```json\n" + `{"scene_interpretation":{},"generation_prompt":"x"}` + "\n```"
	llmSrv := fakeLLMServer(t, fenced)
	defer llmSrv.Close()

	llmClient, err := buildAnthropic(llmSrv.URL)
	if err != nil {
		t.Fatal(err)
	}

	a := &Agent{LLM: llmClient, Compiler: makeFakeCompiler(t)}
	pkgDir := t.TempDir()
	_, err = a.RunOneShot(context.Background(), RunInput{
		Intent: "x", SkillSpec: "path:" + pkgDir, OutputDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("expected fence-stripping to succeed: %v", err)
	}
}

func TestRunOneShot_FailsOnInvalidLLMResponse(t *testing.T) {
	llmSrv := fakeLLMServer(t, "this is not json at all just prose")
	defer llmSrv.Close()

	llmClient, err := buildAnthropic(llmSrv.URL)
	if err != nil {
		t.Fatal(err)
	}

	a := &Agent{LLM: llmClient, Compiler: makeFakeCompiler(t)}
	pkgDir := t.TempDir()
	_, err = a.RunOneShot(context.Background(), RunInput{
		Intent: "x", SkillSpec: "path:" + pkgDir, OutputDir: t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "invalid JSON") {
		t.Errorf("expected invalid JSON error, got %v", err)
	}
}

func TestRunOneShot_RequiresFields(t *testing.T) {
	a := &Agent{}
	_, err := a.RunOneShot(context.Background(), RunInput{})
	if err == nil {
		t.Fatal("expected error for empty input")
	}
}

func TestResolveSkillSpec_PathPrefix(t *testing.T) {
	a := &Agent{}
	dir := t.TempDir()
	got, err := a.resolveSkillSpec("path:"+dir, "")
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.Abs(dir)
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestResolveSkillSpec_SkillplusPrefix(t *testing.T) {
	root := t.TempDir()
	pkgDir := filepath.Join(root, "examples", "food-street-realism.skillplus")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	a := &Agent{}
	got, err := a.resolveSkillSpec("skillplus:food-street-realism", root)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != pkgDir {
		t.Errorf("got %q want %q", got, pkgDir)
	}
}

func TestResolveSkillSpec_NotFound(t *testing.T) {
	a := &Agent{}
	_, err := a.resolveSkillSpec("skillplus:nope", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected not-found error, got %v", err)
	}
}

func TestParseStructuredJSON_PlainJSON(t *testing.T) {
	got, err := parseStructuredJSON(`{"a":1}`)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"a":1}` {
		t.Errorf("got %s", got)
	}
}

func TestParseStructuredJSON_StripsFences(t *testing.T) {
	got, err := parseStructuredJSON("```json\n{\"a\":1}\n```")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `"a":1`) {
		t.Errorf("got %s", got)
	}
}

func TestParseStructuredJSON_ExtractsFromProse(t *testing.T) {
	got, err := parseStructuredJSON(`Sure! Here's the JSON: {"a":1} let me know if you need anything else.`)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"a":1}` {
		t.Errorf("got %s", got)
	}
}

// Build an Anthropic client pointed at the fake test server.
func buildAnthropic(baseURL string) (llm.Client, error) {
	c, err := llm.NewAnthropic("test-key", "", "claude-test")
	if err != nil {
		return nil, err
	}
	// Re-point at the test server via the (unexported) field.
	// We do this with a tiny helper rather than a public Setter so the
	// production constructor stays simple.
	setBaseURLForTest(c, baseURL)
	return c, nil
}

// setBaseURLForTest is implemented in agent_testhelpers.go (same package).
