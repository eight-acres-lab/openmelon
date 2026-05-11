package hooks

import (
	"context"
	"encoding/json"
	"testing"
)

func TestChainManagersMergesFeedbackAndRewrites(t *testing.T) {
	h := ChainManagers(
		testHook{feedback: "first"},
		testHook{rewriteTool: json.RawMessage(`{"text":"rewritten"}`), feedback: "second"},
	)
	got := h.BeforeToolCall(context.Background(), ToolCallEvent{})
	if string(got.RewriteToolArguments) != `{"text":"rewritten"}` {
		t.Fatalf("rewrite = %s", got.RewriteToolArguments)
	}
	if len(got.AppendUserFeedback) != 2 || got.AppendUserFeedback[0] != "first" || got.AppendUserFeedback[1] != "second" {
		t.Fatalf("feedback = %+v", got.AppendUserFeedback)
	}
}

func TestChainManagersStopsOnDeny(t *testing.T) {
	called := false
	h := ChainManagers(
		testHook{decision: Deny, reason: "blocked"},
		testHook{onBeforeTool: func() { called = true }},
	)
	got := h.BeforeToolCall(context.Background(), ToolCallEvent{})
	if got.EffectiveDecision() != Deny || got.Reason != "blocked" {
		t.Fatalf("result = %+v", got)
	}
	if called {
		t.Fatal("chain continued after deny")
	}
}

type testHook struct {
	NoopManager
	decision     Decision
	reason       string
	feedback     string
	rewriteTool  json.RawMessage
	onBeforeTool func()
}

func (h testHook) BeforeToolCall(context.Context, ToolCallEvent) HookResult {
	if h.onBeforeTool != nil {
		h.onBeforeTool()
	}
	var fb []string
	if h.feedback != "" {
		fb = []string{h.feedback}
	}
	return HookResult{
		Decision:             h.decision,
		Reason:               h.reason,
		AppendUserFeedback:   fb,
		RewriteToolArguments: h.rewriteTool,
	}
}
