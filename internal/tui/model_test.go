package tui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/eight-acres-lab/openmelon/internal/llm"
	"github.com/eight-acres-lab/openmelon/internal/runtime"
	"github.com/eight-acres-lab/openmelon/internal/tools"
)

func TestHandleSlashSaveWritesHistoryAsJSONL(t *testing.T) {
	m := newModel(modelInit{})
	m.history = []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, Content: "hi there"},
	}

	path := filepath.Join(t.TempDir(), "conversation.jsonl")
	if cmd := m.handleSlash("/save " + path); cmd != nil {
		t.Fatalf("handleSlash returned command %T", cmd)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	if len(lines) != len(m.history) {
		t.Fatalf("saved %d messages, want %d", len(lines), len(m.history))
	}
	for i, line := range lines {
		var got llm.Message
		if err := json.Unmarshal([]byte(line), &got); err != nil {
			t.Fatalf("line %d is not JSON: %v", i, err)
		}
		if got.Role != m.history[i].Role || got.Content != m.history[i].Content || got.ToolCallID != m.history[i].ToolCallID || len(got.ToolCalls) != len(m.history[i].ToolCalls) {
			t.Fatalf("line %d = %#v, want %#v", i, got, m.history[i])
		}
	}

	if got := m.transcript.String(); !strings.Contains(got, "saved 2 messages") || !strings.Contains(got, filepath.Base(path)) {
		t.Fatalf("transcript %q does not report saved path", got)
	}
}

func TestHandleSlashSaveRequiresPath(t *testing.T) {
	m := newModel(modelInit{})

	if cmd := m.handleSlash("/save"); cmd != nil {
		t.Fatalf("handleSlash returned command %T", cmd)
	}

	if got := m.transcript.String(); !strings.Contains(got, "/save: usage: /save <path>") {
		t.Fatalf("transcript %q does not report usage", got)
	}
}

func TestHandleSlashSaveReportsCreateError(t *testing.T) {
	m := newModel(modelInit{})
	path := t.TempDir()

	if cmd := m.handleSlash("/save " + path); cmd != nil {
		t.Fatalf("handleSlash returned command %T", cmd)
	}

	if got := m.transcript.String(); !strings.Contains(got, "/save:") {
		t.Fatalf("transcript %q does not report save error", got)
	}
}

func TestInputHistoryRecallsPreviousPrompts(t *testing.T) {
	m := newModel(modelInit{})
	m.recordInputHistory("first")
	m.recordInputHistory("second")

	if !m.handleInputHistoryKey(tea.KeyMsg{Type: tea.KeyUp}) {
		t.Fatal("expected up to recall history")
	}
	if got := m.textarea.Value(); got != "second" {
		t.Fatalf("first recall = %q", got)
	}
	if !m.handleInputHistoryKey(tea.KeyMsg{Type: tea.KeyUp}) {
		t.Fatal("expected second up to recall older history")
	}
	if got := m.textarea.Value(); got != "first" {
		t.Fatalf("second recall = %q", got)
	}
	if !m.handleInputHistoryKey(tea.KeyMsg{Type: tea.KeyDown}) {
		t.Fatal("expected down to move forward")
	}
	if got := m.textarea.Value(); got != "second" {
		t.Fatalf("down recall = %q", got)
	}
	if !m.handleInputHistoryKey(tea.KeyMsg{Type: tea.KeyDown}) {
		t.Fatal("expected down to restore draft")
	}
	if got := m.textarea.Value(); got != "" {
		t.Fatalf("restored draft = %q", got)
	}
}

func TestNewlineKeyInsertsNewlineWithoutSubmitting(t *testing.T) {
	m := newModel(modelInit{Runtime: &runtime.Runtime{}})
	m.textarea.SetValue("first")
	m.runner = func(ctx context.Context, in runtime.RunInput) (*runtime.RunResult, error) {
		t.Fatal("newline should not submit")
		return nil, nil
	}

	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	if cmd != nil {
		t.Fatalf("newline returned command %T", cmd)
	}
	m = model.(*Model)
	if got := m.textarea.Value(); got != "first\n" {
		t.Fatalf("textarea = %q", got)
	}
}

func TestEnterStillSubmits(t *testing.T) {
	m := newModel(modelInit{Runtime: &runtime.Runtime{}})
	m.textarea.SetValue("send me")
	m.runner = func(ctx context.Context, in runtime.RunInput) (*runtime.RunResult, error) {
		return &runtime.RunResult{Messages: []llm.Message{{Role: llm.RoleUser, Content: in.UserInput}}}, nil
	}

	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter did not return submit command")
	}
	m = model.(*Model)
	if m.state != stateRunning {
		t.Fatalf("state = %v, want running", m.state)
	}
	if got := m.textarea.Value(); got != "" {
		t.Fatalf("textarea = %q, want reset", got)
	}
}

