package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eight-acres-lab/openmelon/internal/llm"
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

	if got := m.transcript.String(); !strings.Contains(got, "saved 2 messages") || !strings.Contains(got, path) {
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
