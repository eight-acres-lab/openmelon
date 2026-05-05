package repl

// render.go — terminal-friendly Tracer that writes to stdout.
//
// Layout per turn:
//
//   [user types]
//   <streamed assistant text appears here>
//   ⏵ tool_name(args...)
//     ↳ {result-json-truncated}
//   <more streamed assistant text>
//   ⏵ another_tool(...)
//     ↳ ...
//
// We use plain ASCII arrows for terminals without unicode and minimal
// punctuation. No colors yet — comes with the bubbletea pass.

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/eight-acres-lab/openmelon/internal/llm"
)

// terminalTracer renders runtime events to a terminal stream.
type terminalTracer struct {
	w               io.Writer
	textInProgress  bool // true if we're mid-stream of an assistant text reply
}

func newTerminalTracer(w io.Writer) *terminalTracer {
	return &terminalTracer{w: w}
}

func (t *terminalTracer) OnTurnStart(int) { /* nothing — we let the prompt arrow do it */ }

func (t *terminalTracer) OnText(delta string) {
	t.textInProgress = true
	_, _ = io.WriteString(t.w, delta)
	if f, ok := t.w.(*os.File); ok {
		// Best-effort flush so users see incremental output even when
		// stdout is line-buffered.
		_ = f.Sync()
	}
}

func (t *terminalTracer) OnToolCall(call llm.ToolCall) {
	if t.textInProgress {
		fmt.Fprintln(t.w)
		t.textInProgress = false
	}
	fmt.Fprintf(t.w, "  ⏵ %s(%s)\n", call.Name, prettyArgs(call.Arguments))
}

func (t *terminalTracer) OnToolResult(call llm.ToolCall, content string, err error) {
	if err != nil {
		fmt.Fprintf(t.w, "    ↳ error: %v\n", err)
		return
	}
	fmt.Fprintf(t.w, "    ↳ %s\n", truncateOneLine(content, 240))
}

func (t *terminalTracer) OnTurnEnd(_ int, _ llm.FinishReason, _ llm.Usage) {
	if t.textInProgress {
		fmt.Fprintln(t.w)
		t.textInProgress = false
	}
}

// prettyArgs collapses the JSON args to a single line for display.
// If parsing fails, falls back to the raw string truncated.
func prettyArgs(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return truncateOneLine(string(raw), 80)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return truncateOneLine(string(raw), 80)
	}
	return truncateOneLine(string(b), 120)
}

func truncateOneLine(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// --- jsonl helper used by /save ---

type jsonlEncoder struct{ w io.Writer }

func newJSONLEncoder(w io.Writer) *jsonlEncoder { return &jsonlEncoder{w: w} }
func (e *jsonlEncoder) encode(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = e.w.Write(append(b, '\n'))
	return err
}
