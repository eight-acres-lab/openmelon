package hooks

import (
	"context"
	"encoding/json"

	"github.com/eight-acres-lab/openmelon/internal/llm"
)

type Decision string

const (
	Allow  Decision = "allow"
	Deny   Decision = "deny"
	Cancel Decision = "cancel"
)

type HookResult struct {
	Decision                 Decision
	Reason                   string
	AppendUserFeedback       []string
	RewriteToolArguments     json.RawMessage
	RewriteContinuityPayload json.RawMessage
}

func (r HookResult) EffectiveDecision() Decision {
	if r.Decision == "" {
		return Allow
	}
	return r.Decision
}

type Manager interface {
	BeforeModelRequest(context.Context, ModelRequestEvent) HookResult
	AfterModelResponse(context.Context, ModelResponseEvent) HookResult
	BeforeToolCall(context.Context, ToolCallEvent) HookResult
	AfterToolCall(context.Context, ToolResultEvent) HookResult
	BeforeContinuityWrite(context.Context, ContinuityWriteEvent) HookResult
	AfterContinuityWrite(context.Context, ContinuityWriteEvent) HookResult
}

type NoopManager struct{}

func (NoopManager) BeforeModelRequest(context.Context, ModelRequestEvent) HookResult {
	return HookResult{}
}
func (NoopManager) AfterModelResponse(context.Context, ModelResponseEvent) HookResult {
	return HookResult{}
}
func (NoopManager) BeforeToolCall(context.Context, ToolCallEvent) HookResult  { return HookResult{} }
func (NoopManager) AfterToolCall(context.Context, ToolResultEvent) HookResult { return HookResult{} }
func (NoopManager) BeforeContinuityWrite(context.Context, ContinuityWriteEvent) HookResult {
	return HookResult{}
}
func (NoopManager) AfterContinuityWrite(context.Context, ContinuityWriteEvent) HookResult {
	return HookResult{}
}

type Chain []Manager

func ChainManagers(managers ...Manager) Manager {
	var out Chain
	for _, m := range managers {
		if m != nil {
			out = append(out, m)
		}
	}
	if len(out) == 0 {
		return nil
	}
	if len(out) == 1 {
		return out[0]
	}
	return out
}

func (c Chain) BeforeModelRequest(ctx context.Context, e ModelRequestEvent) HookResult {
	var merged HookResult
	for _, m := range c {
		r := m.BeforeModelRequest(ctx, e)
		merged.AppendUserFeedback = append(merged.AppendUserFeedback, r.AppendUserFeedback...)
		if stop := mergeDecision(&merged, r); stop {
			return merged
		}
	}
	return merged
}

func (c Chain) AfterModelResponse(ctx context.Context, e ModelResponseEvent) HookResult {
	var merged HookResult
	for _, m := range c {
		r := m.AfterModelResponse(ctx, e)
		merged.AppendUserFeedback = append(merged.AppendUserFeedback, r.AppendUserFeedback...)
		if stop := mergeDecision(&merged, r); stop {
			return merged
		}
	}
	return merged
}

func (c Chain) BeforeToolCall(ctx context.Context, e ToolCallEvent) HookResult {
	var merged HookResult
	for _, m := range c {
		r := m.BeforeToolCall(ctx, e)
		if len(r.RewriteToolArguments) > 0 {
			merged.RewriteToolArguments = r.RewriteToolArguments
			e.Call.Arguments = r.RewriteToolArguments
		}
		merged.AppendUserFeedback = append(merged.AppendUserFeedback, r.AppendUserFeedback...)
		if stop := mergeDecision(&merged, r); stop {
			return merged
		}
	}
	return merged
}

func (c Chain) AfterToolCall(ctx context.Context, e ToolResultEvent) HookResult {
	var merged HookResult
	for _, m := range c {
		r := m.AfterToolCall(ctx, e)
		merged.AppendUserFeedback = append(merged.AppendUserFeedback, r.AppendUserFeedback...)
		if stop := mergeDecision(&merged, r); stop {
			return merged
		}
	}
	return merged
}

func (c Chain) BeforeContinuityWrite(ctx context.Context, e ContinuityWriteEvent) HookResult {
	var merged HookResult
	for _, m := range c {
		r := m.BeforeContinuityWrite(ctx, e)
		if len(r.RewriteContinuityPayload) > 0 {
			merged.RewriteContinuityPayload = r.RewriteContinuityPayload
			e.Payload = r.RewriteContinuityPayload
		}
		merged.AppendUserFeedback = append(merged.AppendUserFeedback, r.AppendUserFeedback...)
		if stop := mergeDecision(&merged, r); stop {
			return merged
		}
	}
	return merged
}

func (c Chain) AfterContinuityWrite(ctx context.Context, e ContinuityWriteEvent) HookResult {
	var merged HookResult
	for _, m := range c {
		r := m.AfterContinuityWrite(ctx, e)
		merged.AppendUserFeedback = append(merged.AppendUserFeedback, r.AppendUserFeedback...)
		if stop := mergeDecision(&merged, r); stop {
			return merged
		}
	}
	return merged
}

func mergeDecision(dst *HookResult, src HookResult) bool {
	switch src.EffectiveDecision() {
	case Deny, Cancel:
		dst.Decision = src.EffectiveDecision()
		dst.Reason = src.Reason
		return true
	default:
		return false
	}
}

type ModelRequestEvent struct {
	Step     int
	Messages []llm.Message
	Tools    []llm.Tool
}

type ModelResponseEvent struct {
	Step     int
	Response llm.ChatResponse
}

type ToolCallEvent struct {
	Step int
	Call llm.ToolCall
}

type ToolResultEvent struct {
	Step    int
	Call    llm.ToolCall
	Content string
	Err     error
}

type ContinuityWriteEvent struct {
	Tool    string
	Workdir string
	SpaceID string
	Payload json.RawMessage
	Result  any
	Err     error
}
