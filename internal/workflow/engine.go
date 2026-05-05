package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/eight-acres-lab/openmelon/internal/artifacts"
	"github.com/eight-acres-lab/openmelon/internal/generation"
	"github.com/eight-acres-lab/openmelon/internal/project"
	"github.com/eight-acres-lab/openmelon/internal/provenance"
	"github.com/eight-acres-lab/openmelon/internal/skillplus"
)

// RunRequest holds all parameters needed to execute a workflow.
type RunRequest struct {
	// Project is the loaded project for context variables.
	Project *project.Project
	// WorkflowDef is the workflow definition to execute.
	WorkflowDef *WorkflowDefinition
	// Intent is the operator's free-text intent passed to each compile stage as a variable.
	Intent string
	// ArtifactDir is the directory where artifact files are written.
	ArtifactDir string
	// CompilerPath is the PYTHONPATH for the Skill-Plus Python compiler.
	CompilerPath string
	// ProjectDir is the directory containing the project.json file, used to
	// resolve relative skillplus_package paths in stage definitions.
	ProjectDir string
	// ProvenancePath is the path to the append-only JSONL provenance log.
	ProvenancePath string
	// Compiler is the Skill-Plus compiler adapter (optional override, created from CompilerPath if nil).
	Compiler *skillplus.Compiler
	// Provider is the generation provider used for model calls.
	Provider generation.Provider
	// Generate controls whether the generation step is executed (set false for compile-only dry-runs).
	Generate bool
}

// StageResult is the output of a single workflow stage execution.
type StageResult struct {
	Stage    Stage
	Artifact *artifacts.Artifact
}

// Engine runs a workflow definition stage by stage.
type Engine struct{}

// Run executes the workflow defined in req.WorkflowDef stage by stage.
// For each stage it:
//  1. Compiles the Skill-Plus package into a prompt.
//  2. Optionally calls req.Provider.Generate to produce artifact content.
//  3. Assigns a StableID and writes the artifact to req.ArtifactDir.
//  4. Appends a provenance record to req.ProvenancePath.
//
// Run returns all stage results or an error on the first failure.
func (e *Engine) Run(ctx context.Context, req *RunRequest) ([]*StageResult, error) {
	compiler := req.Compiler
	if compiler == nil {
		compiler = &skillplus.Compiler{CompilerPath: req.CompilerPath}
	}

	var results []*StageResult

	for _, stage := range req.WorkflowDef.Stages {
		select {
		case <-ctx.Done():
			return results, ctx.Err()
		default:
		}

		// -- Step 1: Resolve SkillPlus package path relative to the project file's dir --
		pkgPath := stage.SkillPlusPackage
		if req.ProjectDir != "" && !filepath.IsAbs(pkgPath) {
			pkgPath = filepath.Join(req.ProjectDir, pkgPath)
		}

		// -- Step 2: Build compile vars including intent and project context --
		vars := make(map[string]string)
		for k, v := range stage.Vars {
			vars[k] = v
		}
		if req.Intent != "" {
			vars["intent"] = req.Intent
		}
		if req.Project.Audience != "" {
			vars["audience"] = req.Project.Audience
		}
		if req.Project.Persona != "" {
			vars["persona"] = req.Project.Persona
		}

		compileReq := &skillplus.CompileRequest{
			PackagePath:  pkgPath,
			Target:       stage.CompileTarget,
			ModelProfile: stage.ModelProfile,
			Locale:       stage.Locale,
			Vars:         vars,
		}

		compiled, err := compiler.Compile(ctx, compileReq)
		if err != nil {
			return results, fmt.Errorf("engine: compile stage %q: %w", stage.Stage, err)
		}

		// -- Step 3: Optionally generate artifact content --
		content := compiled.Prompt
		var trace *generation.Trace
		if req.Generate && req.Provider != nil {
			genReq := &generation.Request{
				ArtifactType: stage.ArtifactType,
				Prompt:       compiled.Prompt,
				Model:        compiled.ModelProfile,
				Params:       compiled.RuntimeVars,
				Intent:       req.Intent,
			}
			content, trace, err = req.Provider.Generate(ctx, genReq)
			if err != nil {
				return results, fmt.Errorf("engine: generate stage %q: %w", stage.Stage, err)
			}
		}

		// -- Step 4: Build stable artifact ID and provenance record --
		artifactID := artifacts.StableID(
			req.Project.ID,
			req.WorkflowDef.ID,
			string(stage.Stage),
			compiled.PackageID,
		)

		now := time.Now().UTC().Format(time.RFC3339)
		recModel := compiled.ModelProfile
		if trace != nil && trace.Model != "" {
			recModel = trace.Model
		}
		rec := &provenance.Record{
			ArtifactID:     artifactID,
			ProjectID:      req.Project.ID,
			WorkflowID:     req.WorkflowDef.ID,
			Stage:          string(stage.Stage),
			SkillPackage:   pkgPath,
			CompiledTarget: stage.CompileTarget,
			Model:          recModel,
			PromptHash:     artifacts.StableID(compiled.Prompt),
			Timestamp:      now,
		}
		if trace != nil {
			params := map[string]string{
				"provider_type": trace.ProviderType,
			}
			if trace.Command != "" {
				params["command"] = trace.Command
			}
			rec.GenerationParams = params
		}

		provJSON, err := json.Marshal(rec)
		if err != nil {
			return results, fmt.Errorf("engine: marshal provenance for stage %q: %w", stage.Stage, err)
		}

		a := &artifacts.Artifact{
			ID:         artifactID,
			Type:       artifacts.Type(stage.ArtifactType),
			Content:    content,
			Provenance: string(provJSON),
		}

		// -- Step 5: Write artifact to disk --
		if err := artifacts.Write(req.ArtifactDir, a); err != nil {
			return results, fmt.Errorf("engine: write artifact stage %q: %w", stage.Stage, err)
		}

		// -- Step 6: Append provenance record to JSONL log --
		if req.ProvenancePath != "" {
			if err := provenance.AppendRecord(req.ProvenancePath, rec); err != nil {
				return results, fmt.Errorf("engine: append provenance stage %q: %w", stage.Stage, err)
			}
		} else {
			// default: provenance.jsonl in artifact dir
			defaultPath := filepath.Join(req.ArtifactDir, "provenance.jsonl")
			if err := provenance.AppendRecord(defaultPath, rec); err != nil {
				return results, fmt.Errorf("engine: append provenance stage %q: %w", stage.Stage, err)
			}
		}

		results = append(results, &StageResult{Stage: stage.Stage, Artifact: a})
	}

	return results, nil
}
