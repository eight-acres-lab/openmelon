package imagegen

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewOpenAI_NoKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	if _, err := NewOpenAI("", ""); err != ErrNoAPIKey {
		t.Fatalf("expected ErrNoAPIKey, got %v", err)
	}
}

func TestOpenAI_Generate_HappyPath(t *testing.T) {
	pngHeader := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/generations" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("missing auth header")
		}

		var req openaiImageRequest
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if req.Model != "gpt-image-1" {
			t.Errorf("expected default model, got %q", req.Model)
		}
		if req.Size != "1024x1024" {
			t.Errorf("expected default size, got %q", req.Size)
		}
		if req.Prompt != "a cat" {
			t.Errorf("prompt mismatch: %q", req.Prompt)
		}

		_ = json.NewEncoder(w).Encode(openaiImageResponse{
			Data: []openaiImageData{{B64JSON: base64.StdEncoding.EncodeToString(pngHeader)}},
		})
	}))
	defer server.Close()

	g := &OpenAIGenerator{
		apiKey:       "test-key",
		baseURL:      server.URL,
		defaultModel: "gpt-image-1",
		httpClient:   server.Client(),
	}
	res, err := g.Generate(context.Background(), GenerateOptions{Prompt: "a cat"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if res.ContentType != "image/png" {
		t.Errorf("content type %q", res.ContentType)
	}
	if res.SizeBytes != len(pngHeader) {
		t.Errorf("size %d, want %d", res.SizeBytes, len(pngHeader))
	}
	if string(res.Data[:8]) != string(pngHeader) {
		t.Errorf("expected PNG header in data")
	}
	if res.Provider != "openai" || res.Model != "gpt-image-1" {
		t.Errorf("provenance fields wrong: %s / %s", res.Provider, res.Model)
	}
}

func TestOpenAI_Generate_RequiresPrompt(t *testing.T) {
	g := &OpenAIGenerator{apiKey: "k", baseURL: "http://x", defaultModel: "x", httpClient: &http.Client{}}
	if _, err := g.Generate(context.Background(), GenerateOptions{}); err == nil {
		t.Fatal("expected error for empty prompt")
	}
}

func TestOpenAI_Generate_NIsClampedToOne(t *testing.T) {
	g := &OpenAIGenerator{apiKey: "k", baseURL: "http://x", defaultModel: "x", httpClient: &http.Client{}}
	_, err := g.Generate(context.Background(), GenerateOptions{Prompt: "x", N: 4})
	if err == nil || !strings.Contains(err.Error(), "N=1") {
		t.Errorf("expected N=1 restriction, got %v", err)
	}
}

func TestOpenAI_Generate_PropagatesHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad prompt"}`))
	}))
	defer server.Close()

	g := &OpenAIGenerator{apiKey: "k", baseURL: server.URL, defaultModel: "gpt-image-1", httpClient: server.Client()}
	_, err := g.Generate(context.Background(), GenerateOptions{Prompt: "x"})
	if err == nil || !strings.Contains(err.Error(), "400") {
		t.Errorf("expected 400 error, got %v", err)
	}
}

func TestProviderAndModel(t *testing.T) {
	g := &OpenAIGenerator{defaultModel: "gpt-image-1"}
	if g.Provider() != "openai" || g.Model() != "gpt-image-1" {
		t.Errorf("accessors wrong: %s / %s", g.Provider(), g.Model())
	}
}
