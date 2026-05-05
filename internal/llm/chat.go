// chat.go — multi-turn message-list completion with tool calls.
//
// Lives next to client.go but uses a separate ToolCaller interface so
// implementations that don't yet support tools (e.g. our Anthropic
// client today) compile cleanly. Runtime callers do a type assertion
// on Client → ToolCaller and surface a clear error if the underlying
// provider can't do tool use yet.

package llm

import (
	"context"
	"encoding/json"
	"errors"
)

// ErrToolUseUnsupported is returned by clients that don't yet implement
// Chat. The runtime surfaces this with a clear "switch to openrouter or
// openai" hint instead of a stack trace.
var ErrToolUseUnsupported = errors.New("llm: this provider does not support tool calls yet — use openai or openrouter")

// Role is a chat message role. Mirrors OpenAI / Anthropic conventions.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is one entry in a chat history.
type Message struct {
	Role Role `json:"role"`

	// Content is the text body. For tool messages, Content carries the
	// tool's response (typically JSON-stringified). Empty when an
	// assistant message is purely a tool call.
	Content string `json:"content,omitempty"`

	// ToolCalls is set on assistant messages that ask the model to call
	// one or more tools. Mirrors OpenAI's wire shape.
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`

	// ToolCallID is set on tool messages — references the assistant's
	// tool-call id this message responds to.
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// Tool describes a callable function the model can choose to invoke.
//
// Parameters is a JSON schema (passed verbatim to the vendor). Keep it
// small — vendors charge for tokens spent in the schema.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// ToolCall is the model's request to invoke a tool.
type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// FinishReason captures why the model stopped emitting tokens this turn.
type FinishReason string

const (
	FinishStop      FinishReason = "stop"
	FinishToolCalls FinishReason = "tool_calls"
	FinishLength    FinishReason = "length"
	FinishOther     FinishReason = "other"
)

// ChatRequest is one turn of a multi-turn conversation.
type ChatRequest struct {
	Messages    []Message
	Tools       []Tool
	Temperature float64 // 0 → vendor default (~0.7)
	MaxTokens   int     // 0 → vendor default
	Model       string  // empty → client default
}

// ChatResponse is the model's reply for one turn.
type ChatResponse struct {
	Message      Message
	FinishReason FinishReason
}

// ToolCaller is implemented by clients that support multi-turn chat
// with tool calls. Use a type assertion to detect support:
//
//	tc, ok := client.(llm.ToolCaller)
//	if !ok { return llm.ErrToolUseUnsupported }
type ToolCaller interface {
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
}
