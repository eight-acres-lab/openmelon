package tui

// tracer.go — runtime.Tracer implementation that pushes events into a
// running Bubbletea Program via Send().
//
// Bubbletea's Program is goroutine-safe for Send. The runtime calls
// these methods from whichever goroutine RunMsg() picked — usually a
// dedicated worker the TUI spawned. We do not block the runtime; Send
// is non-blocking, drops on closed program.

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/eight-acres-lab/openmelon/internal/llm"
)

// programSender is the subset of *tea.Program we use. Tests can pass
// in a stub that records sent messages.
type programSender interface {
	Send(msg tea.Msg)
}

// programTracer fans runtime events into a Bubbletea program.
type programTracer struct {
	prog programSender
}

func newProgramTracer(p programSender) *programTracer {
	return &programTracer{prog: p}
}

func (t *programTracer) OnTurnStart(turn int) {
	t.prog.Send(turnStartedMsg{Turn: turn})
}

func (t *programTracer) OnText(delta string) {
	t.prog.Send(textDeltaMsg{Delta: delta})
}

func (t *programTracer) OnToolCall(call llm.ToolCall) {
	t.prog.Send(toolCallMsg{Call: call})
}

func (t *programTracer) OnToolResult(call llm.ToolCall, content string, err error) {
	t.prog.Send(toolResultMsg{Call: call, Content: content, Err: err})
}

func (t *programTracer) OnTurnEnd(turn int, finish llm.FinishReason, usage llm.Usage) {
	t.prog.Send(turnEndedMsg{Turn: turn, Finish: finish, Usage: usage})
}
