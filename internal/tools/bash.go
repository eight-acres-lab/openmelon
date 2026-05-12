package tools

// bash.go — the bash tool. Lets the agent run shell commands inside
// the project workdir, gated by a three-tier approval system:
//
//   1. Per-session allowlist  — binaries the user has explicitly
//      "always-approved" this session (e.g. "file", "open", "identify")
//      run without judge or modal.
//   2. Judge LLM (optional)   — classifies into AUTO / ASK / BLOCK.
//      Mode controls who sees the result:
//        strict    AUTO + ASK both prompt; only BLOCK auto-refuses.
//        auto      AUTO runs silently; ASK prompts; BLOCK refuses.
//        trusted   everything runs (judge bypassed entirely).
//   3. User modal             — final fallback. Three options:
//      Yes / Yes always for <binary> / No.
//
// trusted mode is "Claude Code's --dangerously-skip-permissions": no
// approval, model is on the honor system. The user toggles it via the
// /settings panel; we default to strict.

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/eight-acres-lab/openmelon/internal/policy"
)

func bashTool(env *Env) Tool {
	return Tool{
		Spec: Spec{
			Name: "bash",
			Description: "Run a shell command inside the project workdir and return its combined stdout/stderr. " +
				"Use sparingly — for inspecting files (file, ls, du), checking output (open, identify), " +
				"or quick text edits. Do not use bash to discover fonts, render SVG/HTML, compose images, " +
				"or substitute for image generation; typography/style should be handled as continuity context " +
				"and image prompt constraints. Each call is gated by the project's bash permission policy.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"command": {"type": "string", "description": "The shell command to run. Will be passed to /bin/sh -c."},
					"description": {"type": "string", "description": "One-line plain-English explanation of why you're running this. Shown to the user in the approval modal."},
					"timeout_seconds": {"type": "number", "description": "Kill the command after this many seconds. Default 30, max 300."}
				},
				"required": ["command", "description"]
			}`),
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var args struct {
				Command        string  `json:"command"`
				Description    string  `json:"description"`
				TimeoutSeconds float64 `json:"timeout_seconds"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, fmt.Errorf("invalid args: %w", err)
			}
			if args.Command == "" {
				return map[string]any{"error": "command is required"}, nil
			}
			binary := firstBinary(args.Command)
			policyRes := env.policy().Check(ctx, policy.Request{
				Action:      "bash.execute",
				Tool:        "bash",
				Workdir:     env.Workdir,
				Command:     args.Command,
				Description: args.Description,
				Binary:      binary,
			})
			switch policy.NormalizeDecision(policyRes.Decision) {
			case policy.Deny:
				return map[string]any{
					"error": policy.ReasonOrDefault(policyRes.Reason, "blocked by policy"),
				}, nil
			case policy.Allow:
				return runBash(ctx, env.Workdir, args.Command, args.TimeoutSeconds, policy.ReasonOrDefault(policyRes.Reason, "policy"))
			}

			if env.Approve == nil {
				return map[string]any{
					"error": "bash is unavailable: no approval gate is wired (running headless?)",
				}, nil
			}
			decision := env.Approve(ApprovalRequest{
				Tool:        "bash",
				Command:     args.Command,
				Description: args.Description,
				Binary:      binary,
			})
			if !decision.Approved {
				return map[string]any{"error": "user denied execution"}, nil
			}
			if decision.Always && env.AllowBash != nil {
				env.AllowBash(binary)
			}
			return runBash(ctx, env.Workdir, args.Command, args.TimeoutSeconds, "user-approved")
		},
	}
}

// runBash executes the command and returns the structured result the
// model sees as the tool message. via labels how the call was approved
// for provenance ("user-approved", "allowlisted", "judge:auto", "trusted").
func runBash(ctx context.Context, workdir, command string, timeoutSec float64, via string) (any, error) {
	timeout := time.Duration(timeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if timeout > 5*time.Minute {
		timeout = 5 * time.Minute
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(execCtx, "/bin/sh", "-c", command)
	cmd.Dir = workdir
	out, err := cmd.CombinedOutput()
	res := map[string]any{
		"stdout":       string(out),
		"exit_code":    cmd.ProcessState.ExitCode(),
		"approved_via": via,
	}
	if err != nil {
		if execCtx.Err() == context.DeadlineExceeded {
			res["error"] = fmt.Sprintf("timed out after %s", timeout)
		} else if _, isExit := err.(*exec.ExitError); !isExit {
			// exec.ExitError is normal — the model wants exit code
			// + output regardless. Other errors are real failures.
			res["error"] = err.Error()
		}
	}
	return res, nil
}

// firstBinary extracts the first executable name from a shell command.
// Strips leading env assignments, sudo, time, etc., and basenames the
// path so "/usr/bin/file" → "file".
//
// Best-effort: doesn't fully tokenize shell. Used purely for the
// allowlist key + the modal label, so a wrong answer just means the
// user might have to approve a similar command again — never a
// security violation.
func firstBinary(command string) string {
	tokens := strings.Fields(command)
	for _, t := range tokens {
		if t == "" {
			continue
		}
		// Skip env-var assignments like FOO=bar.
		if strings.Contains(t, "=") && !strings.ContainsAny(t, "/\\") {
			continue
		}
		// Skip common wrapper prefixes.
		switch t {
		case "sudo", "time", "exec", "nohup", "env":
			continue
		}
		// Strip path.
		if idx := strings.LastIndexAny(t, "/\\"); idx >= 0 {
			t = t[idx+1:]
		}
		return t
	}
	return ""
}
