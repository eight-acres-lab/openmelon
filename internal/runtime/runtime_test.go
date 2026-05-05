package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

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
}

func (f *fakeLLM) Chat(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	if f.calls >= len(f.responses) {
		f.t.Fatalf("fakeLLM ran out of responses after %d calls", f.calls)
	}
	f.lastReq = req
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
				Role: llm.RoleAssistant,
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
