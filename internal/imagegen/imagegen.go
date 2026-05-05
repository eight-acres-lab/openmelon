// Package imagegen wraps OpenAI's image generation endpoint.
//
// Today only OpenAI is supported because that's what the food-street-realism
// reference package targets (gpt-image-family). Stability / Midjourney /
// other vendors slot in as additional Generator implementations behind the
// same interface; the agent loop will dispatch by the package's
// model_profile.
//
// We use only net/http + encoding/json so consumers don't drag in an SDK.
package imagegen

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// ErrNoAPIKey is returned when no key is supplied AND the env fallback
// is empty.
var ErrNoAPIKey = errors.New("imagegen: no API key supplied and OPENAI_API_KEY env is empty")

// ErrModelRequired is returned when no model id is passed.
var ErrModelRequired = errors.New("imagegen: no model id supplied — pass --image-model")

// Generator generates a single image from a text prompt.
//
// Returns the raw image bytes (PNG today; future vendors may return
// other formats — check ContentType on the result for the actual MIME).
type Generator interface {
	Generate(ctx context.Context, opts GenerateOptions) (*Result, error)
	Provider() string
	Model() string
}

// GenerateOptions describes a single generation.
type GenerateOptions struct {
	// Prompt is the image-generation instruction. Required.
	Prompt string

	// Model overrides the generator's default. Empty → generator default.
	Model string

	// Size in WxH. Empty → "1024x1024". Not all vendors accept arbitrary
	// sizes; consult the vendor docs.
	Size string

	// N is the number of images to generate. Today only N=1 is supported
	// (the agent loop runs multiple Generate calls when it wants variants
	// rather than asking the vendor for a batch).
	N int

	// ReferenceImages, if non-empty, are passed to the model as input
	// images alongside the text prompt. Used for character / reference
	// consistency: pass a portrait, ask the model to keep that character.
	//
	// Provider support varies — OpenRouter chat-completions models
	// (Gemini image, GPT-5 image) accept them via the content array;
	// the OpenAI /v1/images/generations endpoint does not (use
	// /v1/images/edits for that, which ImageEditor wires when needed).
	// A generator that ignores ReferenceImages should still succeed —
	// callers may want a text-only fallback.
	ReferenceImages [][]byte
}

// Result is a single generated image.
type Result struct {
	Data        []byte
	ContentType string // e.g. "image/png"
	Provider    string
	Model       string
	Prompt      string // echoed back so callers can record provenance
	SizeBytes   int
}

// OpenAIGenerator implements Generator against OpenAI's images/generations
// endpoint.
type OpenAIGenerator struct {
	apiKey       string
	baseURL      string
	defaultModel string
	httpClient   *http.Client
}

const (
	openaiDefaultBaseURL = "https://api.openai.com"
)

// NewOpenAI builds an OpenAIGenerator.
//
// Env-var fallbacks (only used when the matching argument is ""):
//   - apiKey ← OPENAI_API_KEY
//   - baseURL ← OPENAI_BASE_URL  (host only, no /v1 suffix). Useful for
//     ChatGPT-Plus relays, LiteLLM, Helicone, or any OpenAI-compatible host.
//
// defaultModel: required — pass an explicit model id (e.g. "gpt-image-1",
// "dall-e-3"). We do not bake in vendor model defaults.
func NewOpenAI(apiKey, baseURL, defaultModel string) (*OpenAIGenerator, error) {
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
	}
	if apiKey == "" {
		return nil, ErrNoAPIKey
	}
	if baseURL == "" {
		baseURL = os.Getenv("OPENAI_BASE_URL")
	}
	if baseURL == "" {
		baseURL = openaiDefaultBaseURL
	}
	if defaultModel == "" {
		return nil, ErrModelRequired
	}
	return &OpenAIGenerator{
		apiKey:       apiKey,
		baseURL:      baseURL,
		defaultModel: defaultModel,
		// Image generation is slow — give it room.
		httpClient: &http.Client{Timeout: 5 * time.Minute},
	}, nil
}

// BaseURL returns the resolved base URL for telemetry / debugging.
func (g *OpenAIGenerator) BaseURL() string { return g.baseURL }

func (g *OpenAIGenerator) Provider() string { return "openai" }
func (g *OpenAIGenerator) Model() string    { return g.defaultModel }

type openaiImageRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Size   string `json:"size,omitempty"`
	N      int    `json:"n,omitempty"`
}

type openaiImageData struct {
	B64JSON string `json:"b64_json"`
}

type openaiImageResponse struct {
	Data []openaiImageData `json:"data"`
}

// Generate sends a single image-generation request and returns the
// decoded image bytes.
func (g *OpenAIGenerator) Generate(ctx context.Context, opts GenerateOptions) (*Result, error) {
	if opts.Prompt == "" {
		return nil, fmt.Errorf("imagegen[openai]: Prompt is required")
	}
	model := opts.Model
	if model == "" {
		model = g.defaultModel
	}
	size := opts.Size
	if size == "" {
		size = "1024x1024"
	}
	n := opts.N
	if n == 0 {
		n = 1
	}
	if n != 1 {
		return nil, fmt.Errorf("imagegen[openai]: only N=1 is supported today (got %d)", n)
	}

	body, err := json.Marshal(openaiImageRequest{Model: model, Prompt: opts.Prompt, Size: size, N: n})
	if err != nil {
		return nil, fmt.Errorf("imagegen[openai]: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.baseURL+"/v1/images/generations", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("imagegen[openai]: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+g.apiKey)
	req.Header.Set("content-type", "application/json")

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("imagegen[openai]: HTTP: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("imagegen[openai]: read response: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("imagegen[openai]: HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var parsed openaiImageResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("imagegen[openai]: parse response: %w (body: %s)", err, string(respBody))
	}
	if len(parsed.Data) == 0 || parsed.Data[0].B64JSON == "" {
		return nil, fmt.Errorf("imagegen[openai]: empty data in response (body: %s)", string(respBody))
	}

	imgBytes, err := base64.StdEncoding.DecodeString(parsed.Data[0].B64JSON)
	if err != nil {
		return nil, fmt.Errorf("imagegen[openai]: decode b64: %w", err)
	}

	return &Result{
		Data:        imgBytes,
		ContentType: "image/png",
		Provider:    "openai",
		Model:       model,
		Prompt:      opts.Prompt,
		SizeBytes:   len(imgBytes),
	}, nil
}