func TestLongInputSoftWrapsByGrowingTextarea(t *testing.T) {
	m := newModel(modelInit{Runtime: &runtime.Runtime{}})
	m.resize(20, 20)
	m.textarea.SetValue(strings.Repeat("a", 40))
	m.recomputeLayout()

	if got := m.textarea.Height(); got < 3 {
		t.Fatalf("textarea height = %d, want soft-wrapped growth", got)
	}
	if got := inputVisualLines(strings.Repeat("a", 40), inputTextWidth(20)); got < 3 {
		t.Fatalf("visual lines = %d, want >= 3", got)
	}
}

func TestAppendLineWrapsLongStatusText(t *testing.T) {
	m := newModel(modelInit{})
	m.resize(24, 20)
	m.appendLine(styleErr.Render(strings.Repeat("x", 60)))

	got := m.transcript.String()
	if lines := strings.Count(got, "\n"); lines < 3 {
		t.Fatalf("transcript line count = %d, want wrapped output; body=%q", lines, got)
	}
}

func TestApprovalCanScrollLongCommand(t *testing.T) {
	m := newModel(modelInit{})
	m.resize(36, 24)
	m.state = stateApprovalPending
	m.approvalReq = &approvalRequestMsg{
		Description: "review this command",
		Command:     strings.Join([]string{"line-01", "line-02", "line-03", "line-04", "line-05", "line-06", "line-07", "line-08", "line-09", "line-10"}, "\n"),
		Binary:      "bash",
		Reply:       make(chan tools.ApprovalDecision, 1),
	}

	before := m.renderApproval()
	if !strings.Contains(before, "line-01") {
		t.Fatalf("initial approval view missing first command line: %q", before)
	}
	m.updateApproval(tea.KeyMsg{Type: tea.KeyPgDown})
	after := m.renderApproval()
	if m.approvalScroll == 0 {
		t.Fatal("approval scroll did not move")
	}
	if !strings.Contains(after, "line-10") {
		t.Fatalf("scrolled approval view missing later command line: %q", after)
	}
}

func TestQueueInputDrainsIntoRuntime(t *testing.T) {
	m := newModel(modelInit{Runtime: &runtime.Runtime{}})
	m.queueInput("make it shorter")

	got := m.drainQueuedInput()
	if len(got) != 1 || got[0] != "make it shorter" {
		t.Fatalf("drain = %#v", got)
	}
	if got := m.drainQueuedInput(); len(got) != 0 {
		t.Fatalf("second drain = %#v", got)
	}
	m.consumeAppliedInputAcks()
	if m.pendingInputs != 0 {
		t.Fatalf("pending inputs = %d", m.pendingInputs)
	}
}

func TestSubmitInstallsRuntimeDrainHook(t *testing.T) {
	rt := &runtime.Runtime{}
	m := newModel(modelInit{Runtime: rt})
	m.queueInput("queued before model call")
	m.runner = func(ctx context.Context, in runtime.RunInput) (*runtime.RunResult, error) {
		got := rt.DrainUserInput()
		if len(got) != 1 || got[0] != "queued before model call" {
			t.Fatalf("runtime drain = %#v", got)
		}
		return &runtime.RunResult{Messages: []llm.Message{{Role: llm.RoleUser, Content: in.UserInput}}}, nil
	}

	cmd := m.submit("start")
	if cmd == nil {
		t.Fatal("submit returned nil command")
	}
	if msg := cmd(); msg == nil {
		t.Fatal("runner command returned nil message")
	}
}

func TestRunDoneStartsNextRunForUndrainedPendingInput(t *testing.T) {
	rt := &runtime.Runtime{}
	m := newModel(modelInit{Runtime: rt})
	m.state = stateRunning
	m.queueInput("follow up")
	m.textarea.SetValue("draft")

	_, cmd := m.Update(runDoneMsg{
		Result: &runtime.RunResult{Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "system"},
			{Role: llm.RoleUser, Content: "start"},
			{Role: llm.RoleAssistant, Content: "done"},
		}},
	})
	if cmd == nil {
		t.Fatal("expected queued input to start a follow-up run")
	}
	if m.state != stateRunning {
		t.Fatalf("state = %v, want running", m.state)
	}
	if m.pendingInputs != 0 {
		t.Fatalf("pending inputs = %d", m.pendingInputs)
	}
	if got := m.textarea.Value(); got != "draft" {
		t.Fatalf("textarea = %q", got)
	}
}
