package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// StreamChat implements StreamingToolCaller against OpenAI Chat
// Completions (and OpenRouter, same wire shape).
//
// Streams text deltas via h.OnText as they arrive. Tool-call deltas
// accumulate silently — vendors split tool_call.function.arguments
// across many chunks ("{\"que" + "ry\":\"vendor\"}"), so streaming them
// raw isn't useful. The final ChatResponse contains fully reassembled
// tool calls keyed by their tool_call_index.
//
// Cancel via ctx — readSSE checks the context between events.
func (c *OpenAIClient) StreamChat(ctx context.Context, in ChatRequest, h StreamChatHandler) (*ChatResponse, error) {
	if len(in.Messages) == 0 {
		return nil, fmt.Errorf("llm[%s]: StreamChat requires at least one message", c.provider)
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

	body, err := json.Marshal(openaiChatStreamRequestWire{
		Model:           model,
		Messages:        wireMessages,
		Tools:           wireTools,
		Temperature:     temperature,
		MaxTokens:       in.MaxTokens,
		Stream:          true,
		StreamOptions:   &openaiStreamOptionsWire{IncludeUsage: true},
		ReasoningEffort: openaiReasoningEffort(in.ReasoningEffort),
	})
	if err != nil {
		return nil, fmt.Errorf("llm[%s]: marshal: %w", c.provider, err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("llm[%s]: build request: %w", c.provider, err)
	}
	c.setHeaders(httpReq, true)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("llm[%s]: HTTP: %w", c.provider, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, &completeError{provider: c.provider, status: resp.StatusCode, body: string(respBody)}
	}

	// Per-stream accumulator: text + tool-call args (indexed by the
	// tool_call.index field, NOT by ToolCall.ID — id only appears on
	// the first delta for each tool call).
	var textBuf bytes.Buffer
	toolByIdx := map[int]*ToolCall{}
	toolArgsByIdx := map[int]*bytes.Buffer{}
	var finishReason FinishReason = FinishOther
	var usage Usage

	parseErr := readSSE(ctx, resp.Body, func(ev sseEvent) bool {
		var chunk openaiChatStreamChunk
		done, err := jsonDecode(ev.data, &chunk)
		if err != nil {
			return true // skip malformed; let the stream finish
		}
		if done {
			return false
		}
		// Final chunk often has empty Choices but populated Usage.
		if chunk.Usage != nil {
			usage = Usage{
				PromptTokens:     chunk.Usage.PromptTokens,
				CompletionTokens: chunk.Usage.CompletionTokens,
				TotalTokens:      chunk.Usage.TotalTokens,
			}
		}
		if len(chunk.Choices) == 0 {
			return true
		}
		ch := chunk.Choices[0]
		if ch.Delta.Content != "" {
			textBuf.WriteString(ch.Delta.Content)
			if h.OnText != nil {
				h.OnText(ch.Delta.Content)
			}
		}
		for _, tcd := range ch.Delta.ToolCalls {
			tc, ok := toolByIdx[tcd.Index]
			if !ok {
				tc = &ToolCall{}
				toolByIdx[tcd.Index] = tc
				toolArgsByIdx[tcd.Index] = &bytes.Buffer{}
			}
			if tcd.ID != "" {
				tc.ID = tcd.ID
			}
			if tcd.Function.Name != "" {
				tc.Name = tcd.Function.Name
			}
			if tcd.Function.Arguments != "" {
				toolArgsByIdx[tcd.Index].WriteString(tcd.Function.Arguments)
			}
		}
		if ch.FinishReason != "" {
			finishReason = mapFinishReason(ch.FinishReason)
		}
		return true
	})
	if parseErr != nil {
		return nil, fmt.Errorf("llm[%s]: stream: %w", c.provider, parseErr)
	}

	// Materialize tool calls in index order so a multi-call turn stays
	// stable across runs.
	maxIdx := -1
	for idx := range toolByIdx {
		if idx > maxIdx {
			maxIdx = idx
		}
	}
	var calls []ToolCall
	for i := 0; i <= maxIdx; i++ {
		tc, ok := toolByIdx[i]
		if !ok {
			continue
		}
		args := toolArgsByIdx[i].Bytes()
		if len(args) == 0 {
			args = []byte("{}")
		}
		calls = append(calls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Name,
			Arguments: json.RawMessage(args),
		})
	}

	return &ChatResponse{
		Message: Message{
			Role:      RoleAssistant,
			Content:   textBuf.String(),
			ToolCalls: calls,
		},
		FinishReason: finishReason,
		Usage:        usage,
	}, nil
}

// --- streaming wire shapes ---

type openaiChatStreamRequestWire struct {
	Model           string                   `json:"model"`
	Messages        []openaiChatMessage      `json:"messages"`
	Tools           []openaiToolWire         `json:"tools,omitempty"`
	Temperature     float64                  `json:"temperature"`
	MaxTokens       int                      `json:"max_tokens,omitempty"`
	Stream          bool                     `json:"stream"`
	StreamOptions   *openaiStreamOptionsWire `json:"stream_options,omitempty"`
	ReasoningEffort string                   `json:"reasoning_effort,omitempty"`
}

// openaiStreamOptionsWire enables usage in the final stream chunk.
// Without this, OpenAI-compatible APIs (incl. OpenRouter) omit usage
// during streaming; we'd have to call /usage separately or estimate.
type openaiStreamOptionsWire struct {
	IncludeUsage bool `json:"include_usage"`
}

type openaiChatStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content   string                    `json:"content"`
			ToolCalls []openaiToolCallDeltaWire `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	// Populated only on the final chunk when stream_options.include_usage
	// is true. May be nil on every other chunk.
	Usage *openaiUsageWire `json:"usage"`
}

type openaiToolCallDeltaWire struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}
