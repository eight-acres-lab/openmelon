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
	anthropicDefaultModel   = "claude-sonnet-4-6"
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
// defaultModel: empty → claude-sonnet-4-6 (the current cost/perf default for
// structured-output tasks; users can override per-call via CompleteOptions.Model).
func NewAnthropic(apiKey, defaultModel string) (*AnthropicClient, error) {
	if apiKey == "" {
		apiKey = os.Getenv("ANTHROPIC_API_KEY")
	}
	if apiKey == "" {
		return nil, ErrNoAPIKey
	}
	if defaultModel == "" {
		defaultModel = anthropicDefaultModel
	}
	return &AnthropicClient{
		apiKey:       apiKey,
		baseURL:      anthropicDefaultBaseURL,
		defaultModel: defaultModel,
		httpClient:   &http.Client{Timeout: 120 * time.Second},
	}, nil
}

func (c *AnthropicClient) Provider() string { return "anthropic" }
func (c *AnthropicClient) Model() string    { return c.defaultModel }

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

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("llm[anthropic]: HTTP: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("llm[anthropic]: read response: %w", err)
	}

	if resp.StatusCode/100 != 2 {
		return "", &completeError{provider: "anthropic", status: resp.StatusCode, body: string(respBody)}
	}

	var parsed anthropicResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", fmt.Errorf("llm[anthropic]: parse response: %w (body: %s)", err, string(respBody))
	}

	// Concatenate all text blocks. In practice Claude returns one block,
	// but the API allows multiples.
	var out bytes.Buffer
	for _, block := range parsed.Content {
		if block.Type == "text" {
			out.WriteString(block.Text)
		}
	}
	return out.String(), nil
}
