package skillplus

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// validCompiledSkillJSON is a minimal valid response from the Python compiler.
const validCompiledSkillJSON = `{
	"target": "openmelon",
	"package": {"id": "food-street-realism", "version": "1.0.0"},
	"compiled_prompt": "test compiled prompt",
	"runtime_vars": {"realism_level": "high"},
	"model_profile": "gpt-image-family",
	"evaluation": {"checklist": ["check sharpness", "check realism"]},
	"output_schema": {"type": "object"},
	"stage_contract": {"stage": "visual_prompt_concretization"}
}`

func TestCompiler_pythonNotFound(t *testing.T) {
	c := &Compiler{
		CompilerPath: "/fake",
		PythonCmd:    "/nonexistent/python99",
		// Force mode-2 path: pretend the console script is also unfindable.
		SkillplusBinary: "/nonexistent/skillplus99",
	}
	req := &CompileRequest{
		PackagePath:  "/some/package",
		Target:       "openmelon",
		ModelProfile: "gpt-image-family",
	}
	_, err := c.Compile(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for missing python, got nil")
	}
	if !strings.Contains(err.Error(), "is on PATH") || !strings.Contains(err.Error(), "pip install skillplus") {
		t.Errorf("expected install hint in error, got: %v", err)
	}
}

func TestCompiler_successPythonMode(t *testing.T) {
	tmpDir := t.TempDir()
	fakePython := filepath.Join(tmpDir, "fake_python3")
	script := "#!/bin/sh\ncat << 'ENDJSON'\n" + validCompiledSkillJSON + "\nENDJSON\n"
	if err := os.WriteFile(fakePython, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	c := &Compiler{
		CompilerPath:    tmpDir,
		PythonCmd:       fakePython,
		SkillplusBinary: "/nonexistent/skillplus", // force python mode
	}
	req := &CompileRequest{
		PackagePath:  "/some/food.skillplus",
		Target:       "openmelon",
		ModelProfile: "gpt-image-family",
		Locale:       "zh-CN",
		Vars:         map[string]string{"realism_level": "high"},
	}

	got, err := c.Compile(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.PackageID != "food-street-realism" {
		t.Errorf("PackageID = %q, want %q", got.PackageID, "food-street-realism")
	}
	if got.Prompt != "test compiled prompt" {
		t.Errorf("Prompt = %q, want %q", got.Prompt, "test compiled prompt")
	}
	if len(got.Evaluation) != 2 {
		t.Errorf("Evaluation len = %d, want 2", len(got.Evaluation))
	}
}

func TestCompiler_successConsoleScriptMode(t *testing.T) {
	tmpDir := t.TempDir()
	fakeBin := filepath.Join(tmpDir, "skillplus")
	script := "#!/bin/sh\ncat << 'ENDJSON'\n" + validCompiledSkillJSON + "\nENDJSON\n"
	if err := os.WriteFile(fakeBin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	c := &Compiler{
		// CompilerPath empty → prefer console script.
		SkillplusBinary: fakeBin,
	}
	req := &CompileRequest{
		PackagePath:  "/some/food.skillplus",
		Target:       "openmelon",
		ModelProfile: "gpt-image-family",
	}
	got, err := c.Compile(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.PackageID != "food-street-realism" {
		t.Errorf("PackageID = %q, want %q", got.PackageID, "food-street-realism")
	}
}

func TestCompileRaw_returnsFullJSON(t *testing.T) {
	tmpDir := t.TempDir()
	fakeBin := filepath.Join(tmpDir, "skillplus")
	script := "#!/bin/sh\ncat << 'ENDJSON'\n" + validCompiledSkillJSON + "\nENDJSON\n"
	if err := os.WriteFile(fakeBin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	c := &Compiler{SkillplusBinary: fakeBin}
	raw, err := c.CompileRaw(context.Background(), &CompileRequest{
		PackagePath: "/x", Target: "openmelon", ModelProfile: "gpt-image-family",
	})
	if err != nil {
		t.Fatalf("CompileRaw: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("returned bytes not valid JSON: %v", err)
	}
	if _, ok := parsed["output_schema"]; !ok {
		t.Errorf("expected output_schema in raw output (slim Compile would drop it)")
	}
	if _, ok := parsed["stage_contract"]; !ok {
		t.Errorf("expected stage_contract in raw output (slim Compile would drop it)")
	}
}
