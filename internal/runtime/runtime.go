// Package runtime is openmelon's tool-driven agent loop.
//
// The loop is a small, classic ReAct-style cycle:
//
//  1. Send (system prompt, conversation, tools) to the LLM.
//  2. LLM replies with either text + tool_calls (FinishToolCalls) or
//     plain text (FinishStop).
//  3. For each tool_call, dispatch via tools.Registry, append the
//     result back as a tool message.
//  4. Loop until: the model finishes naturally, calls the special
//     `finish` tool, or hits MaxSteps.
//
// The runtime is provider-agnostic: anything implementing llm.ToolCaller
// works. When the underlying client also implements llm.StreamingToolCaller
// the runtime uses the streaming path so the REPL can render text as it
// arrives.
package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/eight-acres-lab/openmelon/internal/hooks"
	"github.com/eight-acres-lab/openmelon/internal/llm"
	"github.com/eight-acres-lab/openmelon/internal/tools"
)

// Defaults applied when the caller doesn't override.
const (
	DefaultMaxSteps = 16
)

// Tracer receives structured events as the loop runs.
//
// Implementations render however they like — the REPL prints to a
// terminal, tests collect events into a slice, the legacy plain-stderr
// trace uses an io.Writer adapter (TraceWriter).
//
// All callbacks may be nil-safe: the runtime checks for a nil Tracer
// before calling and never panics on a partial implementation.
type Tracer interface {
	OnTurnStart(turn int)
	OnText(delta string) // streamed when the underlying client supports it; otherwise fired once with the full text
	OnToolCall(call llm.ToolCall)
	OnToolResult(call llm.ToolCall, content string, err error)
	OnTurnEnd(turn int, finish llm.FinishReason, usage llm.Usage)
}

// Runtime is the agent loop.
type Runtime struct {
	LLM      llm.ToolCaller
	Registry *tools.Registry

	// Tracer, if non-nil, receives structured per-turn events.
	Tracer Tracer

	// Hooks, if non-nil, can observe or gate model requests, model
	// responses, and tool calls. Hooks are part of the agent lifecycle;
	// Tracer is only presentation.
	Hooks hooks.Manager

	// DrainUserInput, when non-nil, is called immediately before each
	// model request after the initial user message is seeded. Returned
	// strings are appended as user messages before that request. This
	// lets interactive surfaces accept user corrections while a tool
	// loop is running and feed them into the next model call.
	DrainUserInput func() []string

	// Trace, if non-nil, receives one human-readable line per loop step.
	// Legacy compatibility — kept for cmd/openmelon's headless agent
	// path. Prefer Tracer for new code.
	Trace io.Writer

	// MaxSteps caps how many model+tool round-trips the loop will run
	// before giving up. 0 → DefaultMaxSteps.
	MaxSteps int

	// ReasoningEffort is passed through to providers that expose a
	// thinking-depth knob. Empty means the provider/model default.
	ReasoningEffort string
}

// RunInput is one end-to-end agent run.
type RunInput struct {
	// SystemPrompt sets the agent's behavior + project context. Sent
	// only when History is empty (otherwise the system prompt already
	// lives at History[0]).
	SystemPrompt string

	// UserInput is the user's request for this run. Always appended
	// after History.
	UserInput string

	// History is the prior conversation, including any tool messages.
	// Pass back RunResult.Messages from a previous Run to continue
	// where you left off — that's how the REPL implements multi-turn.
	// When non-empty, SystemPrompt is ignored (the system message is
	// assumed to already be at History[0]).
	History []llm.Message

	// Temperature overrides the model's default. 0 → vendor default.
	Temperature float64

	// MaxTokens caps each turn's reply. 0 → vendor default.
	MaxTokens int
}

