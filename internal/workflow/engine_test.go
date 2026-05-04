package workflow

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Jackyffight/openmelon/internal/project"
	"github.com/Jackyffight/openmelon/internal/skillplus"
)

// fakeCompiledSkillJSON is a valid Python compiler output for unit tests.
const fakeCompiledSkillJSON = `{
	"target": "openmelon",
	"package": {"id": "unit-test-pkg", "version": "0.1.0"},
	"compiled_prompt": "unit test compiled prompt",
	"runtime_vars": {"key": "val"},
	"model_profile": "image_generator",
	"evaluation": {"checklist": ["check A"]}
}`

// setupFakeCompilerScript writes a fake python3 executable to dir and returns its path.
func setupFakeCompilerScript(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "fake_python3")
	script := "#!/bin/sh\ncat << 'EOF'\n" + fakeCompiledSkillJSON + "\nEOF\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake python: %v", err)
	}
	return path
}

func TestEngine_run_compileOnly(t *testing.T) {
	tmpDir := t.TempDir()
	fakePython := setupFakeCompilerScript(t, tmpDir)
	artifactDir := filepath.Join(tmpDir, "artifacts")

	proj := &project.Project{
		ID:       "unit-proj",
		Name:     "Unit Test Project",
		Platform: "test",
		Audience: "testers",
		Persona:  "test persona",
	}

	wfDef := &WorkflowDefinition{
		ID:       "unit_flow",
		Name:     "Unit Flow",
		Vertical: "test",
		Stages: []StageDefinition{
			{
				Stage:            StageVisualConcretization,
				SkillPlusPackage: "/fake/package.skillplus",
				CompileTarget:    "openmelon",
				ModelProfile:     "image_generator",
				ArtifactType:     "image_prompt",
			},
		},
	}

	compiler := &skillplus.Compiler{
		CompilerPath: tmpDir,
		PythonCmd:    fakePython,
	}

	engine := &Engine{}
	req := &RunRequest{
		Project:        proj,
		WorkflowDef:    wfDef,
		Intent:         "test intent",
		ArtifactDir:    artifactDir,
		CompilerPath:   tmpDir,
		ProvenancePath: filepath.Join(tmpDir, "provenance.jsonl"),
		Compiler:       compiler,
		Provider:       nil,
		Generate:       false, // compile-only: no provider needed
	}

	results, err := engine.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("engine.Run error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	r := results[0]
	if r.Stage != StageVisualConcretization {
		t.Errorf("stage = %q, want %q", r.Stage, StageVisualConcretization)
	}
	if r.Artifact == nil {
		t.Fatal("artifact is nil")
	}
	if r.Artifact.Content != "unit test compiled prompt" {
		t.Errorf("content = %q, want compiled prompt text", r.Artifact.Content)
	}

	// Artifact content file must exist on disk.
	entries, err := os.ReadDir(artifactDir)
	if err != nil {
		t.Fatalf("read artifact dir: %v", err)
	}
	if len(entries) == 0 {
		t.Error("expected artifact files in artifact dir, got none")
	}

	// Provenance JSONL must exist.
	if _, err := os.Stat(filepath.Join(tmpDir, "provenance.jsonl")); err != nil {
		t.Errorf("provenance.jsonl not created: %v", err)
	}
}

func TestEngine_run_contextCancelled(t *testing.T) {
	tmpDir := t.TempDir()
	fakePython := setupFakeCompilerScript(t, tmpDir)

	proj := &project.Project{
		ID: "p", Name: "P", Platform: "x",
	}
	wfDef := &WorkflowDefinition{
		ID: "flow",
		Stages: []StageDefinition{
			{Stage: StageVisualConcretization, SkillPlusPackage: "/pkg", CompileTarget: "openmelon", ModelProfile: "m", ArtifactType: "image_prompt"},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	engine := &Engine{}
	_, err := engine.Run(ctx, &RunRequest{
		Project:      proj,
		WorkflowDef:  wfDef,
		ArtifactDir:  filepath.Join(tmpDir, "art"),
		CompilerPath: tmpDir,
		Compiler:     &skillplus.Compiler{CompilerPath: tmpDir, PythonCmd: fakePython},
	})

	if err == nil {
		t.Fatal("expected error for cancelled context, got nil")
	}
}
