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
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/eight-acres-lab/openmelon/internal/llm"
	"github.com/eight-acres-lab/openmelon/internal/projectx"
)

// Session is a writable session directory.
type Session struct {
	ID         string
	Dir        string
	StartedAt  time.Time
	Workdir    string
	ProjectID  string
	Intent     string
	msgFile    *os.File
}

// New creates a fresh session under <workdir>/.openmelon/sessions/<id>/.
//
// The id is "<UTC timestamp>-<8 hex chars>" so directory listings sort
// chronologically and collisions across parallel runs are vanishingly
// unlikely.
func New(workdir, projectID, intent string) (*Session, error) {
	now := time.Now().UTC()
	id := now.Format("20060102-150405") + "-" + randHex(4)
	dir := filepath.Join(projectx.StateDir(workdir), "sessions", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("session: mkdir %s: %w", dir, err)
	}
	s := &Session{
		ID:        id,
		Dir:       dir,
		StartedAt: now,
		Workdir:   workdir,
		ProjectID: projectID,
		Intent:    intent,
	}
	meta, _ := json.MarshalIndent(map[string]any{
		"id":         id,
		"project_id": projectID,
		"intent":     intent,
		"started_at": now.Format(time.RFC3339),
	}, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), append(meta, '\n'), 0o644); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "messages.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	s.msgFile = f
	return s, nil
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
		"id":         s.ID,
		"finished":   finished,
		"summary":    summary,
		"artifacts":  artifacts,
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
