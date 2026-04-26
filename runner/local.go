// Copyright 2026 Point Eight AI Pte. Ltd.
// Licensed under the Apache License, Version 2.0

// Package runner executes Skill-Plus skills.
package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/pointeight/skillplus-engine/registry"
)

const defaultMaxOutputBytes = 8192

// Local executes skills as local child processes.
type Local struct {
	Timeout time.Duration
}

// NewLocal creates a local runner with a per-skill timeout.
func NewLocal(timeout time.Duration) *Local {
	return &Local{Timeout: timeout}
}

// Run executes skill with JSON input and returns stdout bytes.
func (r *Local) Run(ctx context.Context, skill registry.Skill, input map[string]any) ([]byte, error) {
	timeout := r.Timeout
	if timeout == 0 {
		timeout = 15 * time.Second
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd, err := commandForSkill(runCtx, skill)
	if err != nil {
		return nil, err
	}
	cmd.Dir = skill.Dir

	payload, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("marshal skill input for %s: %w", skill.Manifest.Slug, err)
	}
	cmd.Stdin = bytes.NewReader(payload)

	maxOutput := skill.Manifest.Output.MaxSizeBytes
	if maxOutput == 0 {
		maxOutput = defaultMaxOutputBytes
	}
	stdout := newCappedBuffer(maxOutput)
	stderr := newCappedBuffer(maxOutput)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err = cmd.Run()
	if runCtx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("skill %s timed out after %s", skill.Manifest.Slug, timeout)
	}
	if stdout.Exceeded() {
		return nil, fmt.Errorf("skill %s output exceeded %d bytes", skill.Manifest.Slug, maxOutput)
	}
	if stderr.Exceeded() {
		return nil, fmt.Errorf("skill %s stderr exceeded %d bytes", skill.Manifest.Slug, maxOutput)
	}
	if err != nil {
		return nil, fmt.Errorf("skill %s failed: %w: %s", skill.Manifest.Slug, err, stderr.String())
	}

	return stdout.Bytes(), nil
}

func commandForSkill(ctx context.Context, skill registry.Skill) (*exec.Cmd, error) {
	switch skill.Manifest.Runtime {
	case "python":
		entry := skill.Manifest.Entrypoint
		if entry == "" || entry == "main" {
			entry = "main.py"
		}
		path, err := resolveEntrypoint(entry)
		if err != nil {
			return nil, err
		}
		return exec.CommandContext(ctx, "python3", path), nil
	case "go":
		if skill.Manifest.Entrypoint != "" && skill.Manifest.Entrypoint != "main" {
			return nil, fmt.Errorf("unsupported go entrypoint %q for skill %s", skill.Manifest.Entrypoint, skill.Manifest.Slug)
		}
		return exec.CommandContext(ctx, "go", "run", "."), nil
	case "typescript":
		entry := skill.Manifest.Entrypoint
		if entry == "" || entry == "main" {
			entry = "main.js"
		}
		path, err := resolveEntrypoint(entry)
		if err != nil {
			return nil, err
		}
		return exec.CommandContext(ctx, "node", path), nil
	default:
		return nil, fmt.Errorf("unsupported runtime %q for skill %s", skill.Manifest.Runtime, skill.Manifest.Slug)
	}
}

func resolveEntrypoint(entry string) (string, error) {
	if filepath.IsAbs(entry) {
		return "", fmt.Errorf("entrypoint %q must be relative", entry)
	}
	cleaned := filepath.Clean(entry)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("entrypoint %q escapes skill directory", entry)
	}
	return cleaned, nil
}

type cappedBuffer struct {
	buf      bytes.Buffer
	limit    int
	exceeded bool
}

func newCappedBuffer(limit int) *cappedBuffer {
	return &cappedBuffer{limit: limit}
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if b.buf.Len()+len(p) > b.limit {
		remaining := b.limit - b.buf.Len()
		if remaining > 0 {
			_, _ = b.buf.Write(p[:remaining])
		}
		b.exceeded = true
		return len(p), nil
	}
	_, _ = b.buf.Write(p)
	return len(p), nil
}

func (b *cappedBuffer) Bytes() []byte {
	return b.buf.Bytes()
}

func (b *cappedBuffer) String() string {
	return b.buf.String()
}

func (b *cappedBuffer) Exceeded() bool {
	return b.exceeded
}
