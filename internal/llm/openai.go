package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/eight-acres-lab/openmelon/internal/version"
)

// OpenAI Chat Completions client.
//
// API docs: https://platform.openai.com/docs/api-reference/chat
//
// Also serves OpenRouter (passes a baseURL override) and any other
// OpenAI-compatible endpoint.

const (
	openaiDefaultBaseURL = "https://api.openai.com"
)

// OpenAIClient implements Client against OpenAI's Chat Completions API or
// any compatible endpoint (OpenRouter, vLLM, LM Studio, etc.).
type OpenAIClient struct {
	apiKey       string
	baseURL      string
	defaultModel string
	provider     string // "openai" or "openrouter" — used only in Provider() / errors
	httpClient   *http.Client
}

// NewOpenAI builds an OpenAIClient against the official OpenAI API
// (or any OpenAI-compatible endpoint).
//
// Env-var fallbacks (only used when the matching argument is ""):
//   - apiKey ← OPENAI_API_KEY
//   - baseURL ← OPENAI_BASE_URL  (host only, no /v1 suffix). Matches the
//     official OpenAI SDK convention. Useful for ChatGPT-Plus relays,
//     LiteLLM, Helicone, vLLM, LM Studio, or any compatible host.
//
// defaultModel: required — pass an explicit model id. We do not bake in
// vendor model defaults; the menu changes too often to live in source.
func NewOpenAI(apiKey, baseURL, defaultModel string) (*OpenAIClient, error) {
	if baseURL == "" {
		baseURL = os.Getenv("OPENAI_BASE_URL")
	}
	if baseURL == "" {
		baseURL = openaiDefaultBaseURL
	}
	return newOpenAILike("openai", apiKey, "OPENAI_API_KEY", baseURL, defaultModel)
}

// NewOpenRouter builds an OpenAIClient that talks to https://openrouter.ai.
// Uses the same Chat Completions wire shape as OpenAI; only the host and
// the env-var fallback differ.
//
// baseURL override: empty → OPENROUTER_BASE_URL → https://openrouter.ai/api.
// defaultModel is required (e.g. "x-ai/grok-4", "anthropic/claude-sonnet-4-6").
func NewOpenRouter(apiKey, baseURL, defaultModel string) (*OpenAIClient, error) {
	if baseURL == "" {
		baseURL = os.Getenv("OPENROUTER_BASE_URL")
	}
	if baseURL == "" {
		baseURL = "https://openrouter.ai/api"
	}
	return newOpenAILike("openrouter", apiKey, "OPENROUTER_API_KEY", baseURL, defaultModel)
}

func newOpenAILike(provider, apiKey, envVar, baseURL, defaultModel string) (*OpenAIClient, error) {
	if apiKey == "" {
		apiKey = os.Getenv(envVar)
	}
	if apiKey == "" {
		return nil, ErrNoAPIKey
	}
	if defaultModel == "" {
		return nil, ErrModelRequired
	}
	return &OpenAIClient{
		apiKey:       apiKey,
		baseURL:      baseURL,
		defaultModel: defaultModel,
		provider:     provider,
		httpClient:   &http.Client{Timeout: 120 * time.Second},
	}, nil
}

// BaseURL returns the resolved base URL for telemetry / debugging.
// Useful when --json output wants to record which endpoint was hit.
func (c *OpenAIClient) BaseURL() string { return c.baseURL }

func (c *OpenAIClient) Provider() string { return c.provider }
func (c *OpenAIClient) Model() string    { return c.defaultModel }

type openaiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openaiResponseFormat struct {
	Type string `json:"type"`
}

type openaiRequest struct {
	Model           string                `json:"model"`
	Messages        []openaiMessage       `json:"messages"`
	Temperature     float64               `json:"temperature"`
	MaxTokens       int                   `json:"max_tokens,omitempty"`
	ResponseFormat  *openaiResponseFormat `json:"response_format,omitempty"`
	Stream          bool                  `json:"stream,omitempty"`
	ReasoningEffort string                `json:"reasoning_effort,omitempty"`
}

type openaiChoice struct {
	Message openaiMessage `json:"message"`
}

type openaiResponse struct {
	Choices []openaiChoice `json:"choices"`
}

// Complete sends a chat completion request.
//
// JSONOnly maps to OpenAI's response_format={"type":"json_object"} which
// reliably constrains output to JSON. Anthropic-via-OpenRouter does not
// support that flag, so we fall back to the same explicit instruction
// pattern Anthropic uses.
func (c *OpenAIClient) Complete(ctx context.Context, opts CompleteOptions) (string, error) {
	return c.doRequest(ctx, opts, nil)
}

