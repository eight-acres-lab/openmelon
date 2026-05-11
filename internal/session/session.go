// Package session writes per-run conversation history under
// <project>/.openmelon/sessions/<id>/.
//
// One session captures one end-to-end agent run: the system prompt, the
// user input, every model reply, every tool call + tool result, and any
// generated images saved into the session directory. The directory is
// the unit of resumability: copy it to share, point a future
// `openmelon --session <id>` at it to re-enter mid-conversation.
//
// Layout:
//
//	sessions/<id>/
//	  meta.json        — session id, started_at, project id, intent
//	  messages.jsonl   — one JSON message per line (llm.Message shape)
//	  summary.json     — set when the runtime finishes; carries the
//	                     finish-tool summary + final artifact paths
//	  *.png|jpg        — generated images written by generate_image
package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/eight-acres-lab/openmelon/internal/hooks"
	"github.com/eight-acres-lab/openmelon/internal/llm"
	"github.com/eight-acres-lab/openmelon/internal/projectx"
)

// Session is a writable session directory.
type Session struct {
	ID          string
	Dir         string
	StartedAt   time.Time
	Workdir     string
	ProjectID   string
	Intent      string
	Provider    string
	Model       string
	ResumedFrom string
	msgFile     *os.File
}

const SchemaVersion = 2

type Meta struct {
	Version       int       `json:"version"`
	ID            string    `json:"id"`
	ProjectID     string    `json:"project_id"`
	Intent        string    `json:"intent"`
	StartedAt     time.Time `json:"started_at"`
	WorkspaceRoot string    `json:"workspace_root,omitempty"`
	Provider      string    `json:"provider,omitempty"`
	Model         string    `json:"model,omitempty"`
	ResumedFrom   string    `json:"resumed_from,omitempty"`
}

type PromptRecord struct {
	At      time.Time `json:"at"`
	Kind    string    `json:"kind"`
	Content string    `json:"content"`
}

type CompactionRecord struct {
	At           time.Time `json:"at"`
	MessageStart int       `json:"message_start"`
	MessageEnd   int       `json:"message_end"`
	Summary      string    `json:"summary"`
}

type EventRecord struct {
	At      time.Time      `json:"at"`
	Type    string         `json:"type"`
	Step    int            `json:"step,omitempty"`
	Tool    string         `json:"tool,omitempty"`
	SpaceID string         `json:"space_id,omitempty"`
	Status  string         `json:"status,omitempty"`
	Detail  map[string]any `json:"detail,omitempty"`
}

// New creates a fresh session under <workdir>/.openmelon/sessions/<id>/.
//
// The id is "<UTC timestamp>-<8 hex chars>" so directory listings sort
// chronologically and collisions across parallel runs are vanishingly
// unlikely. resumedFrom, if non-empty, is recorded in meta.json so the
// new session keeps a breadcrumb back to the conversation it
// continues.
func New(workdir, projectID, intent string) (*Session, error) {
	return NewResume(workdir, projectID, intent, "")
}

// NewResume is like New but tags the session as a continuation of
// resumedFrom in meta.json.
func NewResume(workdir, projectID, intent, resumedFrom string) (*Session, error) {
	now := time.Now().UTC()
	id := now.Format("20060102-150405") + "-" + randHex(4)
	dir := filepath.Join(projectx.StateDir(workdir), "sessions", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("session: mkdir %s: %w", dir, err)
	}
	s := &Session{
		ID:          id,
		Dir:         dir,
		StartedAt:   now,
		Workdir:     workdir,
		ProjectID:   projectID,
		Intent:      intent,
		ResumedFrom: resumedFrom,
	}
	meta := map[string]any{
		"version":        SchemaVersion,
		"id":             id,
		"project_id":     projectID,
		"intent":         intent,
		"started_at":     now.Format(time.RFC3339),
		"workspace_root": workdir,
	}
	if resumedFrom != "" {
		meta["resumed_from"] = resumedFrom
	}
	body, _ := json.MarshalIndent(meta, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), append(body, '\n'), 0o644); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "messages.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	s.msgFile = f
	return s, nil
}

func (s *Session) SetRuntimeInfo(provider, model string) error {
	s.Provider = strings.TrimSpace(provider)
	s.Model = strings.TrimSpace(model)
	return s.rewriteMeta()
}

