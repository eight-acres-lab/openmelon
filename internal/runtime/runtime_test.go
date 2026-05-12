package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/eight-acres-lab/openmelon/internal/hooks"
	"github.com/eight-acres-lab/openmelon/internal/llm"
	"github.com/eight-acres-lab/openmelon/internal/tools"
)

// fakeLLM is a scripted ToolCaller for tests. Each call to Chat returns
// the next pre-recorded response. If we run out of responses, the test
// fails.
type fakeLLM struct {
	t         *testing.T
	responses []llm.ChatResponse
	calls     int
	lastReq   llm.ChatRequest
	requests  []llm.ChatRequest
}

func (f *fakeLLM) Chat(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	if f.calls >= len(f.responses) {
		f.t.Fatalf("fakeLLM ran out of responses after %d calls", f.calls)
	}
	f.lastReq = req
	f.requests = append(f.requests, req)
	r := f.responses[f.calls]
	f.calls++
	return &r, nil
}

func TestRunStopsImmediatelyWhenModelHasNoToolCalls(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{
		Spec: tools.Spec{
			Name:        "noop",
			Description: "no-op",
			Parameters:  json.RawMessage(`{"type":"object"}`),
		},
		Handler: func(_ context.Context, _ json.RawMessage) (any, error) { return "ok", nil },
	})

	llmFake := &fakeLLM{t: t, responses: []llm.ChatResponse{{
		Message:      llm.Message{Role: llm.RoleAssistant, Content: "all done"},
		FinishReason: llm.FinishStop,
	}}}

	rt := &Runtime{LLM: llmFake, Registry: reg}
	res, err := rt.Run(context.Background(), RunInput{SystemPrompt: "be terse", UserInput: "hi"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Finished {
		t.Errorf("expected Finished=true")
	}
	if res.Steps != 1 {
		t.Errorf("expected 1 step, got %d", res.Steps)
	}
	// Tools were forwarded to the model.
	if len(llmFake.lastReq.Tools) != 1 || llmFake.lastReq.Tools[0].Name != "noop" {
		t.Errorf("tools not forwarded: %+v", llmFake.lastReq.Tools)
	}
}

func TestRunDispatchesToolCallsAndFeedsResultsBack(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{
		Spec: tools.Spec{
			Name:        "echo",
			Description: "echo",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}}}`),
		},
		Handler: func(_ context.Context, raw json.RawMessage) (any, error) {
			var args struct{ Text string }
			_ = json.Unmarshal(raw, &args)
			return map[string]any{"echoed": args.Text}, nil
		},
	})

	llmFake := &fakeLLM{t: t, responses: []llm.ChatResponse{
		{
			Message: llm.Message{
				Role: llm.RoleAssistant,
				ToolCalls: []llm.ToolCall{{
					ID: "call-1", Name: "echo",
					Arguments: json.RawMessage(`{"text":"hello"}`),
				}},
			},
			FinishReason: llm.FinishToolCalls,
		},
		{
			Message:      llm.Message{Role: llm.RoleAssistant, Content: "got it"},
			FinishReason: llm.FinishStop,
		},
	}}

	rt := &Runtime{LLM: llmFake, Registry: reg}
	res, err := rt.Run(context.Background(), RunInput{SystemPrompt: "x", UserInput: "go"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Steps != 2 {
		t.Errorf("expected 2 steps, got %d", res.Steps)
	}

	// Conversation should be: system, user, assistant(tool_call), tool, assistant(stop)
	if len(res.Messages) != 5 {
		t.Fatalf("expected 5 messages, got %d: %+v", len(res.Messages), res.Messages)
	}
	if res.Messages[3].Role != llm.RoleTool || res.Messages[3].ToolCallID != "call-1" {
		t.Errorf("tool reply mismatched: %+v", res.Messages[3])
	}
	if !strings.Contains(res.Messages[3].Content, `"echoed":"hello"`) {
		t.Errorf("tool reply content: %q", res.Messages[3].Content)
	}
}

func TestRunDrainsUserInputBeforeEachModelCall(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{
		Spec: tools.Spec{
			Name:        "echo",
			Description: "echo",
			Parameters:  json.RawMessage(`{"type":"object"}`),
		},
		Handler: func(_ context.Context, _ json.RawMessage) (any, error) {
			return map[string]any{"ok": true}, nil
		},
	})

	llmFake := &fakeLLM{t: t, responses: []llm.ChatResponse{
		{
			Message: llm.Message{
				Role:      llm.RoleAssistant,
				ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "echo", Arguments: json.RawMessage(`{}`)}},
			},
			FinishReason: llm.FinishToolCalls,
		},
		{
			Message:      llm.Message{Role: llm.RoleAssistant, Content: "updated"},
			FinishReason: llm.FinishStop,
		},
	}}

	drains := 0
	rt := &Runtime{
		LLM:      llmFake,
		Registry: reg,
		DrainUserInput: func() []string {
			drains++
			if drains == 1 {
				return []string{"First queued context."}
			}
			if drains == 2 {
				return []string{"Actually, make it shorter."}
			}
			return nil
		},
	}
	res, err := rt.Run(context.Background(), RunInput{SystemPrompt: "x", UserInput: "go"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(llmFake.requests) != 2 {
		t.Fatalf("expected 2 model requests, got %d", len(llmFake.requests))
	}
	first := llmFake.requests[0].Messages
	if got := first[len(first)-1]; got.Role != llm.RoleUser || got.Content != "First queued context." {
		t.Fatalf("first drained input not appended before first model call: %+v", got)
	}
	second := llmFake.requests[1].Messages
	if len(second) < 5 {
		t.Fatalf("second request too short: %+v", second)
	}
	got := second[len(second)-1]
	if got.Role != llm.RoleUser || got.Content != "Actually, make it shorter." {
		t.Fatalf("drained input not appended before second model call: %+v", got)
	}
	if len(res.Messages) == 0 || res.Messages[len(res.Messages)-2].Content != "Actually, make it shorter." {
		t.Fatalf("result history missing drained input: %+v", res.Messages)
	}
}

func TestRunSurfacesToolErrorAsContentSoModelCanRecover(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{
		Spec: tools.Spec{
			Name: "boom", Description: "x",
			Parameters: json.RawMessage(`{"type":"object"}`),
		},
		Handler: func(_ context.Context, _ json.RawMessage) (any, error) {
			return nil, errFake("explicit failure")
		},
	})

	llmFake := &fakeLLM{t: t, responses: []llm.ChatResponse{
		{
			Message: llm.Message{
				Role:      llm.RoleAssistant,
				ToolCalls: []llm.ToolCall{{ID: "x", Name: "boom", Arguments: json.RawMessage(`{}`)}},
			},
			FinishReason: llm.FinishToolCalls,
		},
		{
			Message:      llm.Message{Role: llm.RoleAssistant, Content: "stopping"},
			FinishReason: llm.FinishStop,
		},
	}}

	rt := &Runtime{LLM: llmFake, Registry: reg}
	res, err := rt.Run(context.Background(), RunInput{SystemPrompt: "x", UserInput: "go"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// The tool error must reach the model as a JSON tool message.
	toolMsg := res.Messages[3]
	if toolMsg.Role != llm.RoleTool {
		t.Fatalf("expected tool message at [3], got %+v", toolMsg)
	}
	if !strings.Contains(toolMsg.Content, "explicit failure") {
		t.Errorf("tool error not surfaced: %q", toolMsg.Content)
	}
}

func TestRunMarksToolResultErrorFieldAsFailure(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{
		Spec: tools.Spec{
			Name: "soft_fail", Description: "x",
			Parameters: json.RawMessage(`{"type":"object"}`),
		},
		Handler: func(_ context.Context, _ json.RawMessage) (any, error) {
			return map[string]any{"error": "provider rejected request"}, nil
		},
	})

	llmFake := &fakeLLM{t: t, responses: []llm.ChatResponse{
		{
			Message: llm.Message{
				Role:      llm.RoleAssistant,
				ToolCalls: []llm.ToolCall{{ID: "x", Name: "soft_fail", Arguments: json.RawMessage(`{}`)}},
			},
			FinishReason: llm.FinishToolCalls,
		},
		{
			Message:      llm.Message{Role: llm.RoleAssistant, Content: "recovered"},
			FinishReason: llm.FinishStop,
		},
	}}
	tracer := &captureTracer{}
	var hookErr error
	rt := &Runtime{
		LLM:      llmFake,
		Registry: reg,
		Tracer:   tracer,
		Hooks: &scriptedHooks{
			afterTool: func(_ context.Context, e hooks.ToolResultEvent) hooks.HookResult {
				hookErr = e.Err
				return hooks.HookResult{}
			},
		},
	}

	res, err := rt.Run(context.Background(), RunInput{SystemPrompt: "x", UserInput: "go"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(res.Messages[3].Content, "provider rejected request") {
		t.Fatalf("tool error content not preserved: %q", res.Messages[3].Content)
	}
	if hookErr == nil || !strings.Contains(hookErr.Error(), "provider rejected request") {
		t.Fatalf("hook did not receive tool error: %v", hookErr)
	}
	if len(tracer.errors) == 0 || tracer.errors[0] == nil || !strings.Contains(tracer.errors[0].Error(), "provider rejected request") {
		t.Fatalf("tracer did not receive tool error: %#v", tracer.errors)
	}
}

func TestRunLifecycleHooksCanRewriteAndDenyToolCalls(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{
		Spec: tools.Spec{
			Name:        "echo",
			Description: "echo",
			Parameters:  json.RawMessage(`{"type":"object"}`),
		},
		Handler: func(_ context.Context, raw json.RawMessage) (any, error) {
			var args struct{ Text string }
			_ = json.Unmarshal(raw, &args)
			return map[string]any{"echoed": args.Text}, nil
		},
	})
	llmFake := &fakeLLM{t: t, responses: []llm.ChatResponse{
		{
			Message: llm.Message{
				Role:      llm.RoleAssistant,
				ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "echo", Arguments: json.RawMessage(`{"text":"original"}`)}},
			},
			FinishReason: llm.FinishToolCalls,
		},
		{
			Message:      llm.Message{Role: llm.RoleAssistant, Content: "done"},
			FinishReason: llm.FinishStop,
		},
	}}
	h := &scriptedHooks{
		beforeTool: func(_ context.Context, e hooks.ToolCallEvent) hooks.HookResult {
			return hooks.HookResult{RewriteToolArguments: json.RawMessage(`{"text":"rewritten"}`)}
		},
		afterTool: func(_ context.Context, e hooks.ToolResultEvent) hooks.HookResult {
			return hooks.HookResult{AppendUserFeedback: []string{"hook feedback"}}
		},
	}
	rt := &Runtime{LLM: llmFake, Registry: reg, Hooks: h}
	res, err := rt.Run(context.Background(), RunInput{SystemPrompt: "x", UserInput: "go"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(res.Messages[3].Content, "rewritten") {
		t.Fatalf("tool args were not rewritten: %+v", res.Messages[3])
	}
	second := llmFake.requests[1].Messages
	if got := second[len(second)-1]; got.Role != llm.RoleUser || got.Content != "hook feedback" {
		t.Fatalf("hook feedback not appended before next model call: %+v", got)
	}
}

func TestRunHookDenyToolCallReturnsToolError(t *testing.T) {
	reg := tools.NewRegistry()
	called := false
	reg.Register(tools.Tool{
		Spec: tools.Spec{Name: "echo", Description: "echo", Parameters: json.RawMessage(`{"type":"object"}`)},
		Handler: func(_ context.Context, _ json.RawMessage) (any, error) {
			called = true
			return nil, nil
		},
	})
	llmFake := &fakeLLM{t: t, responses: []llm.ChatResponse{
		{
			Message: llm.Message{
				Role:      llm.RoleAssistant,
				ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "echo", Arguments: json.RawMessage(`{}`)}},
			},
			FinishReason: llm.FinishToolCalls,
		},
		{
			Message:      llm.Message{Role: llm.RoleAssistant, Content: "done"},
			FinishReason: llm.FinishStop,
		},
	}}
	rt := &Runtime{
		LLM:      llmFake,
		Registry: reg,
		Hooks: &scriptedHooks{beforeTool: func(_ context.Context, e hooks.ToolCallEvent) hooks.HookResult {
			return hooks.HookResult{Decision: hooks.Deny, Reason: "not allowed"}
		}},
	}
	res, err := rt.Run(context.Background(), RunInput{SystemPrompt: "x", UserInput: "go"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if called {
		t.Fatal("denied tool handler was called")
	}
	if !strings.Contains(res.Messages[3].Content, "not allowed") {
		t.Fatalf("denial not returned as tool content: %+v", res.Messages[3])
	}
}

func TestRunStopsOnFinishTool(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{
		Spec: tools.Spec{
			Name: "finish", Description: "done",
			Parameters: json.RawMessage(`{"type":"object","properties":{"summary":{"type":"string"}}}`),
		},
		Handler: func(_ context.Context, raw json.RawMessage) (any, error) {
			var a struct{ Summary string }
			_ = json.Unmarshal(raw, &a)
			return map[string]any{"summary": a.Summary, "ok": true}, nil
		},
	})

	llmFake := &fakeLLM{t: t, responses: []llm.ChatResponse{{
		Message: llm.Message{
			Role: llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{{
				ID: "f", Name: "finish", Arguments: json.RawMessage(`{"summary":"all done"}`),
			}},
		},
		FinishReason: llm.FinishToolCalls,
	}}}

	rt := &Runtime{LLM: llmFake, Registry: reg}
	res, err := rt.Run(context.Background(), RunInput{SystemPrompt: "x", UserInput: "go"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Finished {
		t.Error("expected Finished=true")
	}
	if res.FinishSummary != "all done" {
		t.Errorf("summary: %q", res.FinishSummary)
	}
	// Loop did not run a second LLM turn after finish.
	if llmFake.calls != 1 {
		t.Errorf("expected 1 LLM call, got %d", llmFake.calls)
	}
}

func TestRunReturnsErrorWhenMaxStepsExceeded(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{
		Spec: tools.Spec{
			Name: "loop", Description: "x",
			Parameters: json.RawMessage(`{"type":"object"}`),
		},
		Handler: func(_ context.Context, _ json.RawMessage) (any, error) { return "ok", nil },
	})

	// Always return tool_calls — the model never finishes.
	llmFake := &fakeLLM{t: t}
	for i := 0; i < 5; i++ {
		llmFake.responses = append(llmFake.responses, llm.ChatResponse{
			Message: llm.Message{
				Role: llm.RoleAssistant,
				ToolCalls: []llm.ToolCall{{
					ID: "x", Name: "loop", Arguments: json.RawMessage(`{}`),
				}},
			},
			FinishReason: llm.FinishToolCalls,
		})
	}

	rt := &Runtime{LLM: llmFake, Registry: reg, MaxSteps: 3}
	_, err := rt.Run(context.Background(), RunInput{SystemPrompt: "x", UserInput: "go"})
	if err == nil || !strings.Contains(err.Error(), "MaxSteps") {
		t.Errorf("expected MaxSteps error, got %v", err)
	}
}

type errFake string

func (e errFake) Error() string { return string(e) }

type scriptedHooks struct {
	hooks.NoopManager
	beforeModel func(context.Context, hooks.ModelRequestEvent) hooks.HookResult
	afterModel  func(context.Context, hooks.ModelResponseEvent) hooks.HookResult
	beforeTool  func(context.Context, hooks.ToolCallEvent) hooks.HookResult
	afterTool   func(context.Context, hooks.ToolResultEvent) hooks.HookResult
}

func (h *scriptedHooks) BeforeModelRequest(ctx context.Context, e hooks.ModelRequestEvent) hooks.HookResult {
	if h.beforeModel != nil {
		return h.beforeModel(ctx, e)
	}
	return hooks.HookResult{}
}

func (h *scriptedHooks) AfterModelResponse(ctx context.Context, e hooks.ModelResponseEvent) hooks.HookResult {
	if h.afterModel != nil {
		return h.afterModel(ctx, e)
	}
	return hooks.HookResult{}
}

func (h *scriptedHooks) BeforeToolCall(ctx context.Context, e hooks.ToolCallEvent) hooks.HookResult {
	if h.beforeTool != nil {
		return h.beforeTool(ctx, e)
	}
	return hooks.HookResult{}
}

func (h *scriptedHooks) AfterToolCall(ctx context.Context, e hooks.ToolResultEvent) hooks.HookResult {
	if h.afterTool != nil {
		return h.afterTool(ctx, e)
	}
	return hooks.HookResult{}
}

func TestRunWithHistoryAppendsUserAndPreservesPriorMessages(t *testing.T) {
	reg := tools.NewRegistry()
	llmFake := &fakeLLM{t: t, responses: []llm.ChatResponse{{
		Message:      llm.Message{Role: llm.RoleAssistant, Content: "ack"},
		FinishReason: llm.FinishStop,
	}}}

	prior := []llm.Message{
		{Role: llm.RoleSystem, Content: "be terse"},
		{Role: llm.RoleUser, Content: "first"},
		{Role: llm.RoleAssistant, Content: "first reply"},
	}
	rt := &Runtime{LLM: llmFake, Registry: reg}
	res, err := rt.Run(context.Background(), RunInput{
		History:   prior,
		UserInput: "second",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Wire request must include all 3 prior + the new user message.
	if len(llmFake.lastReq.Messages) != 4 {
		t.Fatalf("expected 4 messages sent, got %d", len(llmFake.lastReq.Messages))
	}
	if llmFake.lastReq.Messages[3].Content != "second" || llmFake.lastReq.Messages[3].Role != llm.RoleUser {
		t.Errorf("user message not appended: %+v", llmFake.lastReq.Messages[3])
	}
	// SystemPrompt must NOT be re-injected when History is non-empty.
	if llmFake.lastReq.Messages[0].Content != "be terse" {
		t.Errorf("system prompt clobbered: %+v", llmFake.lastReq.Messages[0])
	}
	// Final result includes the new user + new assistant.
	if len(res.Messages) != 5 {
		t.Errorf("expected 5 result messages, got %d", len(res.Messages))
	}
}

// captureTracer is a Tracer that records every event for assertions.
type captureTracer struct {
	turns      []int
	textDeltas []string
	calls      []llm.ToolCall
	results    []string
	errors     []error
}

func (c *captureTracer) OnTurnStart(turn int)       { c.turns = append(c.turns, turn) }
func (c *captureTracer) OnText(d string)            { c.textDeltas = append(c.textDeltas, d) }
func (c *captureTracer) OnToolCall(tc llm.ToolCall) { c.calls = append(c.calls, tc) }
func (c *captureTracer) OnToolResult(tc llm.ToolCall, content string, err error) {
	c.results = append(c.results, content)
	c.errors = append(c.errors, err)
}
func (c *captureTracer) OnTurnEnd(turn int, _ llm.FinishReason, _ llm.Usage) {}

func TestTracerReceivesAllEvents(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{
		Spec: tools.Spec{
			Name: "echo", Description: "x",
			Parameters: json.RawMessage(`{"type":"object"}`),
		},
		Handler: func(_ context.Context, _ json.RawMessage) (any, error) {
			return map[string]any{"ok": true}, nil
		},
	})

	llmFake := &fakeLLM{t: t, responses: []llm.ChatResponse{
		{
			Message: llm.Message{
				Role:      llm.RoleAssistant,
				Content:   "thinking...",
				ToolCalls: []llm.ToolCall{{ID: "x", Name: "echo", Arguments: json.RawMessage(`{}`)}},
			},
			FinishReason: llm.FinishToolCalls,
		},
		{
			Message:      llm.Message{Role: llm.RoleAssistant, Content: "all done"},
			FinishReason: llm.FinishStop,
		},
	}}

	tr := &captureTracer{}
	rt := &Runtime{LLM: llmFake, Registry: reg, Tracer: tr}
	if _, err := rt.Run(context.Background(), RunInput{SystemPrompt: "x", UserInput: "go"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(tr.turns) != 2 || tr.turns[0] != 1 || tr.turns[1] != 2 {
		t.Errorf("turns: %v", tr.turns)
	}
	// Non-streaming fakeLLM still fires OnText once per non-empty turn.
	if !strings.Contains(strings.Join(tr.textDeltas, ""), "thinking...") {
		t.Errorf("text deltas missing 'thinking...': %v", tr.textDeltas)
	}
	if !strings.Contains(strings.Join(tr.textDeltas, ""), "all done") {
		t.Errorf("text deltas missing 'all done': %v", tr.textDeltas)
	}
	if len(tr.calls) != 1 || tr.calls[0].Name != "echo" {
		t.Errorf("tool calls: %+v", tr.calls)
	}
	if len(tr.results) != 1 || !strings.Contains(tr.results[0], `"ok":true`) {
		t.Errorf("tool results: %+v", tr.results)
	}
}
