package session

import (
	"context"

	"github.com/eight-acres-lab/openmelon/internal/hooks"
)

type sessionHookRecorder struct {
	hooks.NoopManager
	s *Session
}

func (r sessionHookRecorder) BeforeModelRequest(_ context.Context, e hooks.ModelRequestEvent) hooks.HookResult {
	_ = r.s.AppendEvent("model_request", EventRecord{
		Step:   e.Step,
		Status: "before",
		Detail: map[string]any{
			"messages": len(e.Messages),
			"tools":    len(e.Tools),
		},
	})
	return hooks.HookResult{}
}

func (r sessionHookRecorder) AfterModelResponse(_ context.Context, e hooks.ModelResponseEvent) hooks.HookResult {
	_ = r.s.AppendEvent("model_response", EventRecord{
		Step:   e.Step,
		Status: string(e.Response.FinishReason),
		Detail: map[string]any{
			"tool_calls":        len(e.Response.Message.ToolCalls),
			"content_chars":     len(e.Response.Message.Content),
			"prompt_tokens":     e.Response.Usage.PromptTokens,
			"completion_tokens": e.Response.Usage.CompletionTokens,
		},
	})
	return hooks.HookResult{}
}

func (r sessionHookRecorder) BeforeToolCall(_ context.Context, e hooks.ToolCallEvent) hooks.HookResult {
	_ = r.s.AppendEvent("tool_call", EventRecord{
		Step:   e.Step,
		Tool:   e.Call.Name,
		Status: "before",
		Detail: map[string]any{
			"tool_call_id": e.Call.ID,
		},
	})
	return hooks.HookResult{}
}

func (r sessionHookRecorder) AfterToolCall(_ context.Context, e hooks.ToolResultEvent) hooks.HookResult {
	status := "ok"
	if e.Err != nil {
		status = "error"
	}
	_ = r.s.AppendEvent("tool_result", EventRecord{
		Step:   e.Step,
		Tool:   e.Call.Name,
		Status: status,
		Detail: map[string]any{
			"tool_call_id":  e.Call.ID,
			"content_chars": len(e.Content),
		},
	})
	return hooks.HookResult{}
}

func (r sessionHookRecorder) BeforeContinuityWrite(_ context.Context, e hooks.ContinuityWriteEvent) hooks.HookResult {
	_ = r.s.AppendEvent("continuity_write", EventRecord{
		Tool:    e.Tool,
		SpaceID: e.SpaceID,
		Status:  "before",
		Detail: map[string]any{
			"payload_chars": len(e.Payload),
		},
	})
	return hooks.HookResult{}
}

func (r sessionHookRecorder) AfterContinuityWrite(_ context.Context, e hooks.ContinuityWriteEvent) hooks.HookResult {
	status := "ok"
	if e.Err != nil {
		status = "error"
	}
	_ = r.s.AppendEvent("continuity_write", EventRecord{
		Tool:    e.Tool,
		SpaceID: e.SpaceID,
		Status:  status,
	})
	return hooks.HookResult{}
}
