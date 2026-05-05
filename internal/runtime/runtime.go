// Package runtime is openmelon's tool-driven agent loop.
//
// The loop is a small, classic ReAct-style cycle:
//
//	1. Send (system prompt, conversation, tools) to the LLM.
//	2. LLM replies with either text + tool_calls (FinishToolCalls) or
//	   plain text (FinishStop).
//	3. For each tool_call, dispatch via tools.Registry, append the
//	   result back as a tool message.
//	4. Loop until: the model finishes naturally, calls the special
//	   `finish` tool, or hits MaxSteps.
//
// The runtime is provider-agnostic: anything implementing llm.ToolCaller
// works. Streaming text deltas inside one turn are out of scope here —
// see runtime/stream.go (later) for that.
package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/eight-acres-lab/openmelon/internal/llm"
	"github.com/eight-acres-lab/openmelon/internal/tools"
)

// Defaults applied when the caller doesn't override.
const (
	DefaultMaxSteps = 16
)

// Runtime is the agent loop.
type Runtime struct {
	LLM      llm.ToolCaller
	Registry *tools.Registry

	// Trace, if non-nil, receives one human-readable line per loop step
	// (model reply, tool call, tool result). cmd/openmelon wires this
	// to os.Stderr so the user can watch the agent think.
	Trace io.Writer

	// MaxSteps caps how many model+tool round-trips the loop will run
	// before giving up. 0 → DefaultMaxSteps.
	MaxSteps int
}

// RunInput is one end-to-end agent run.
type RunInput struct {
	// SystemPrompt sets the agent's behavior + project context. Sent
	// once as the first message.
	SystemPrompt string

	// UserInput is the user's request. Sent as the first user message.
	UserInput string

	// Temperature overrides the model's default. 0 → vendor default.
	Temperature float64

	// MaxTokens caps each turn's reply. 0 → vendor default.
	MaxTokens int
}

// RunResult summarizes one loop run.
type RunResult struct {
	// Messages is the full conversation history, including all tool
	// calls + tool replies. The session writer can persist it.
	Messages []llm.Message

	// Steps is the number of LLM round-trips taken.
	Steps int

	// Finished is true when the loop exited via `finish` or
	// FinishStop. False means MaxSteps cap or loop error.
	Finished bool

	// FinishSummary is set when the loop exited via the `finish`
	// tool — that tool's "summary" argument.
	FinishSummary string

	// FinishArtifacts is set similarly — paths reported by `finish`.
	FinishArtifacts []string
}

// Run drives the loop end-to-end.
func (r *Runtime) Run(ctx context.Context, in RunInput) (*RunResult, error) {
	if r.LLM == nil {
		return nil, fmt.Errorf("runtime: LLM is required")
	}
	if r.Registry == nil {
		return nil, fmt.Errorf("runtime: Registry is required")
	}
	maxSteps := r.MaxSteps
	if maxSteps <= 0 {
		maxSteps = DefaultMaxSteps
	}

	specs := r.Registry.Specs()
	wireTools := make([]llm.Tool, 0, len(specs))
	for _, s := range specs {
		wireTools = append(wireTools, llm.Tool{
			Name:        s.Name,
			Description: s.Description,
			Parameters:  s.Parameters,
		})
	}

	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: in.SystemPrompt},
		{Role: llm.RoleUser, Content: in.UserInput},
	}

	out := &RunResult{}
	for step := 0; step < maxSteps; step++ {
		out.Steps = step + 1
		req := llm.ChatRequest{
			Messages:    messages,
			Tools:       wireTools,
			Temperature: in.Temperature,
			MaxTokens:   in.MaxTokens,
		}
		resp, err := r.LLM.Chat(ctx, req)
		if err != nil {
			return out, fmt.Errorf("runtime: chat (step %d): %w", step+1, err)
		}
		messages = append(messages, resp.Message)
		r.tracef("[turn %d] reply (finish=%s, tool_calls=%d)", step+1, resp.FinishReason, len(resp.Message.ToolCalls))
		if resp.Message.Content != "" {
			r.tracef("[turn %d] text: %s", step+1, truncate(resp.Message.Content, 240))
		}

		if len(resp.Message.ToolCalls) == 0 {
			// Model finished without calling tools — done.
			out.Messages = messages
			out.Finished = resp.FinishReason == llm.FinishStop || resp.FinishReason == llm.FinishOther
			return out, nil
		}

		// Dispatch each tool call and append the result.
		var hitFinish bool
		for _, tc := range resp.Message.ToolCalls {
			r.tracef("[turn %d] → %s(%s)", step+1, tc.Name, truncate(string(tc.Arguments), 240))
			res, err := r.Registry.Dispatch(ctx, tc.Name, tc.Arguments)
			var content string
			switch {
			case err != nil:
				// Surface as a structured error the model can read.
				b, _ := json.Marshal(map[string]string{"error": err.Error()})
				content = string(b)
			default:
				b, mErr := json.Marshal(res)
				if mErr != nil {
					b, _ = json.Marshal(map[string]string{"error": "tool result not serializable: " + mErr.Error()})
				}
				content = string(b)
			}
			r.tracef("[turn %d] ← %s", step+1, truncate(content, 240))
			messages = append(messages, llm.Message{
				Role:       llm.RoleTool,
				ToolCallID: tc.ID,
				Content:    content,
			})

			if tc.Name == "finish" {
				if m, ok := res.(map[string]any); ok {
					if s, _ := m["summary"].(string); s != "" {
						out.FinishSummary = s
					}
					if arts, ok := m["artifacts"].([]string); ok {
						out.FinishArtifacts = arts
					}
				}
				hitFinish = true
			}
		}
		if hitFinish {
			out.Messages = messages
			out.Finished = true
			return out, nil
		}
	}

	out.Messages = messages
	return out, fmt.Errorf("runtime: hit MaxSteps=%d without finishing", maxSteps)
}

func (r *Runtime) tracef(format string, args ...any) {
	if r.Trace == nil {
		return
	}
	fmt.Fprintf(r.Trace, format+"\n", args...)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