// Stream sends the same request as Complete but with stream=true, parses
// the SSE response, and invokes handler for each text delta. Returns the
// full accumulated text when the stream ends.
func (c *OpenAIClient) Stream(ctx context.Context, opts CompleteOptions, handler StreamHandler) (string, error) {
	return c.doRequest(ctx, opts, handler)
}

func (c *OpenAIClient) doRequest(ctx context.Context, opts CompleteOptions, handler StreamHandler) (string, error) {
	if opts.User == "" {
		return "", fmt.Errorf("llm[%s]: User is required", c.provider)
	}

	model := opts.Model
	if model == "" {
		model = c.defaultModel
	}
	temperature := opts.Temperature
	if temperature == 0 {
		if opts.JSONOnly {
			temperature = 0.2
		} else {
			temperature = 0.7
		}
	}

	messages := []openaiMessage{}
	if opts.System != "" {
		sys := opts.System
		if opts.JSONOnly && c.provider != "openai" {
			sys += "\n\nOutput ONLY a single JSON object. Do not wrap it in markdown fences. Do not add any prose."
		}
		messages = append(messages, openaiMessage{Role: "system", Content: sys})
	}
	messages = append(messages, openaiMessage{Role: "user", Content: opts.User})

	req := openaiRequest{
		Model:       model,
		Messages:    messages,
		Temperature: temperature,
		MaxTokens:   opts.MaxTokens,
		Stream:      handler != nil,
	}
	if effort := openaiReasoningEffort(opts.ReasoningEffort); effort != "" {
		req.ReasoningEffort = effort
	}
	if opts.JSONOnly && c.provider == "openai" {
		req.ResponseFormat = &openaiResponseFormat{Type: "json_object"}
	}

	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("llm[%s]: marshal request: %w", c.provider, err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("llm[%s]: build request: %w", c.provider, err)
	}
	c.setHeaders(httpReq, handler != nil)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("llm[%s]: HTTP: %w", c.provider, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		respBody, _ := io.ReadAll(resp.Body)
		return "", &completeError{provider: c.provider, status: resp.StatusCode, body: string(respBody)}
	}

	if handler == nil {
		// Non-streaming path — single JSON response.
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", fmt.Errorf("llm[%s]: read response: %w", c.provider, err)
		}
		var parsed openaiResponse
		if err := json.Unmarshal(respBody, &parsed); err != nil {
			return "", fmt.Errorf("llm[%s]: parse response: %w (body: %s)", c.provider, err, string(respBody))
		}
		if len(parsed.Choices) == 0 {
			return "", fmt.Errorf("llm[%s]: no choices in response (body: %s)", c.provider, string(respBody))
		}
		return parsed.Choices[0].Message.Content, nil
	}

	// Streaming path — parse SSE events, accumulate text deltas.
	var accumulated bytes.Buffer
	parseErr := readSSE(ctx, resp.Body, func(ev sseEvent) bool {
		var chunk openaiStreamChunk
		done, err := jsonDecode(ev.data, &chunk)
		if err != nil {
			return true // skip malformed; let the stream finish
		}
		if done {
			return false
		}
		if len(chunk.Choices) == 0 {
			return true
		}
		text := chunk.Choices[0].Delta.Content
		if text != "" {
			accumulated.WriteString(text)
			handler(text)
		}
		return true
	})
	if parseErr != nil {
		return accumulated.String(), fmt.Errorf("llm[%s]: stream: %w", c.provider, parseErr)
	}
	return accumulated.String(), nil
}

func (c *OpenAIClient) setHeaders(req *http.Request, stream bool) {
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("content-type", "application/json")
	req.Header.Set("User-Agent", openmelonUserAgent())
	if stream {
		req.Header.Set("accept", "text/event-stream")
	}
	if c.provider == "openrouter" {
		req.Header.Set("HTTP-Referer", "https://github.com/eight-acres-lab/openmelon")
		req.Header.Set("X-Title", "openmelon")
	}
}

func openmelonUserAgent() string {
	return fmt.Sprintf("openmelon-tui/%s (%s; %s)", version.Version, runtime.GOOS, runtime.GOARCH)
}

func openaiReasoningEffort(effort string) string {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "none", "minimal", "low", "medium", "high", "xhigh":
		return strings.ToLower(strings.TrimSpace(effort))
	default:
		return ""
	}
}

// openaiStreamChunk is the per-event payload for a streaming chat completion.
type openaiStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
}
