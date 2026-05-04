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

// OpenAI Chat Completions client.
//
// API docs: https://platform.openai.com/docs/api-reference/chat
//
// Also serves OpenRouter (passes a baseURL override) and any other
// OpenAI-compatible endpoint.

const (
	openaiDefaultBaseURL = "https://api.openai.com"
	openaiDefaultModel   = "gpt-5"
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

// NewOpenAI builds an OpenAIClient against the official OpenAI API.
//
// apiKey: explicit key, or "" to read OPENAI_API_KEY from env.
// defaultModel: empty → gpt-5 (the current default; override per-call via
// CompleteOptions.Model).
func NewOpenAI(apiKey, defaultModel string) (*OpenAIClient, error) {
	return newOpenAILike("openai", apiKey, "OPENAI_API_KEY", openaiDefaultBaseURL, defaultModel, openaiDefaultModel)
}

// NewOpenRouter builds an OpenAIClient that talks to https://openrouter.ai.
// Uses the same Chat Completions wire shape as OpenAI; only the host and
// the env-var fallback differ.
func NewOpenRouter(apiKey, defaultModel string) (*OpenAIClient, error) {
	return newOpenAILike("openrouter", apiKey, "OPENROUTER_API_KEY", "https://openrouter.ai/api", defaultModel, "anthropic/claude-sonnet-4-6")
}

func newOpenAILike(provider, apiKey, envVar, baseURL, defaultModel, fallbackModel string) (*OpenAIClient, error) {
	if apiKey == "" {
		apiKey = os.Getenv(envVar)
	}
	if apiKey == "" {
		return nil, ErrNoAPIKey
	}
	if defaultModel == "" {
		defaultModel = fallbackModel
	}
	return &OpenAIClient{
		apiKey:       apiKey,
		baseURL:      baseURL,
		defaultModel: defaultModel,
		provider:     provider,
		httpClient:   &http.Client{Timeout: 120 * time.Second},
	}, nil
}

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
	Model          string                `json:"model"`
	Messages       []openaiMessage       `json:"messages"`
	Temperature    float64               `json:"temperature"`
	MaxTokens      int                   `json:"max_tokens,omitempty"`
	ResponseFormat *openaiResponseFormat `json:"response_format,omitempty"`
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
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("content-type", "application/json")
	if c.provider == "openrouter" {
		// OpenRouter recommends these for routing telemetry. Optional.
		httpReq.Header.Set("HTTP-Referer", "https://github.com/eight-acres-lab/openmelon")
		httpReq.Header.Set("X-Title", "openmelon")
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("llm[%s]: HTTP: %w", c.provider, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("llm[%s]: read response: %w", c.provider, err)
	}

	if resp.StatusCode/100 != 2 {
		return "", &completeError{provider: c.provider, status: resp.StatusCode, body: string(respBody)}
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
