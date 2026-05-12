package repl

// render.go — terminal-friendly Tracer that writes to stdout.
//
// Layout per turn:
//
//	[user types]
//	<streamed assistant text appears here>
//	● tool_name  compact summary
//	  └ compact result
//	<more streamed assistant text>
//	● another_tool  compact summary
//	  └ compact result

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/eight-acres-lab/openmelon/internal/llm"
)

var (
	replToolDotStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Bold(true)
	replToolStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("7")).Bold(true)
	replToolSummaryStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	replResultStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
	replErrorStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
)

// terminalTracer renders runtime events to a terminal stream.
type terminalTracer struct {
	w              io.Writer
	textInProgress bool // true if we're buffering an assistant markdown reply
	markdown       strings.Builder
}

func newTerminalTracer(w io.Writer) *terminalTracer {
	return &terminalTracer{w: w}
}

func renderHistory(w io.Writer, messages []llm.Message) {
	if len(messages) == 0 {
		return
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "%s\n", renderHistoryRule(w, fmt.Sprintf("prior conversation (%d messages)", len(messages))))
	toolNames := make(map[string]string)
	for i, msg := range messages {
		before := i > 0
		renderHistoricMessage(w, msg, before, toolNames)
	}
	fmt.Fprintf(w, "%s\n", renderHistoryRule(w, "continue below"))
}

func renderHistoricMessage(w io.Writer, msg llm.Message, spacer bool, toolNames map[string]string) {
	switch msg.Role {
	case llm.RoleSystem:
		return
	case llm.RoleUser:
		if spacer {
			fmt.Fprintln(w)
		}
		renderUserMessage(w, msg.Content)
	case llm.RoleAssistant:
		if strings.TrimSpace(msg.Content) != "" {
			if spacer {
				fmt.Fprintln(w)
			}
			renderMarkdownBlock(w, strings.TrimRight(msg.Content, "\n"), " ")
		}
		for _, call := range msg.ToolCalls {
			if call.ID != "" {
				toolNames[call.ID] = call.Name
			}
			if call.Name == "finish" {
				continue
			}
			renderToolCallBlock(w, call)
		}
	case llm.RoleTool:
		toolName := toolNames[msg.ToolCallID]
		if toolName == "finish" {
			renderFinishResult(w, msg.Content, nil)
			fmt.Fprintln(w)
			return
		}
		if errMsg := toolErrorMessage(msg.Content); errMsg != "" {
			renderWrappedText(w, replErrorStyle.Render("└ error: "+errMsg), "  ")
		} else {
			renderToolResultBlock(w, toolName, msg.Content, nil)
		}
		fmt.Fprintln(w)
	}
}

func renderUserMessage(w io.Writer, content string) {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		fmt.Fprintln(w, ">")
		return
	}
	renderWrappedText(w, "> "+lines[0], "")
	for _, line := range lines[1:] {
		renderWrappedText(w, line, "  ")
	}
}

func (t *terminalTracer) OnTurnStart(int) { /* nothing — we let the prompt arrow do it */ }

func (t *terminalTracer) OnText(delta string) {
	t.textInProgress = true
	t.markdown.WriteString(delta)
	t.flushMarkdown(false)
	if f, ok := t.w.(*os.File); ok {
		// Best-effort flush so users see incremental output even when
		// stdout is line-buffered.
		_ = f.Sync()
	}
}

func (t *terminalTracer) OnToolCall(call llm.ToolCall) {
	if t.textInProgress {
		t.flushMarkdown(true)
		t.textInProgress = false
	}
	if call.Name == "finish" {
		return
	}
	renderToolCallBlock(t.w, call)
}

func (t *terminalTracer) OnToolResult(call llm.ToolCall, content string, err error) {
	if call.Name == "finish" {
		renderFinishResult(t.w, content, err)
		return
	}
	renderToolResultBlock(t.w, call.Name, content, err)
	fmt.Fprintln(t.w)
}

