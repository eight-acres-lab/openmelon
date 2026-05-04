package main

// publish.go — agent-mode helper that shells out to vbox-cli to publish
// the generated artifact. Kept in its own file so future publish targets
// (e.g. local file copy, S3, other social platforms) can slot in next to
// runPublishToVBox without growing main.go further.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/eight-acres-lab/openmelon/internal/agent"
)

// runPublishToVBox uploads the generated image via `vbox-cli upload`,
// extracts the resulting fid, then posts via `vbox-cli post --media-fid`.
//
// Preconditions:
//   - `vbox-cli` is on PATH
//   - VBOX_API_KEY is set in the environment (vbox-cli reads it itself)
//   - res.ImagePath is non-empty (otherwise nothing to upload)
//
// Failure mode: returns an error if any step fails. The caller treats
// this as non-fatal — the local artifact is the primary deliverable.
func runPublishToVBox(ctx context.Context, res *agent.RunResult, opts agentOpts) error {
	if res.ImagePath == "" {
		return fmt.Errorf("no image to publish — generation_prompt was empty or image generation was disabled")
	}
	if _, err := exec.LookPath("vbox-cli"); err != nil {
		return fmt.Errorf("vbox-cli not on PATH — install with `npm i -g @e8s/vbox-cli`")
	}
	if os.Getenv("VBOX_API_KEY") == "" {
		return fmt.Errorf("VBOX_API_KEY not set in env")
	}

	// 1. Upload.
	uploadOut, err := runVBoxCLI(ctx, "upload", "--file", res.ImagePath, "--category", "image")
	if err != nil {
		return fmt.Errorf("vbox-cli upload: %w", err)
	}
	fid, err := extractFID(uploadOut)
	if err != nil {
		return fmt.Errorf("parse upload result: %w (raw: %s)", err, uploadOut)
	}
	fmt.Fprintf(os.Stderr, "[openmelon] uploaded → fid=%s\n", fid)

	// 2. Post.
	text := opts.postText
	if text == "" {
		text = opts.intent
	}
	postOut, err := runVBoxCLI(ctx, "post", "--text", text, "--media-fid", fid)
	if err != nil {
		return fmt.Errorf("vbox-cli post: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[openmelon] published. vbox-cli response: %s\n", strings.TrimSpace(postOut))
	return nil
}

// runVBoxCLI executes vbox-cli with the given args, returning stdout.
// Stderr is mirrored so the user sees vbox-cli's human messages.
func runVBoxCLI(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "vbox-cli", args...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return stdout.String(), nil
}

// extractFID parses vbox-cli upload's stdout JSON to find the file id.
//
// vbox-cli upload returns the MediaItem shape:
//
//	{ "fid": "...", "ext": "png", "media_type": "image", ... }
func extractFID(uploadStdout string) (string, error) {
	var m map[string]any
	if err := json.Unmarshal([]byte(uploadStdout), &m); err != nil {
		return "", err
	}
	fid, ok := m["fid"].(string)
	if !ok || fid == "" {
		return "", fmt.Errorf("no fid in response")
	}
	return fid, nil
}
