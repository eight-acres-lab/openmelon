package session

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eight-acres-lab/openmelon/internal/llm"
	"github.com/eight-acres-lab/openmelon/internal/projectx"
)

func TestNewCreatesDirAndMeta(t *testing.T) {
	wd := t.TempDir()
	if _, err := projectx.Init(wd, "ai-talks", "AI Talks"); err != nil {
		t.Fatalf("project init: %v", err)
	}
	s, err := New(wd, "ai-talks", "Lao Wang sells noodles")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	if _, err := os.Stat(s.Dir); err != nil {
		t.Errorf("session dir missing: %v", err)
	}
	meta, err := os.ReadFile(filepath.Join(s.Dir, "meta.json"))
	if err != nil {
		t.Fatalf("read meta: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(meta, &m); err != nil {
		t.Fatalf("parse meta: %v", err)
	}
	if m["intent"] != "Lao Wang sells noodles" {
		t.Errorf("intent: %v", m["intent"])
	}
	if m["project_id"] != "ai-talks" {
		t.Errorf("project_id: %v", m["project_id"])
	}
	if m["version"].(float64) != SchemaVersion {
		t.Errorf("version: %v", m["version"])
	}
	if m["workspace_root"] != wd {
		t.Errorf("workspace_root: %v", m["workspace_root"])
	}
}

func TestRuntimeInfoAndPromptHistory(t *testing.T) {
	wd := t.TempDir()
	if _, err := projectx.Init(wd, "ai-talks", "AI Talks"); err != nil {
		t.Fatalf("project init: %v", err)
	}
	s, err := NewResume(wd, "ai-talks", "x", "old-session")
	if err != nil {
		t.Fatalf("NewResume: %v", err)
	}
	defer s.Close()
	if err := s.SetRuntimeInfo("openrouter", "openai/gpt-5"); err != nil {
		t.Fatalf("SetRuntimeInfo: %v", err)
	}
	if err := s.AppendPrompt("pending", "make it shorter"); err != nil {
		t.Fatalf("AppendPrompt: %v", err)
	}
	meta, err := LoadMeta(wd, s.ID)
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if meta.Provider != "openrouter" || meta.Model != "openai/gpt-5" || meta.ResumedFrom != "old-session" {
		t.Fatalf("meta missing runtime info: %+v", meta)
	}
	b, err := os.ReadFile(filepath.Join(s.Dir, "prompt_history.jsonl"))
	if err != nil {
		t.Fatalf("read prompt history: %v", err)
	}
	if !strings.Contains(string(b), `"kind":"pending"`) || !strings.Contains(string(b), "make it shorter") {
		t.Fatalf("prompt history missing record: %s", string(b))
	}
}

func TestAppendEventAndValidateWorkspace(t *testing.T) {
	wd := t.TempDir()
	if _, err := projectx.Init(wd, "ai-talks", "AI Talks"); err != nil {
		t.Fatalf("project init: %v", err)
	}
	s, err := New(wd, "ai-talks", "x")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()
	if err := s.AppendEvent("tool_call", EventRecord{Tool: "search", Status: "ok"}); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	if err := ValidateWorkspace(wd, s.ID); err != nil {
		t.Fatalf("ValidateWorkspace: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(s.Dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	if !strings.Contains(string(b), `"type":"tool_call"`) || !strings.Contains(string(b), `"tool":"search"`) {
		t.Fatalf("events missing record: %s", string(b))
	}
	events, err := LoadEvents(wd, s.ID, 10)
	if err != nil {
		t.Fatalf("LoadEvents: %v", err)
	}
	if len(events) != 1 || events[0].Tool != "search" {
		t.Fatalf("loaded events mismatch: %+v", events)
	}
}

func TestAppendMessagesWritesJSONL(t *testing.T) {
	wd := t.TempDir()
	if _, err := projectx.Init(wd, "ai-talks", "AI Talks"); err != nil {
		t.Fatalf("project init: %v", err)
	}
	s, err := New(wd, "ai-talks", "x")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "be terse"},
		{Role: llm.RoleUser, Content: "hi"},
		{Role: llm.RoleAssistant, Content: "hello"},
	}
	if err := s.AppendMessages(msgs); err != nil {
		t.Fatalf("AppendMessages: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	f, err := os.Open(filepath.Join(s.Dir, "messages.jsonl"))
	if err != nil {
		t.Fatalf("open messages: %v", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	var lines []string
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
	if !strings.Contains(lines[0], `"role":"system"`) {
		t.Errorf("first line missing system role: %s", lines[0])
	}
}

func TestWriteSummary(t *testing.T) {
	wd := t.TempDir()
	if _, err := projectx.Init(wd, "ai-talks", "AI Talks"); err != nil {
		t.Fatalf("project init: %v", err)
	}
	s, err := New(wd, "ai-talks", "x")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := s.WriteSummary("all done", []string{"/tmp/a.png"}, true); err != nil {
		t.Fatalf("WriteSummary: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(s.Dir, "summary.json"))
	if err != nil {
		t.Fatalf("read summary: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("parse summary: %v", err)
	}
	if m["summary"] != "all done" || m["finished"] != true {
		t.Errorf("summary mismatch: %+v", m)
	}
}