func (t *terminalTracer) OnTurnEnd(_ int, _ llm.FinishReason, _ llm.Usage) {
	if t.textInProgress {
		t.flushMarkdown(true)
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
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func toolErrorMessage(content string) string {
	var obj map[string]any
	if err := json.Unmarshal([]byte(content), &obj); err != nil {
		return ""
	}
	v, ok := obj["error"]
	if !ok {
		return ""
	}
	switch msg := v.(type) {
	case string:
		return strings.TrimSpace(msg)
	default:
		return strings.TrimSpace(fmt.Sprint(msg))
	}
}

func renderToolCallBlock(w io.Writer, call llm.ToolCall) {
	fmt.Fprintln(w)
	renderWrappedText(w, formatToolCallLine(call), "")
}

func renderToolResultBlock(w io.Writer, toolName, content string, err error) {
	if err != nil {
		renderWrappedText(w, replErrorStyle.Render("└ error: "+err.Error()), "  ")
		return
	}
	if msg := toolErrorMessage(content); msg != "" {
		renderWrappedText(w, replErrorStyle.Render("└ error: "+msg), "  ")
		return
	}
	summary := toolResultSummary(toolName, content)
	if summary == "" {
		summary = "(done)"
	}
	renderWrappedText(w, replResultStyle.Render("└ "+summary), "  ")
}

func renderFinishResult(w io.Writer, content string, err error) {
	if err != nil {
		renderWrappedText(w, replErrorStyle.Render("error: "+err.Error()), " ")
		fmt.Fprintln(w)
		return
	}
	if msg := toolErrorMessage(content); msg != "" {
		renderWrappedText(w, replErrorStyle.Render("error: "+msg), " ")
		fmt.Fprintln(w)
		return
	}
	obj := jsonObjectBytes([]byte(content))
	if len(obj) == 0 {
		renderMarkdownBlock(w, content, " ")
		fmt.Fprintln(w)
		return
	}
	summary := stringField(obj, "summary")
	if summary != "" {
		renderMarkdownBlock(w, summary, " ")
	}
	artifacts := artifactStrings(obj)
	if len(artifacts) > 0 {
		if summary != "" {
			fmt.Fprintln(w)
		}
		for _, path := range artifacts {
			renderWrappedText(w, replResultStyle.Render("artifact: "+path), " ")
		}
	}
	if summary != "" || len(artifacts) > 0 {
		fmt.Fprintln(w)
	}
}

func formatToolCallLine(call llm.ToolCall) string {
	name := replToolStyle.Render(call.Name)
	summary := toolCallSummary(call)
	if summary == "" {
		return fmt.Sprintf("%s %s", replToolDotStyle.Render("●"), name)
	}
	return fmt.Sprintf("%s %s  %s", replToolDotStyle.Render("●"), name, replToolSummaryStyle.Render(summary))
}

func toolCallSummary(call llm.ToolCall) string {
	obj := jsonObject(call.Arguments)
	if len(obj) == 0 {
		return prettyArgs(call.Arguments)
	}
	switch call.Name {
	case "generate_image":
		return joinSummaryParts(
			stringField(obj, "label"),
			stringField(obj, "size"),
			shortField(obj, "prompt", 110),
			countField(obj, "reference_images", "refs"),
		)
	case "save_artifact":
		return joinSummaryParts(
			stringField(obj, "slug"),
			shortPath(stringField(obj, "image_path")),
		)
	case "register_asset":
		return joinSummaryParts(
			stringField(obj, "space_id"),
			firstNonEmpty(stringField(obj, "id"), stringField(obj, "kind")),
			shortField(obj, "description", 90),
		)
	case "finish":
		return shortField(obj, "summary", 110)
	default:
		return fallbackArgSummary(obj, call.Arguments)
	}
}

func toolResultSummary(toolName, content string) string {
	if strings.TrimSpace(content) == "" {
		return "(no output)"
	}
	if toolName == "finish" {
		if obj := jsonObjectBytes([]byte(content)); len(obj) > 0 {
			return joinSummaryParts(shortField(obj, "summary", 120), artifactsCount(obj))
		}
	}
	if obj := jsonObjectBytes([]byte(content)); len(obj) > 0 {
		if path := stringField(obj, "path"); path != "" {
			switch toolName {
			case "generate_image":
				return "saved " + shortPath(path)
			case "save_artifact":
				return "artifact " + shortPath(path)
			default:
				return shortPath(path)
			}
		}
		if id := stringField(obj, "id"); id != "" {
			return "ok " + id
		}
		if summary := stringField(obj, "summary"); summary != "" {
			return truncateOneLine(summary, 140)
		}
		if ok, exists := obj["ok"]; exists {
			return "ok " + fmt.Sprint(ok)
		}
	}
	if arr := jsonArrayObjects([]byte(content)); len(arr) > 0 {
		first := arr[0]
		if path := stringField(first, "path"); path != "" {
			if len(arr) == 1 {
				return "saved " + shortPath(path)
			}
			return fmt.Sprintf("saved %d files, first %s", len(arr), shortPath(path))
		}
		if id := stringField(first, "id"); id != "" {
			if len(arr) == 1 {
				return "ok " + id
			}
			return fmt.Sprintf("ok %d items, first %s", len(arr), id)
		}
	}
	return truncateOneLine(content, 180)
}

func jsonObject(raw json.RawMessage) map[string]any {
	return jsonObjectBytes(raw)
}

func jsonObjectBytes(raw []byte) map[string]any {
	var obj map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &obj) != nil {
		return nil
	}
	return obj
}

func jsonArrayObjects(raw []byte) []map[string]any {
	var arr []map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &arr) != nil {
		return nil
	}
	return arr
}

