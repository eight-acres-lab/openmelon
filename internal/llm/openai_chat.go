package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Chat implements ToolCaller against OpenAI's Chat Completions API
// (and OpenRouter, which uses the same wire shape).
//
// We don't stream tool-call deltas — they're awkward to reassemble and
// our runtime only needs the final tool calls per turn. If we want
// streaming "model is thinking" output later, the right move is a
// separate StreamChat that fans both text deltas and finalized tool
// calls into a callback.
func (c *OpenAIClient) Chat(ctx context.Context, in ChatRequest) (*ChatResponse, error) {
	if len(in.Messages) == 0 {
		return nil, fmt.Errorf("llm[%s]: Chat requires at least one message", c.provider)
	}
	model := in.Model
	if model == "" {
		model = c.defaultModel
	}
	temperature := in.Temperature
	if temperature == 0 {
		temperature = 0.7
	}

	wireMessages := make([]openaiChatMessage, 0, len(in.Messages))
	for _, m := range in.Messages {
		wireMessages = append(wireMessages, toWireMessage(m))
	}

	wireTools := make([]openaiToolWire, 0, len(in.Tools))
	for _, t := range in.Tools {
		wireTools = append(wireTools, openaiToolWire{
			Type: "function",
			Function: openaiFunctionWire{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}

	body, err := json.Marshal(openaiChatRequestWire{
		Model:       model,
		Messages:    wireMessages,
		Tools:       wireTools,
		Temperature: temperature,
		MaxTokens:   in.MaxTokens,
	})
	if err != nil {
		return nil, fmt.Errorf("llm[%s]: marshal: %w", c.provider, err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("llm[%s]: build request: %w", c.provider, err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("content-type", "application/json")
	if c.provider == "openrouter" {
		httpReq.Header.Set("HTTP-Referer", "https://github.com/eight-acres-lab/openmelon")
		httpReq.Header.Set("X-Title", "openmelon")
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("llm[%s]: HTTP: %w", c.provider, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("llm[%s]: read response: %w", c.provider, err)
	}
	if resp.StatusCode/100 != 2 {
		return nil, &completeError{provider: c.provider, status: resp.StatusCode, body: string(respBody)}
	}

	var parsed openaiChatResponseWire
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("llm[%s]: parse response: %w (body: %s)", c.provider, err, string(respBody))
	}
	if len(parsed.Choices) == 0 {
		return nil, fmt.Errorf("llm[%s]: no choices in response (body: %s)", c.provider, string(respBody))
	}
	ch := parsed.Choices[0]

	out := &ChatResponse{
		Message:      fromWireMessage(ch.Message),
		FinishReason: mapFinishReason(ch.FinishReason),
	}
	return out, nil
}

// --- wire types ---

type openaiChatRequestWire struct {
	Model       string              `json:"model"`
	Messages    []openaiChatMessage `json:"messages"`
	Tools       []openaiToolWire    `json:"tools,omitempty"`
	Temperature float64             `json:"temperature"`
	MaxTokens   int                 `json:"max_tokens,omitempty"`
}

type openaiChatMessage struct {
	Role       string               `json:"role"`
	Content    string               `json:"content,omitempty"`
	ToolCalls  []openaiToolCallWire `json:"tool_calls,omitempty"`
	ToolCallID string               `json:"tool_call_id,omitempty"`
}

type openaiToolWire struct {
	Type     string             `json:"type"`
	Function openaiFunctionWire `json:"function"`
}

type openaiFunctionWire struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type openaiToolCallWire struct {
	ID       string                 `json:"id"`
	Type     string                 `json:"type"`
	Function openaiToolCallFunction `json:"function"`
}

type openaiToolCallFunction struct {
	Name string `json:"name"`
	// Arguments is wire-encoded as a JSON string (yes, a JSON string
	// containing JSON). We round-trip it through json.RawMessage on our
	// side so callers can re-parse without double-decoding.
	Arguments string `json:"arguments"`
}

type openaiChatResponseWire struct {
	Choices []struct {
		Message      openaiChatMessage `json:"message"`
		FinishReason string            `json:"finish_reason"`
	} `json:"choices"`
}

// --- mapping helpers ---

func toWireMessage(m Message) openaiChatMessage {
	out := openaiChatMessage{
		Role:       string(m.Role),
		Content:    m.Content,
		ToolCallID: m.ToolCallID,
	}
	for _, tc := range m.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, openaiToolCallWire{
			ID:   tc.ID,
			Type: "function",
			Function: openaiToolCallFunction{
				Name:      tc.Name,
				Arguments: string(tc.Arguments),
			},
		})
	}
	return out
}

func fromWireMessage(w openaiChatMessage) Message {
	out := Message{
		Role:       Role(w.Role),
		Content:    w.Content,
		ToolCallID: w.ToolCallID,
	}
	for _, tc := range w.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: json.RawMessage(tc.Function.Arguments),
		})
	}
	return out
}

func mapFinishReason(s string) FinishReason {
	switch s {
	case "stop":
		return FinishStop
	case "tool_calls":
		return FinishToolCalls
	case "length":
		return FinishLength
	default:
		return FinishOther
	}
}