func (s *Session) AppendPrompt(kind, content string) error {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil
	}
	kind = strings.TrimSpace(kind)
	if kind == "" {
		kind = "user"
	}
	return appendJSONL(filepath.Join(s.Dir, "prompt_history.jsonl"), PromptRecord{
		At:      time.Now().UTC(),
		Kind:    kind,
		Content: content,
	})
}

func (s *Session) AppendCompaction(start, end int, summary string) error {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return nil
	}
	return appendJSONL(filepath.Join(s.Dir, "compactions.jsonl"), CompactionRecord{
		At:           time.Now().UTC(),
		MessageStart: start,
		MessageEnd:   end,
		Summary:      summary,
	})
}

func (s *Session) AppendEvent(eventType string, rec EventRecord) error {
	eventType = strings.TrimSpace(eventType)
	if eventType == "" {
		return nil
	}
	rec.At = time.Now().UTC()
	rec.Type = eventType
	return appendJSONL(filepath.Join(s.Dir, "events.jsonl"), rec)
}

func (s *Session) HookRecorder() hooks.Manager {
	if s == nil {
		return nil
	}
	return sessionHookRecorder{s: s}
}

// AppendMessages persists each message as one JSONL line. Idempotent
// with respect to ordering: the runtime calls AppendMessages with the
// full delta since the last call, so we don't have to track cursors.
func (s *Session) AppendMessages(msgs []llm.Message) error {
	for _, m := range msgs {
		b, err := json.Marshal(m)
		if err != nil {
			return err
		}
		if _, err := s.msgFile.Write(append(b, '\n')); err != nil {
			return err
		}
	}
	return nil
}

// WriteSummary writes the final summary.json. Best-effort — failure
// here is logged but doesn't fail the run.
func (s *Session) WriteSummary(summary string, artifacts []string, finished bool) error {
	body, _ := json.MarshalIndent(map[string]any{
		"id":          s.ID,
		"finished":    finished,
		"summary":     summary,
		"artifacts":   artifacts,
		"finished_at": time.Now().UTC().Format(time.RFC3339),
	}, "", "  ")
	return os.WriteFile(filepath.Join(s.Dir, "summary.json"), append(body, '\n'), 0o644)
}

// Close flushes + closes the messages file. Safe to call once.
func (s *Session) Close() error {
	if s.msgFile == nil {
		return nil
	}
	err := s.msgFile.Close()
	s.msgFile = nil
	return err
}

func (s *Session) rewriteMeta() error {
	body, _ := json.MarshalIndent(map[string]any{
		"version":        SchemaVersion,
		"id":             s.ID,
		"project_id":     s.ProjectID,
		"intent":         s.Intent,
		"started_at":     s.StartedAt.Format(time.RFC3339),
		"workspace_root": s.Workdir,
		"provider":       s.Provider,
		"model":          s.Model,
	}, "", "  ")
	var meta map[string]any
	if err := json.Unmarshal(body, &meta); err == nil && s.ResumedFrom != "" {
		meta["resumed_from"] = s.ResumedFrom
		body, _ = json.MarshalIndent(meta, "", "  ")
	}
	return os.WriteFile(filepath.Join(s.Dir, "meta.json"), append(body, '\n'), 0o644)
}

