// Package tui is openmelon's bubbletea-based interactive surface.
//
// Architecture: the runtime runs in a goroutine and emits Tracer
// callbacks. tracer.go converts each callback into a tea.Msg pushed
// into the Program via Send(). model.go is a pure Bubbletea Model that
// reacts to those messages — no synchronous calls into the runtime.
//
// Messages are intentionally small (each carries one event), so the
// Update path can pattern-match cleanly.
package tui

import (
	"github.com/eight-acres-lab/openmelon/internal/llm"
	"github.com/eight-acres-lab/openmelon/internal/runtime"
)

// turnStartedMsg fires when the runtime begins a new model turn. The
// TUI uses it to start the spinner.
type turnStartedMsg struct{ Turn int }

// textDeltaMsg carries one streamed text chunk from the model.
type textDeltaMsg struct{ Delta string }

// toolCallMsg announces a tool the model is about to call.
type toolCallMsg struct{ Call llm.ToolCall }

// toolResultMsg carries the rendered result of a tool call. Err is
// non-nil only when the dispatch itself failed (not when the model
// reported an error inside the JSON body — those are normal results).
type toolResultMsg struct {
	Call    llm.ToolCall
	Content string
	Err     error
}

// turnEndedMsg fires when the model's turn is done.
type turnEndedMsg struct {
	Turn   int
	Finish llm.FinishReason
	Usage  llm.Usage
}

// runDoneMsg signals the entire Run() call returned. The TUI re-arms
// the input box and persists the new history.
type runDoneMsg struct {
	Result *runtime.RunResult
	Err    error
}