// RunResult summarizes one loop run.
type RunResult struct {
	// Messages is the full conversation history, including all tool
	// calls + tool replies. Pass back as RunInput.History to continue.
	Messages []llm.Message

	// Steps is the number of LLM round-trips taken in this Run call
	// (NOT cumulative across continuations).
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

	// Seed the message list. New conversations get system + user;
	// continuations get history + user.
	var messages []llm.Message
	if len(in.History) > 0 {
		messages = append(messages, in.History...)
	} else if in.SystemPrompt != "" {
		messages = append(messages, llm.Message{Role: llm.RoleSystem, Content: in.SystemPrompt})
	}
	if in.UserInput != "" {
		messages = append(messages, llm.Message{Role: llm.RoleUser, Content: in.UserInput})
	}

	// Detect streaming support.
	streamer, _ := r.LLM.(llm.StreamingToolCaller)

	out := &RunResult{}
	for step := 0; step < maxSteps; step++ {
		out.Steps = step + 1
		messages = appendDrainedUserInput(messages, r.DrainUserInput)
		r.onTurnStart(step + 1)
		hr := r.beforeModelRequest(ctx, step+1, messages, wireTools)
		switch hr.EffectiveDecision() {
		case hooks.Deny, hooks.Cancel:
			out.Messages = messages
			return out, fmt.Errorf("runtime: model request blocked by hook: %s", hr.Reason)
		}
		messages = appendUserFeedback(messages, hr.AppendUserFeedback)
		req := llm.ChatRequest{
			Messages:        messages,
			Tools:           wireTools,
			Temperature:     in.Temperature,
			MaxTokens:       in.MaxTokens,
			ReasoningEffort: r.ReasoningEffort,
		}

		var resp *llm.ChatResponse
		var err error
		if streamer != nil {
			resp, err = streamer.StreamChat(ctx, req, llm.StreamChatHandler{
				OnText: func(d string) { r.onText(d) },
			})
		} else {
			resp, err = r.LLM.Chat(ctx, req)
			if err == nil && resp.Message.Content != "" {
				// Fire OnText once with the full body so non-streaming
				// callers still see the model's reply.
				r.onText(resp.Message.Content)
			}
		}
		if err != nil {
			return out, fmt.Errorf("runtime: chat (step %d): %w", step+1, err)
		}
		messages = append(messages, resp.Message)
		var pendingHookFeedback []string
		hr = r.afterModelResponse(ctx, step+1, resp)
		switch hr.EffectiveDecision() {
		case hooks.Deny, hooks.Cancel:
			out.Messages = messages
			return out, fmt.Errorf("runtime: model response blocked by hook: %s", hr.Reason)
		}
		pendingHookFeedback = append(pendingHookFeedback, hr.AppendUserFeedback...)
		r.legacyTracef("[turn %d] reply (finish=%s, tool_calls=%d)", step+1, resp.FinishReason, len(resp.Message.ToolCalls))

		if len(resp.Message.ToolCalls) == 0 {
			messages = appendUserFeedback(messages, pendingHookFeedback)
			r.onTurnEnd(step+1, resp.FinishReason, resp.Usage)
			out.Messages = messages
			out.Finished = resp.FinishReason == llm.FinishStop || resp.FinishReason == llm.FinishOther
			return out, nil
		}

		// Dispatch each tool call and append the result.
		var hitFinish bool
		for _, tc := range resp.Message.ToolCalls {
			var res any
			var dispatchErr error
			hr := r.beforeToolCall(ctx, step+1, tc)
			if len(hr.RewriteToolArguments) > 0 {
				tc.Arguments = hr.RewriteToolArguments
			}
			r.onToolCall(tc)
			r.legacyTracef("[turn %d] → %s(%s)", step+1, tc.Name, truncate(string(tc.Arguments), 240))
			if hr.EffectiveDecision() == hooks.Deny || hr.EffectiveDecision() == hooks.Cancel {
				dispatchErr = fmt.Errorf("blocked by hook: %s", hr.Reason)
			} else {
				res, dispatchErr = r.Registry.Dispatch(ctx, tc.Name, tc.Arguments)
			}
			var content string
			switch {
			case dispatchErr != nil:
				b, _ := json.Marshal(map[string]string{"error": dispatchErr.Error()})
				content = string(b)
			default:
				b, mErr := json.Marshal(res)
				if mErr != nil {
					b, _ = json.Marshal(map[string]string{"error": "tool result not serializable: " + mErr.Error()})
				}
				content = string(b)
			}
			hr = r.afterToolCall(ctx, step+1, tc, content, dispatchErr)
			pendingHookFeedback = append(pendingHookFeedback, hr.AppendUserFeedback...)
			r.onToolResult(tc, content, dispatchErr)
			r.legacyTracef("[turn %d] ← %s", step+1, truncate(content, 240))
			messages = append(messages, llm.Message{
				Role:       llm.RoleTool,
				ToolCallID: tc.ID,
				Content:    content,
			})

			if tc.Name == "finish" && dispatchErr == nil {
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
		messages = appendUserFeedback(messages, pendingHookFeedback)
		r.onTurnEnd(step+1, resp.FinishReason, resp.Usage)
		if hitFinish {
			out.Messages = messages
			out.Finished = true
			return out, nil
		}
	}

	out.Messages = messages
	return out, fmt.Errorf("runtime: hit MaxSteps=%d without finishing", maxSteps)
}

func appendDrainedUserInput(messages []llm.Message, drain func() []string) []llm.Message {
	if drain == nil {
		return messages
	}
	for _, text := range drain() {
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		messages = append(messages, llm.Message{Role: llm.RoleUser, Content: text})
	}
	return messages
}

func appendUserFeedback(messages []llm.Message, feedback []string) []llm.Message {
	for _, text := range feedback {
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		messages = append(messages, llm.Message{Role: llm.RoleUser, Content: text})
	}
	return messages
}

// --- tracer + legacy-writer fan-out ---

func (r *Runtime) onTurnStart(turn int) {
	if r.Tracer != nil {
		r.Tracer.OnTurnStart(turn)
	}
}

func (r *Runtime) onText(delta string) {
	if r.Tracer != nil {
		r.Tracer.OnText(delta)
	}
}

func (r *Runtime) onToolCall(tc llm.ToolCall) {
	if r.Tracer != nil {
		r.Tracer.OnToolCall(tc)
	}
}

func (r *Runtime) onToolResult(tc llm.ToolCall, content string, err error) {
	if r.Tracer != nil {
		r.Tracer.OnToolResult(tc, content, err)
	}
}

func (r *Runtime) onTurnEnd(turn int, finish llm.FinishReason, usage llm.Usage) {
	if r.Tracer != nil {
		r.Tracer.OnTurnEnd(turn, finish, usage)
	}
}

func (r *Runtime) beforeModelRequest(ctx context.Context, step int, messages []llm.Message, tools []llm.Tool) hooks.HookResult {
	if r.Hooks == nil {
		return hooks.HookResult{}
	}
	return r.Hooks.BeforeModelRequest(ctx, hooks.ModelRequestEvent{
		Step:     step,
		Messages: append([]llm.Message(nil), messages...),
		Tools:    append([]llm.Tool(nil), tools...),
	})
}

func (r *Runtime) afterModelResponse(ctx context.Context, step int, resp *llm.ChatResponse) hooks.HookResult {
	if r.Hooks == nil || resp == nil {
		return hooks.HookResult{}
	}
	return r.Hooks.AfterModelResponse(ctx, hooks.ModelResponseEvent{Step: step, Response: *resp})
}

func (r *Runtime) beforeToolCall(ctx context.Context, step int, tc llm.ToolCall) hooks.HookResult {
	if r.Hooks == nil {
		return hooks.HookResult{}
	}
	return r.Hooks.BeforeToolCall(ctx, hooks.ToolCallEvent{Step: step, Call: tc})
}

func (r *Runtime) afterToolCall(ctx context.Context, step int, tc llm.ToolCall, content string, err error) hooks.HookResult {
	if r.Hooks == nil {
		return hooks.HookResult{}
	}
	return r.Hooks.AfterToolCall(ctx, hooks.ToolResultEvent{Step: step, Call: tc, Content: content, Err: err})
}

func (r *Runtime) legacyTracef(format string, args ...any) {
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