func LoadMeta(workdir, sessionID string) (Meta, error) {
	path := filepath.Join(projectx.StateDir(workdir), "sessions", sessionID, "meta.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return Meta{}, fmt.Errorf("session: read %s: %w", path, err)
	}
	var raw struct {
		Version       int    `json:"version"`
		ID            string `json:"id"`
		ProjectID     string `json:"project_id"`
		Intent        string `json:"intent"`
		StartedAt     string `json:"started_at"`
		WorkspaceRoot string `json:"workspace_root"`
		Provider      string `json:"provider"`
		Model         string `json:"model"`
		ResumedFrom   string `json:"resumed_from"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return Meta{}, fmt.Errorf("session: parse %s: %w", path, err)
	}
	startedAt, _ := time.Parse(time.RFC3339, raw.StartedAt)
	return Meta{
		Version:       raw.Version,
		ID:            raw.ID,
		ProjectID:     raw.ProjectID,
		Intent:        raw.Intent,
		StartedAt:     startedAt,
		WorkspaceRoot: raw.WorkspaceRoot,
		Provider:      raw.Provider,
		Model:         raw.Model,
		ResumedFrom:   raw.ResumedFrom,
	}, nil
}

func ValidateWorkspace(workdir, sessionID string) error {
	meta, err := LoadMeta(workdir, sessionID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(meta.WorkspaceRoot) == "" {
		return nil
	}
	want, err := filepath.Abs(workdir)
	if err != nil {
		return err
	}
	got, err := filepath.Abs(meta.WorkspaceRoot)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("session: workspace mismatch for %s: session belongs to %s, current project is %s", sessionID, got, want)
	}
	return nil
}

// LoadHistory reads messages.jsonl from the named session and returns
// the parsed message list. Used by `openmelon resume` to seed a new
// TUI with the prior conversation.
func LoadHistory(workdir, sessionID string) ([]llm.Message, error) {
	path := filepath.Join(projectx.StateDir(workdir), "sessions", sessionID, "messages.jsonl")
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("session: open %s: %w", path, err)
	}
	defer f.Close()
	var out []llm.Message
	dec := json.NewDecoder(f)
	for {
		var m llm.Message
		if err := dec.Decode(&m); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("session: parse %s: %w", path, err)
		}
		out = append(out, m)
	}
	return out, nil
}

func LoadEvents(workdir, sessionID string, limit int) ([]EventRecord, error) {
	path := filepath.Join(projectx.StateDir(workdir), "sessions", sessionID, "events.jsonl")
	events, err := readSessionJSONL[EventRecord](path, limit)
	if err != nil {
		return nil, fmt.Errorf("session: parse %s: %w", path, err)
	}
	return events, nil
}

// Summary is the lightweight metadata record used by Recent. The
// FirstUserMessage field is populated from messages.jsonl when
// present so the picker can show a preview alongside the id.
type Summary struct {
	ID               string
	StartedAt        time.Time
	Intent           string
	ResumedFrom      string
	FirstUserMessage string
	TurnCount        int
}

// Recent returns the most recent N sessions for the project at workdir,
// sorted newest-first. Sessions whose meta.json is unreadable are
// skipped silently.
func Recent(workdir string, limit int) ([]Summary, error) {
	root := filepath.Join(projectx.StateDir(workdir), "sessions")
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Summary
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		s, ok := loadSummary(workdir, e.Name())
		if !ok {
			continue
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func loadSummary(workdir, id string) (Summary, bool) {
	dir := filepath.Join(projectx.StateDir(workdir), "sessions", id)
	body, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return Summary{}, false
	}
	var meta struct {
		ID          string `json:"id"`
		Intent      string `json:"intent"`
		StartedAt   string `json:"started_at"`
		ResumedFrom string `json:"resumed_from"`
	}
	if err := json.Unmarshal(body, &meta); err != nil {
		return Summary{}, false
	}
	startedAt, _ := time.Parse(time.RFC3339, meta.StartedAt)
	s := Summary{
		ID:          meta.ID,
		Intent:      meta.Intent,
		StartedAt:   startedAt,
		ResumedFrom: meta.ResumedFrom,
	}
	// Pull the first user message + turn count for the picker.
	if msgs, err := LoadHistory(workdir, id); err == nil {
		s.TurnCount = len(msgs)
		for _, m := range msgs {
			if m.Role == llm.RoleUser && strings.TrimSpace(m.Content) != "" {
				s.FirstUserMessage = m.Content
				break
			}
		}
	}
	return s, true
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// Fall back to a timestamp-derived suffix; collisions still
		// unlikely because the parent dir name already includes
		// per-second granularity.
		now := time.Now().UnixNano()
		for i := range b {
			b[i] = byte(now >> (i * 8))
		}
	}
	return hex.EncodeToString(b)
}

func appendJSONL(path string, v any) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	return err
}

func readSessionJSONL[T any](path string, limit int) ([]T, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []T
	dec := json.NewDecoder(f)
	for {
		var v T
		if err := dec.Decode(&v); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		out = append(out, v)
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}
