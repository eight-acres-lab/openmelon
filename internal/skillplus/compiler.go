package skillplus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// CompileRequest holds the parameters for a Skill-Plus compilation.
type CompileRequest struct {
	PackagePath  string
	Target       string
	ModelProfile string
	Locale       string
	Vars         map[string]string
}

// Compiler invokes the Skill-Plus reference compiler.
//
// Resolution order for how to invoke the compiler:
//
//  1. If CompilerPath is empty AND the `skillplus` console script is on
//     PATH (because the user has run `pip install skillplus`), invoke it
//     directly: `skillplus <pkg> --target ...`. No PYTHONPATH gymnastics.
//
//  2. If CompilerPath is set, invoke `python -m skillplus` with
//     PYTHONPATH=<CompilerPath>. Useful for local development against an
//     editable checkout of skillplus (point CompilerPath at
//     `<skillplus-repo>/src`).
//
//  3. If both fail, return a clear error explaining the install /
//     PYTHONPATH options.
type Compiler struct {
	// CompilerPath is added to PYTHONPATH when invoking via `python -m`.
	// Leave empty to prefer the `skillplus` console script on PATH.
	CompilerPath string
	// PythonCmd overrides the Python executable used in mode (2). Default
	// "python3". Useful in tests.
	PythonCmd string
	// SkillplusBinary overrides the console-script command used in mode
	// (1). Default "skillplus". Useful in tests.
	SkillplusBinary string
}

// rawOutput is the slim view the workflow engine has historically used.
// The agent loop wants the full compiler output instead — see CompileRaw.
type rawOutput struct {
	Target  string `json:"target"`
	Package struct {
		ID      string `json:"id"`
		Version string `json:"version"`
	} `json:"package"`
	CompiledPrompt string            `json:"compiled_prompt"`
	RuntimeVars    map[string]string `json:"runtime_vars"`
	ModelProfile   string            `json:"model_profile"`
	Evaluation     struct {
		Checklist []string `json:"checklist"`
	} `json:"evaluation"`
}

// Compile invokes the Skill-Plus compiler subprocess and returns a slim
// CompiledSkill (the workflow-engine view).
func (c *Compiler) Compile(ctx context.Context, req *CompileRequest) (*CompiledSkill, error) {
	stdout, err := c.runCompiler(ctx, req)
	if err != nil {
		return nil, err
	}

	var raw rawOutput
	if err := json.Unmarshal(stdout, &raw); err != nil {
		return nil, fmt.Errorf("skillplus: failed to parse compiler output: %w", err)
	}

	return &CompiledSkill{
		PackageID:      raw.Package.ID,
		PackageVersion: raw.Package.Version,
		Target:         raw.Target,
		ModelProfile:   raw.ModelProfile,
		RuntimeVars:    raw.RuntimeVars,
		Prompt:         raw.CompiledPrompt,
		Evaluation:     raw.Evaluation.Checklist,
	}, nil
}

// CompileRaw returns the full compiler JSON output unparsed.
//
// The agent loop uses this to feed the LLM the entire compiled object —
// output_schema, stage_contract, evaluation, runtime_vars — without the
// workflow engine's slim CompiledSkill view dropping fields.
func (c *Compiler) CompileRaw(ctx context.Context, req *CompileRequest) (json.RawMessage, error) {
	stdout, err := c.runCompiler(ctx, req)
	if err != nil {
		return nil, err
	}
	// Validate it parses, but return the bytes verbatim so the caller can
	// re-marshal or pluck fields without re-running the compiler.
	var probe map[string]any
	if err := json.Unmarshal(stdout, &probe); err != nil {
		return nil, fmt.Errorf("skillplus: compiler output is not valid JSON: %w", err)
	}
	return json.RawMessage(stdout), nil
}

// runCompiler picks an invocation mode and returns raw stdout.
func (c *Compiler) runCompiler(ctx context.Context, req *CompileRequest) ([]byte, error) {
	cmd, err := c.buildCommand(ctx, req)
	if err != nil {
		return nil, err
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return nil, fmt.Errorf("skillplus compile failed for %q: %s", req.PackagePath, detail)
	}
	return stdout.Bytes(), nil
}

func (c *Compiler) buildCommand(ctx context.Context, req *CompileRequest) (*exec.Cmd, error) {
	args := []string{req.PackagePath,
		"--target", req.Target,
		"--model-profile", req.ModelProfile,
	}
	if req.Locale != "" {
		args = append(args, "--locale", req.Locale)
	}
	for k, v := range req.Vars {
		args = append(args, "--var", k+"="+v)
	}

	// Mode 1: console script (preferred whenever available on PATH,
	// regardless of whether CompilerPath is set).
	binary := c.skillplusBinary()
	if _, err := exec.LookPath(binary); err == nil {
		return exec.CommandContext(ctx, binary, args...), nil
	}

	// Mode 2: `python -m skillplus` with optional PYTHONPATH=<CompilerPath>.
	pythonCmd := c.pythonCmd()
	if _, err := exec.LookPath(pythonCmd); err != nil {
		return nil, fmt.Errorf(
			"skillplus: neither %q nor %q is on PATH — install with `pip install skillplus`, "+
				"or pass --compiler <path-to-skillplus-src> for editable use: %w",
			binary, pythonCmd, err,
		)
	}
	cmd := exec.CommandContext(ctx, pythonCmd, append([]string{"-m", "skillplus"}, args...)...)
	if c.CompilerPath != "" {
		cmd.Env = append(os.Environ(), "PYTHONPATH="+filepath.Clean(c.CompilerPath))
	} else {
		cmd.Env = os.Environ()
	}
	return cmd, nil
}

func (c *Compiler) pythonCmd() string {
	if c.PythonCmd != "" {
		return c.PythonCmd
	}
	return "python3"
}

func (c *Compiler) skillplusBinary() string {
	if c.SkillplusBinary != "" {
		return c.SkillplusBinary
	}
	return "skillplus"
}
