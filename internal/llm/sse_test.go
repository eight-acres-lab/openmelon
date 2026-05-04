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

func TestAnthropic_Stream_AccumulatesTextDeltas(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		// Two text deltas → "Hello world"; intersperse a non-text event
		// so we exercise the event-name filter.
		fmt.Fprint(w, "event: message_start\n")
		fmt.Fprint(w, "data: {\"type\":\"message_start\"}\n\n")
		fmt.Fprint(w, "event: content_block_delta\n")
		fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\n")
		fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\" world\"}}\n\n")
		fmt.Fprint(w, "event: message_stop\n")
		fmt.Fprint(w, "data: {\"type\":\"message_stop\"}\n\n")
	}))
	defer server.Close()

	c := &AnthropicClient{
		apiKey: "test", baseURL: server.URL, defaultModel: "claude-test",
		httpClient: server.Client(),
	}

	var deltas []string
	got, err := c.Stream(context.Background(), CompleteOptions{User: "hi"}, func(d string) {
		deltas = append(deltas, d)
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if got != "Hello world" {
		t.Errorf("accumulated = %q, want %q", got, "Hello world")
	}
	if len(deltas) != 2 || deltas[0] != "Hello" || deltas[1] != " world" {
		t.Errorf("deltas = %#v", deltas)
	}
}

func TestAnthropic_Stream_RequestSetsStreamFlag(t *testing.T) {
	var sawStream bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req anthropicRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		sawStream = req.Stream
		w.Header().Set("content-type", "text/event-stream")
		fmt.Fprint(w, "event: message_stop\ndata: {}\n\n")
	}))
	defer server.Close()

	c := &AnthropicClient{apiKey: "k", baseURL: server.URL, defaultModel: "x", httpClient: server.Client()}
	_, _ = c.Stream(context.Background(), CompleteOptions{User: "hi"}, func(string) {})
	if !sawStream {
		t.Error("expected stream:true in request body")
	}
}

func TestOpenAI_Stream_AccumulatesContentDeltas(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\" world\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	c := &OpenAIClient{apiKey: "k", baseURL: server.URL, defaultModel: "gpt-x", provider: "openai", httpClient: server.Client()}

	var deltas []string
	got, err := c.Stream(context.Background(), CompleteOptions{User: "hi"}, func(d string) {
		deltas = append(deltas, d)
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if got != "Hello world" {
		t.Errorf("accumulated = %q", got)
	}
	if len(deltas) != 2 {
		t.Errorf("deltas = %#v", deltas)
	}
}

func TestOpenAI_Stream_HandlesDONESentinel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
		// Anything after [DONE] should not be processed (we return false
		// from the SSE callback).
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"AFTER_DONE\"}}]}\n\n")
	}))
	defer server.Close()

	c := &OpenAIClient{apiKey: "k", baseURL: server.URL, defaultModel: "gpt-x", provider: "openai", httpClient: server.Client()}
	got, err := c.Stream(context.Background(), CompleteOptions{User: "hi"}, func(string) {})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if strings.Contains(got, "AFTER_DONE") {
		t.Errorf("text after [DONE] should be ignored, got %q", got)
	}
}

func TestStream_PropagatesNon2xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad"}`))
	}))
	defer server.Close()

	c := &AnthropicClient{apiKey: "k", baseURL: server.URL, defaultModel: "x", httpClient: server.Client()}
	_, err := c.Stream(context.Background(), CompleteOptions{User: "hi"}, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("expected 401, got %v", err)
	}
}
