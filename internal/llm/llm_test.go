package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Network behavior is exercised against a local httptest server so the
// vendor wire shape is verified without burning real API credits.

func TestNew_UnknownProvider(t *testing.T) {
	if _, err := New("notavendor", "key", ""); err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestAnthropic_NoKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	if _, err := NewAnthropic("", ""); err != ErrNoAPIKey {
		t.Fatalf("expected ErrNoAPIKey, got %v", err)
	}
}

func TestOpenAI_NoKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	if _, err := NewOpenAI("", ""); err != ErrNoAPIKey {
		t.Fatalf("expected ErrNoAPIKey, got %v", err)
	}
}

func TestAnthropic_RequestShape(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/v1/messages" {
			t.Errorf("expected /v1/messages, got %s", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "test-key" {
			t.Errorf("expected x-api-key=test-key, got %q", r.Header.Get("x-api-key"))
		}
		if r.Header.Get("anthropic-version") == "" {
			t.Errorf("missing anthropic-version header")
		}

		var req anthropicRequest
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("unmarshal: %v (body: %s)", err, body)
		}
		if req.Model != "claude-test" {
			t.Errorf("expected model=claude-test, got %q", req.Model)
		}
		if !strings.Contains(req.System, "test system") {
			t.Errorf("expected system to contain 'test system', got %q", req.System)
		}
		if !strings.Contains(req.System, "Output ONLY a single JSON object") {
			t.Errorf("expected JSON-only hint appended to system, got %q", req.System)
		}
		if len(req.Messages) != 1 || req.Messages[0].Role != "user" || req.Messages[0].Content != "test user" {
			t.Errorf("unexpected messages: %+v", req.Messages)
		}
		if req.Temperature != 0.2 {
			t.Errorf("expected temperature=0.2 for JSONOnly, got %v", req.Temperature)
		}

		_ = json.NewEncoder(w).Encode(anthropicResponse{
			Content: []anthropicContentBlock{{Type: "text", Text: `{"ok":true}`}},
		})
	}))
	defer server.Close()

	c := &AnthropicClient{
		apiKey:       "test-key",
		baseURL:      server.URL,
		defaultModel: "claude-test",
		httpClient:   server.Client(),
	}
	got, err := c.Complete(context.Background(), CompleteOptions{
		System:   "test system",
		User:     "test user",
		JSONOnly: true,
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got != `{"ok":true}` {
		t.Errorf("unexpected body: %q", got)
	}
}

func TestAnthropic_ErrorPropagates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid key"}}`))
	}))
	defer server.Close()

	c := &AnthropicClient{
		apiKey:       "bad-key",
		baseURL:      server.URL,
		defaultModel: "claude-test",
		httpClient:   server.Client(),
	}
	_, err := c.Complete(context.Background(), CompleteOptions{User: "hi"})
	if err == nil {
		t.Fatal("expected error from 401 response")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("expected 401 in error, got %v", err)
	}
}

func TestOpenAI_RequestShape_JSONResponseFormat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("expected /v1/chat/completions, got %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("expected Bearer test-key, got %q", r.Header.Get("Authorization"))
		}

		var req openaiRequest
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("unmarshal: %v (body: %s)", err, body)
		}
		if req.Model != "gpt-test" {
			t.Errorf("expected model=gpt-test, got %q", req.Model)
		}
		if req.ResponseFormat == nil || req.ResponseFormat.Type != "json_object" {
			t.Errorf("expected response_format=json_object for openai+JSONOnly, got %+v", req.ResponseFormat)
		}
		if len(req.Messages) != 2 || req.Messages[0].Role != "system" || req.Messages[1].Role != "user" {
			t.Errorf("unexpected messages: %+v", req.Messages)
		}

		resp := openaiResponse{Choices: []openaiChoice{{Message: openaiMessage{Role: "assistant", Content: `{"ok":true}`}}}}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	c := &OpenAIClient{
		apiKey:       "test-key",
		baseURL:      server.URL,
		defaultModel: "gpt-test",
		provider:     "openai",
		httpClient:   server.Client(),
	}
	got, err := c.Complete(context.Background(), CompleteOptions{
		System:   "you are a test",
		User:     "hi",
		JSONOnly: true,
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got != `{"ok":true}` {
		t.Errorf("unexpected body: %q", got)
	}
}

func TestOpenRouter_AddsTelemetryHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("HTTP-Referer") == "" {
			t.Errorf("expected HTTP-Referer header for openrouter")
		}
		if r.Header.Get("X-Title") == "" {
			t.Errorf("expected X-Title header for openrouter")
		}
		_ = json.NewEncoder(w).Encode(openaiResponse{
			Choices: []openaiChoice{{Message: openaiMessage{Content: "ok"}}},
		})
	}))
	defer server.Close()

	c := &OpenAIClient{
		apiKey:       "k",
		baseURL:      server.URL,
		defaultModel: "any",
		provider:     "openrouter",
		httpClient:   server.Client(),
	}
	if _, err := c.Complete(context.Background(), CompleteOptions{User: "hi"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
}

func TestProviderAndModel(t *testing.T) {
	a := &AnthropicClient{defaultModel: "claude-x"}
	if a.Provider() != "anthropic" || a.Model() != "claude-x" {
		t.Errorf("Anthropic accessors wrong: %s / %s", a.Provider(), a.Model())
	}
	o := &OpenAIClient{provider: "openai", defaultModel: "gpt-y"}
	if o.Provider() != "openai" || o.Model() != "gpt-y" {
		t.Errorf("OpenAI accessors wrong: %s / %s", o.Provider(), o.Model())
	}
}