func stringField(obj map[string]any, key string) string {
	v, ok := obj[key]
	if !ok || v == nil {
		return ""
	}
	switch typed := v.(type) {
	case string:
		return strings.TrimSpace(typed)
	case fmt.Stringer:
		return strings.TrimSpace(typed.String())
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func shortField(obj map[string]any, key string, limit int) string {
	return truncateOneLine(stringField(obj, key), limit)
}

func countField(obj map[string]any, key, label string) string {
	v, ok := obj[key]
	if !ok || v == nil {
		return ""
	}
	switch typed := v.(type) {
	case []any:
		if len(typed) == 0 {
			return ""
		}
		return fmt.Sprintf("%d %s", len(typed), label)
	case []string:
		if len(typed) == 0 {
			return ""
		}
		return fmt.Sprintf("%d %s", len(typed), label)
	default:
		return ""
	}
}

func artifactsCount(obj map[string]any) string {
	if artifacts := artifactStrings(obj); len(artifacts) > 0 {
		return fmt.Sprintf("%d artifact(s)", len(artifacts))
	}
	return ""
}

func artifactStrings(obj map[string]any) []string {
	v, ok := obj["artifacts"]
	if !ok || v == nil {
		return nil
	}
	switch typed := v.(type) {
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if s := strings.TrimSpace(fmt.Sprint(item)); s != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if s := strings.TrimSpace(item); s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func fallbackArgSummary(obj map[string]any, raw json.RawMessage) string {
	for _, key := range []string{"name", "id", "title", "space_id", "query", "path", "command", "summary", "description"} {
		if v := shortField(obj, key, 100); v != "" {
			return v
		}
	}
	return prettyArgs(raw)
}

func joinSummaryParts(parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return strings.Join(out, " · ")
}

func shortPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	dir := filepath.Base(filepath.Dir(path))
	base := filepath.Base(path)
	if dir == "." || dir == string(filepath.Separator) || dir == "" {
		return base
	}
	return filepath.Join(dir, base)
}

func renderHistoryRule(w io.Writer, label string) string {
	width := terminalWidth(w) - rightWrapBuffer
	if width < 28 {
		width = 28
	}
	text := " " + label + " "
	remaining := width - ansi.StringWidth(text)
	if remaining < 4 {
		return dividerStyle.Render("── " + label)
	}
	left := remaining / 2
	right := remaining - left
	return dividerStyle.Render(strings.Repeat("─", left) + text + strings.Repeat("─", right))
}

func (t *terminalTracer) flushMarkdown(force bool) {
	raw := t.markdown.String()
	if raw == "" {
		return
	}
	if !force && !hasStableMarkdownBoundary(raw) {
		return
	}
	renderMarkdownBlock(t.w, raw, " ")
	fmt.Fprintln(t.w)
	t.markdown.Reset()
}

func hasStableMarkdownBoundary(raw string) bool {
	trimmed := strings.TrimRight(raw, " \t")
	return strings.HasSuffix(trimmed, "\n\n")
}

func renderMarkdownBlock(w io.Writer, markdown, prefix string) {
	markdown = strings.TrimRight(markdown, "\n")
	if strings.TrimSpace(markdown) == "" {
		return
	}
	rendered := renderMarkdownWithWidth(markdown, wrappedTextWidth(w, prefix))
	renderWrappedText(w, rendered, prefix)
}

func renderWrappedText(w io.Writer, text, prefix string) {
	width := wrappedTextWidth(w, prefix)
	for _, line := range strings.Split(text, "\n") {
		for _, wrapped := range wrapDisplayLine(line, width) {
			fmt.Fprintln(w, prefix+wrapped)
		}
	}
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
