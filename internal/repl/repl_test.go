package repl

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/eight-acres-lab/openmelon/internal/llm"
	"github.com/eight-acres-lab/openmelon/internal/projectx"
	"github.com/eight-acres-lab/openmelon/internal/runtime"
	"github.com/eight-acres-lab/openmelon/internal/tools"
)

// scriptedLLM returns a sequence of pre-recorded chat responses, one
// per Run() call. Used to drive the REPL through deterministic turns.
type scriptedLLM struct{ responses []llm.ChatResponse }

func (s *scriptedLLM) Chat(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	if len(s.responses) == 0 {
		return &llm.ChatResponse{
			Message:      llm.Message{Role: llm.RoleAssistant, Content: "(out of script)"},
			FinishReason: llm.FinishStop,
		}, nil
	}
	r := s.responses[0]
	s.responses = s.responses[1:]
	return &r, nil
}

func newProjectAt(t *testing.T) (string, *projectx.Project) {
	t.Helper()
	wd := t.TempDir()
	p, err := projectx.Init(wd, "test-proj", "Test")
	if err != nil {
		t.Fatalf("project init: %v", err)
	}
	return wd, p
}

func TestRunExitsOnSlashExit(t *testing.T) {
	wd, p := newProjectAt(t)
	reg := tools.NewRegistry()
	rt := &runtime.Runtime{LLM: &scriptedLLM{}, Registry: reg}

	in := strings.NewReader("/exit\n")
	var out, errOut bytes.Buffer
	err := Run(context.Background(), Options{
		Workdir: wd, Project: p, Runtime: rt,
		SystemPrompt: "be terse", SessionIntent: "test",
		In: in, Out: &out, Err: &errOut,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "bye") {
		t.Errorf("expected goodbye, got: %q", out.String())
	}
}

func TestRunExitsOnEOF(t *testing.T) {
	wd, p := newProjectAt(t)
	reg := tools.NewRegistry()
	rt := &runtime.Runtime{LLM: &scriptedLLM{}, Registry: reg}

	in := strings.NewReader("") // immediate EOF
	var out, errOut bytes.Buffer
	if err := Run(context.Background(), Options{
		Workdir: wd, Project: p, Runtime: rt,
		SystemPrompt: "x", SessionIntent: "test",
		In: in, Out: &out, Err: &errOut,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestRunSendsUserInputThroughRuntimeAndStreamsTextOut(t *testing.T) {
	wd, p := newProjectAt(t)
	reg := tools.NewRegistry()
	rt := &runtime.Runtime{
		LLM: &scriptedLLM{responses: []llm.ChatResponse{{
			Message:      llm.Message{Role: llm.RoleAssistant, Content: "hello back"},
			FinishReason: llm.FinishStop,
		}}},
		Registry: reg,
	}
	in := strings.NewReader("hi\n/exit\n")
	var out, errOut bytes.Buffer
	if err := Run(context.Background(), Options{
		Workdir: wd, Project: p, Runtime: rt,
		SystemPrompt: "x", SessionIntent: "test",
		In: in, Out: &out, Err: &errOut,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "hello back") {
		t.Errorf("model reply not rendered: %q", out.String())
	}
}

func TestRunPersistsHistoryAcrossTurns(t *testing.T) {
	wd, p := newProjectAt(t)
	reg := tools.NewRegistry()

	// Track what each Chat call sees.
	var seen []int
	wrapping := &recordingLLM{
		inner: &scriptedLLM{responses: []llm.ChatResponse{
			{Message: llm.Message{Role: llm.RoleAssistant, Content: "a"}, FinishReason: llm.FinishStop},
			{Message: llm.Message{Role: llm.RoleAssistant, Content: "b"}, FinishReason: llm.FinishStop},
		}},
		recorder: &seen,
	}
	rt := &runtime.Runtime{LLM: wrapping, Registry: reg}

	in := strings.NewReader("first\nsecond\n/exit\n")
	var out, errOut bytes.Buffer
	if err := Run(context.Background(), Options{
		Workdir: wd, Project: p, Runtime: rt,
		SystemPrompt: "be terse", SessionIntent: "test",
		In: in, Out: &out, Err: &errOut,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(seen) != 2 {
		t.Fatalf("expected 2 chat calls, got %d", len(seen))
	}
	// Turn 1: system + user. Turn 2: system + user + assistant + user.
	if seen[0] != 2 {
		t.Errorf("first turn message count: %d (want 2)", seen[0])
	}
	if seen[1] != 4 {
		t.Errorf("second turn message count: %d (want 4)", seen[1])
	}
}

func TestSlashClearResetsHistory(t *testing.T) {
	wd, p := newProjectAt(t)
	reg := tools.NewRegistry()

	var seen []int
	wrapping := &recordingLLM{
		inner: &scriptedLLM{responses: []llm.ChatResponse{
			{Message: llm.Message{Role: llm.RoleAssistant, Content: "a"}, FinishReason: llm.FinishStop},
			{Message: llm.Message{Role: llm.RoleAssistant, Content: "b"}, FinishReason: llm.FinishStop},
		}},
		recorder: &seen,
	}
	rt := &runtime.Runtime{LLM: wrapping, Registry: reg}

	in := strings.NewReader("first\n/clear\nsecond\n/exit\n")
	var out, errOut bytes.Buffer
	if err := Run(context.Background(), Options{
		Workdir: wd, Project: p, Runtime: rt,
		SystemPrompt: "x", SessionIntent: "test",
		In: in, Out: &out, Err: &errOut,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(seen) != 2 {
		t.Fatalf("expected 2 chat calls, got %d", len(seen))
	}
	// After /clear, the second turn should NOT include prior history —
	// it sends the system prompt + user only, just like the first turn.
	if seen[1] != 2 {
		t.Errorf("after /clear, second turn should send 2 messages, got %d", seen[1])
	}
	if !strings.Contains(out.String(), "history cleared") {
		t.Errorf("expected /clear feedback, got: %q", out.String())
	}
}

func TestSlashHelpPrintsCommandList(t *testing.T) {
	wd, p := newProjectAt(t)
	reg := tools.NewRegistry()
	rt := &runtime.Runtime{LLM: &scriptedLLM{}, Registry: reg}

	in := strings.NewReader("/help\n/exit\n")
	var out, errOut bytes.Buffer
	if err := Run(context.Background(), Options{
		Workdir: wd, Project: p, Runtime: rt,
		SystemPrompt: "x", SessionIntent: "test",
		In: in, Out: &out, Err: &errOut,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	body := out.String()
	for _, want := range []string{"/clear", "/history", "/save", "/session", "/exit"} {
		if !strings.Contains(body, want) {
			t.Errorf("/help missing %q", want)
		}
	}
}

func TestRunSeedsResumedHistory(t *testing.T) {
	wd, p := newProjectAt(t)
	reg := tools.NewRegistry()
	var seen []int
	wrapping := &recordingLLM{
		inner: &scriptedLLM{responses: []llm.ChatResponse{{
			Message:      llm.Message{Role: llm.RoleAssistant, Content: "continued"},
			FinishReason: llm.FinishStop,
		}}},
		recorder: &seen,
	}
	rt := &runtime.Runtime{LLM: wrapping, Registry: reg}
	initial := []llm.Message{
		{Role: llm.RoleSystem, Content: "system"},
		{Role: llm.RoleUser, Content: "before"},
		{Role: llm.RoleAssistant, Content: "old reply"},
	}

	in := strings.NewReader("next\n/exit\n")
	var out, errOut bytes.Buffer
	if err := Run(context.Background(), Options{
		Workdir:        wd,
		Project:        p,
		Runtime:        rt,
		SystemPrompt:   "ignored",
		SessionIntent:  "test",
		InitialHistory: initial,
		ResumedFrom:    "prev-session",
		In:             in,
		Out:            &out,
		Err:            &errOut,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(seen) != 1 || seen[0] != 4 {
		t.Fatalf("chat saw message counts %+v, want [4]", seen)
	}
	body := out.String()
	for _, want := range []string{
		"resumed from: prev-session",
		"loaded 3 prior messages",
		"prior conversation",
		"> before",
		"old reply",
		"continue below",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("resume output missing %q: %q", want, body)
		}
	}
}

func TestRenderHistoryShowsToolCallsAndErrors(t *testing.T) {
	var out bytes.Buffer
	renderHistory(&out, []llm.Message{
		{Role: llm.RoleUser, Content: "make image"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
			Name:      "generate_image",
			Arguments: json.RawMessage(`{"prompt":"x"}`),
		}}},
		{Role: llm.RoleTool, Content: `{"error":"image failed"}`},
	})

	body := out.String()
	for _, want := range []string{"prior conversation", "> make image", "generate_image", "error: image failed"} {
		if !strings.Contains(body, want) {
			t.Fatalf("history output missing %q: %q", want, body)
		}
	}
	if strings.Contains(body, `{"error"`) {
		t.Fatalf("history output should expose error text, not raw JSON: %q", body)
	}
}

func TestRenderToolCallUsesCompactSummary(t *testing.T) {
	var out bytes.Buffer
	renderToolCallBlock(&out, llm.ToolCall{
		Name:      "generate_image",
		Arguments: json.RawMessage(`{"label":"episode-2-panel-1","size":"1024x1536","prompt":"draw a consistent tennis comic with the same vertical four-panel layout and recurring character details","reference_images":["/tmp/a.png","/tmp/b.png"]}`),
	})

	body := out.String()
	for _, want := range []string{"generate_image", "episode-2-panel-1", "1024x1536", "2 refs"} {
		if !strings.Contains(body, want) {
			t.Fatalf("tool call summary missing %q: %q", want, body)
		}
	}
	if strings.Contains(body, `{"label"`) || strings.Contains(body, `"reference_images"`) {
		t.Fatalf("tool call should not show raw JSON args: %q", body)
	}
}

func TestRenderToolResultSummarizesPath(t *testing.T) {
	var out bytes.Buffer
	renderToolResultBlock(&out, "generate_image", `{"path":"/tmp/openmelon/session/image.png","prompt":"long prompt","sha256":"abc"}`, nil)

	body := out.String()
	if !strings.Contains(body, "saved session/image.png") {
		t.Fatalf("tool result summary missing saved path: %q", body)
	}
	if strings.Contains(body, "sha256") || strings.Contains(body, "long prompt") {
		t.Fatalf("tool result should not show noisy raw JSON: %q", body)
	}
}

func TestTerminalTracerRendersMarkdown(t *testing.T) {
	var out bytes.Buffer
	tr := newTerminalTracer(&out)
	tr.OnText("# Plan\n\n- **First** item with `code`.\n")
	tr.OnTurnEnd(1, llm.FinishStop, llm.Usage{})

	body := out.String()
	for _, want := range []string{"Plan", "- First item with code."} {
		if !strings.Contains(body, want) {
			t.Fatalf("markdown render missing %q: %q", want, body)
		}
	}
	for _, raw := range []string{"# Plan", "**First**", "`code`"} {
		if strings.Contains(body, raw) {
			t.Fatalf("markdown render leaked raw marker %q: %q", raw, body)
		}
	}
}

func TestFinishRendersAsTextNotToolCall(t *testing.T) {
	var out bytes.Buffer
	tr := newTerminalTracer(&out)
	call := llm.ToolCall{
		ID:        "finish-1",
		Name:      "finish",
		Arguments: json.RawMessage(`{"summary":"# Done\n\n- **Ready**"}`),
	}
	tr.OnToolCall(call)
	tr.OnToolResult(call, `{"summary":"# Done\n\n- **Ready**","artifacts":["/tmp/final.png"],"ok":true}`, nil)

	body := out.String()
	if strings.Contains(body, "finish") || strings.Contains(body, "●") || strings.Contains(body, "└") {
		t.Fatalf("finish should not render as a tool call: %q", body)
	}
	for _, want := range []string{"Done", "- Ready", "artifact: /tmp/final.png"} {
		if !strings.Contains(body, want) {
			t.Fatalf("finish text missing %q: %q", want, body)
		}
	}
	if strings.Contains(body, "# Done") || strings.Contains(body, "**Ready**") {
		t.Fatalf("finish summary should render markdown: %q", body)
	}
}

func TestRenderHistorySkipsSystemMessages(t *testing.T) {
	var out bytes.Buffer
	renderHistory(&out, []llm.Message{
		{Role: llm.RoleSystem, Content: "secret system prompt"},
		{Role: llm.RoleUser, Content: "hello"},
	})

	body := out.String()
	if strings.Contains(body, "secret system prompt") {
		t.Fatalf("resume banner missing context: %q", body)
	}
	if !strings.Contains(body, "> hello") {
		t.Fatalf("history output missing user message: %q", body)
	}
}

func TestInlineApprovalPromptAllowsAlways(t *testing.T) {
	scanner := bufio.NewScanner(strings.NewReader("a\n"))
	var out bytes.Buffer
	approve := newApprovalPrompt(scanner, &out)
	decision := approve(tools.ApprovalRequest{
		Tool:        "bash",
		Command:     "ls -la",
		Description: "inspect files",
		Binary:      "ls",
	})
	if !decision.Approved || !decision.Always {
		t.Fatalf("approval decision = %+v, want approved always", decision)
	}
	if !strings.Contains(out.String(), "Do you want to proceed?") {
		t.Fatalf("approval output missing prompt: %q", out.String())
	}
}

// recordingLLM wraps a ToolCaller and records the message-list length
// passed into each Chat call.
type recordingLLM struct {
	inner    llm.ToolCaller
	recorder *[]int
}

func (r *recordingLLM) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	*r.recorder = append(*r.recorder, len(req.Messages))
	return r.inner.Chat(ctx, req)
}

// satisfy json import in renderer (used by /save smoke test below)
var _ = json.RawMessage{}
