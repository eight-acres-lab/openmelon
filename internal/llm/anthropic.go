package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// Anthropic Messages API client.
//
// API docs: https://docs.anthropic.com/en/api/messages
//
// We only use the synchronous, non-streaming form — the agent loop does
// streaming at the orchestration layer (multiple sequential Complete calls),
// not at the token level today.

const (
	anthropicDefaultBaseURL = "https://api.anthropic.com"
	anthropicAPIVersion     = "2023-06-01"
)

// AnthropicClient implements Client against Anthropic's Messages API.
type AnthropicClient struct {
	apiKey       string
	baseURL      string
	defaultModel string
	httpClient   *http.Client
}

// NewAnthropic builds an AnthropicClient.
//
// apiKey: explicit key, or "" to read ANTHROPIC_API_KEY from env.
// baseURL: explicit host, or "" to read ANTHROPIC_BASE_URL / use default.
// defaultModel: required — caller must pass an explicit model id (we
// deliberately do not bake in vendor model defaults; the model menu
// changes too often to live in source).
func NewAnthropic(apiKey, baseURL, defaultModel string) (*AnthropicClient, error) {
	if apiKey == "" {
		apiKey = os.Getenv("ANTHROPIC_API_KEY")
	}
	if apiKey == "" {
		return nil, ErrNoAPIKey
	}
	if baseURL == "" {
		baseURL = os.Getenv("ANTHROPIC_BASE_URL")
	}
	if baseURL == "" {
		baseURL = anthropicDefaultBaseURL
	}
	if defaultModel == "" {
		return nil, ErrModelRequired
	}
	return &AnthropicClient{
		apiKey:       apiKey,
		baseURL:      baseURL,
		defaultModel: defaultModel,
		httpClient:   &http.Client{Timeout: 120 * time.Second},
	}, nil
}

func (c *AnthropicClient) Provider() string { return "anthropic" }
func (c *AnthropicClient) Model() string    { return c.defaultModel }
func (c *AnthropicClient) BaseURL() string  { return c.baseURL }

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicRequest struct {
	Model       string             `json:"model"`
	MaxTokens   int                `json:"max_tokens"`
	Temperature float64            `json:"temperature"`
	System      string             `json:"system,omitempty"`
	Messages    []anthropicMessage `json:"messages"`
	Stream      bool               `json:"stream,omitempty"`
}

type anthropicContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type anthropicResponse struct {
	Content []anthropicContentBlock `json:"content"`
}

// Complete sends one user message and returns the model's text reply.
//
// JSONOnly is honored by appending an explicit "Output ONLY a single JSON
// object..." instruction to the system prompt — Anthropic does not have an
// API-level response_format flag the way OpenAI does, but the explicit
// instruction is reliable for Claude 3.5+.
func (c *AnthropicClient) Complete(ctx context.Context, opts CompleteOptions) (string, error) {
	return c.doRequest(ctx, opts, nil)
}

// Stream sends the same request as Complete but with stream=true, parses
// the SSE response, and invokes handler for each text delta. Returns the
// full accumulated text when the stream ends.
func (c *AnthropicClient) Stream(ctx context.Context, opts CompleteOptions, handler StreamHandler) (string, error) {
	return c.doRequest(ctx, opts, handler)
}

// doRequest is the shared implementation. handler==nil means non-streaming.
func (c *AnthropicClient) doRequest(ctx context.Context, opts CompleteOptions, handler StreamHandler) (string, error) {
	if opts.User == "" {
		return "", fmt.Errorf("llm[anthropic]: User is required")
	}

	model := opts.Model
	if model == "" {
		model = c.defaultModel
	}
	maxTokens := opts.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096
	}
	temperature := opts.Temperature
	if temperature == 0 {
		if opts.JSONOnly {
			temperature = 0.2
		} else {
			temperature = 0.7
		}
	}

	system := opts.System
	if opts.JSONOnly {
		jsonHint := "\n\nOutput ONLY a single JSON object matching the schema described above. Do not wrap it in markdown fences. Do not add any prose before or after the JSON."
		if system == "" {
			system = jsonHint
		} else {
			system = system + jsonHint
		}
	}

	body, err := json.Marshal(anthropicRequest{
		Model:       model,
		MaxTokens:   maxTokens,
		Temperature: temperature,
		System:      system,
		Messages:    []anthropicMessage{{Role: "user", Content: opts.User}},
		Stream:      handler != nil,
	})
	if err != nil {
		return "", fmt.Errorf("llm[anthropic]: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("llm[anthropic]: build request: %w", err)
	}
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", anthropicAPIVersion)
	req.Header.Set("content-type", "application/json")
	req.Header.Set("User-Agent", openmelonUserAgent())
	if handler != nil {
		req.Header.Set("accept", "text/event-stream")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("llm[anthropic]: HTTP: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		respBody, _ := io.ReadAll(resp.Body)
		return "", &completeError{provider: "anthropic", status: resp.StatusCode, body: string(respBody)}
	}

	if handler == nil {
		// Non-streaming path — single JSON response.
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", fmt.Errorf("llm[anthropic]: read response: %w", err)
		}
		var parsed anthropicResponse
		if err := json.Unmarshal(respBody, &parsed); err != nil {
			return "", fmt.Errorf("llm[anthropic]: parse response: %w (body: %s)", err, string(respBody))
		}
		var out bytes.Buffer
		for _, block := range parsed.Content {
			if block.Type == "text" {
				out.WriteString(block.Text)
			}
		}
		return out.String(), nil
	}

	// Streaming path — parse SSE events, accumulate text deltas.
	var accumulated bytes.Buffer
	parseErr := readSSE(ctx, resp.Body, func(ev sseEvent) bool {
		// Anthropic uses event names; we only care about content_block_delta.
		if ev.event != "content_block_delta" {
			return true
		}
		var delta anthropicStreamDelta
		if _, err := jsonDecode(ev.data, &delta); err != nil {
			return true // skip malformed events; let the stream finish
		}
		if delta.Delta.Type == "text_delta" && delta.Delta.Text != "" {
			accumulated.WriteString(delta.Delta.Text)
			handler(delta.Delta.Text)
		}
		return true
	})
	if parseErr != nil {
		return accumulated.String(), fmt.Errorf("llm[anthropic]: stream: %w", parseErr)
	}
	return accumulated.String(), nil
}

// anthropicStreamDelta is the per-event payload for content_block_delta.
// Anthropic also emits message_start, content_block_start, message_delta,
// content_block_stop, and message_stop — we ignore all of those for the
// single-turn use case.
type anthropicStreamDelta struct {
	Delta struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"delta"`
}
