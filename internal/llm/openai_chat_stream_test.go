package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// streamingServer fakes an OpenAI-compatible streaming endpoint by
// writing each event in `events` as `data: <json>\n\n`. Terminates with
// a [DONE] marker.
func streamingServer(t *testing.T, events []map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ua := r.Header.Get("User-Agent"); !strings.Contains(ua, "openmelon-tui/") {
			t.Errorf("expected openmelon User-Agent, got %q", ua)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		for _, ev := range events {
			b, _ := json.Marshal(ev)
			fmt.Fprintf(w, "data: %s\n\n", b)
			if flusher != nil {
				flusher.Flush()
			}
		}
		fmt.Fprintf(w, "data: [DONE]\n\n")
	}))
}

func TestStreamChat_AccumulatesTextDeltasAndCallsHandler(t *testing.T) {
	server := streamingServer(t, []map[string]any{
		{"choices": []any{map[string]any{"delta": map[string]any{"role": "assistant"}}}},
		{"choices": []any{map[string]any{"delta": map[string]any{"content": "Hel"}}}},
		{"choices": []any{map[string]any{"delta": map[string]any{"content": "lo "}}}},
		{"choices": []any{map[string]any{"delta": map[string]any{"content": "world"}}}},
		{"choices": []any{map[string]any{"delta": map[string]any{}, "finish_reason": "stop"}}},
	})
	defer server.Close()

	c := &OpenAIClient{apiKey: "k", baseURL: server.URL, defaultModel: "gpt", provider: "openai", httpClient: server.Client()}
	var seen []string
	resp, err := c.StreamChat(context.Background(), ChatRequest{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	}, StreamChatHandler{
		OnText: func(d string) { seen = append(seen, d) },
	})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	if got := strings.Join(seen, ""); got != "Hello world" {
		t.Errorf("text deltas concatenated: %q", got)
	}
	if resp.Message.Content != "Hello world" {
		t.Errorf("final content: %q", resp.Message.Content)
	}
	if resp.FinishReason != FinishStop {
		t.Errorf("finish reason: %v", resp.FinishReason)
	}
}

func TestStreamChat_ReassemblesToolCallDeltas(t *testing.T) {
	server := streamingServer(t, []map[string]any{
		// First chunk for the tool call: id + name, no arguments yet.
		{"choices": []any{map[string]any{"delta": map[string]any{
			"tool_calls": []any{map[string]any{
				"index": 0,
				"id":    "call_1",
				"function": map[string]any{
					"name":      "list_characters",
					"arguments": "",
				},
			}},
		}}}},
		// Arguments stream across multiple chunks.
		{"choices": []any{map[string]any{"delta": map[string]any{
			"tool_calls": []any{map[string]any{
				"index": 0,
				"function": map[string]any{
					"arguments": "{\"que",
				},
			}},
		}}}},
		{"choices": []any{map[string]any{"delta": map[string]any{
			"tool_calls": []any{map[string]any{
				"index": 0,
				"function": map[string]any{
					"arguments": "ry\":\"vendor\"}",
				},
			}},
		}}}},
		{"choices": []any{map[string]any{"delta": map[string]any{}, "finish_reason": "tool_calls"}}},
	})
	defer server.Close()

	c := &OpenAIClient{apiKey: "k", baseURL: server.URL, defaultModel: "gpt", provider: "openai", httpClient: server.Client()}
	resp, err := c.StreamChat(context.Background(), ChatRequest{
		Messages: []Message{{Role: RoleUser, Content: "find vendors"}},
	}, StreamChatHandler{})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.Message.ToolCalls))
	}
	tc := resp.Message.ToolCalls[0]
	if tc.ID != "call_1" || tc.Name != "list_characters" {
		t.Errorf("tool call mismatch: %+v", tc)
	}
	if string(tc.Arguments) != `{"query":"vendor"}` {
		t.Errorf("arguments: %q", string(tc.Arguments))
	}
	if resp.FinishReason != FinishToolCalls {
		t.Errorf("finish reason: %v", resp.FinishReason)
	}
}

func TestStreamChat_TwoToolCallsPreserveOrder(t *testing.T) {
	server := streamingServer(t, []map[string]any{
		// Two tool calls interleaved by index.
		{"choices": []any{map[string]any{"delta": map[string]any{
			"tool_calls": []any{
				map[string]any{"index": 0, "id": "a", "function": map[string]any{"name": "first", "arguments": "{}"}},
				map[string]any{"index": 1, "id": "b", "function": map[string]any{"name": "second", "arguments": "{}"}},
			},
		}}}},
		{"choices": []any{map[string]any{"delta": map[string]any{}, "finish_reason": "tool_calls"}}},
	})
	defer server.Close()

	c := &OpenAIClient{apiKey: "k", baseURL: server.URL, defaultModel: "gpt", provider: "openai", httpClient: server.Client()}
	resp, err := c.StreamChat(context.Background(), ChatRequest{Messages: []Message{{Role: RoleUser, Content: "go"}}}, StreamChatHandler{})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	if len(resp.Message.ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(resp.Message.ToolCalls))
	}
	if resp.Message.ToolCalls[0].Name != "first" || resp.Message.ToolCalls[1].Name != "second" {
		t.Errorf("tool call order: %+v", resp.Message.ToolCalls)
	}
}

func TestStreamChat_HTTPErrorIsSurfaced(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad"}`))
	}))
	defer server.Close()

	c := &OpenAIClient{apiKey: "k", baseURL: server.URL, defaultModel: "gpt", provider: "openai", httpClient: server.Client()}
	_, err := c.StreamChat(context.Background(), ChatRequest{Messages: []Message{{Role: RoleUser, Content: "x"}}}, StreamChatHandler{})
	if err == nil || !strings.Contains(err.Error(), "400") {
		t.Errorf("expected 400 error, got %v", err)
	}
}
