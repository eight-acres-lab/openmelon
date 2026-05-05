package imagegen

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenRouter_Generate_NoReferences_PlainStringContent(t *testing.T) {
	pngHeader := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var raw map[string]any
		if err := json.Unmarshal(body, &raw); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		msgs := raw["messages"].([]any)
		first := msgs[0].(map[string]any)
		// No references → content must be a plain string.
		if _, ok := first["content"].(string); !ok {
			t.Errorf("expected string content, got %T (%v)", first["content"], first["content"])
		}

		dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngHeader)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{
				map[string]any{"message": map[string]any{
					"images": []any{map[string]any{"image_url": map[string]any{"url": dataURL}}},
				}},
			},
		})
	}))
	defer server.Close()

	g := &OpenRouterGenerator{apiKey: "k", baseURL: server.URL, defaultModel: "google/gemini-2.5-flash-image", httpClient: server.Client()}
	res, err := g.Generate(context.Background(), GenerateOptions{Prompt: "draw a cat"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if string(res.Data) != string(pngHeader) {
		t.Errorf("data mismatch")
	}
}

func TestOpenRouter_Generate_WithReferences_StructuredContent(t *testing.T) {
	pngHeader := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}
	jpgHeader := []byte{0xFF, 0xD8, 0xFF, 0xE0}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var raw map[string]any
		if err := json.Unmarshal(body, &raw); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		msgs := raw["messages"].([]any)
		first := msgs[0].(map[string]any)
		parts, ok := first["content"].([]any)
		if !ok {
			t.Fatalf("expected content array, got %T (%v)", first["content"], first["content"])
		}
		if len(parts) != 3 {
			t.Fatalf("expected 3 parts (2 images + 1 text), got %d", len(parts))
		}
		// First two are images, third is text.
		if parts[0].(map[string]any)["type"] != "image_url" {
			t.Errorf("part[0] not image_url: %v", parts[0])
		}
		if parts[1].(map[string]any)["type"] != "image_url" {
			t.Errorf("part[1] not image_url: %v", parts[1])
		}
		if parts[2].(map[string]any)["type"] != "text" {
			t.Errorf("part[2] not text: %v", parts[2])
		}
		// Verify content type sniffing put the right MIME on each.
		url0 := parts[0].(map[string]any)["image_url"].(map[string]any)["url"].(string)
		url1 := parts[1].(map[string]any)["image_url"].(map[string]any)["url"].(string)
		if url0[:len("data:image/png;base64,")] != "data:image/png;base64," {
			t.Errorf("first url should be image/png: %q", url0[:30])
		}
		if url1[:len("data:image/jpeg;base64,")] != "data:image/jpeg;base64," {
			t.Errorf("second url should be image/jpeg: %q", url1[:30])
		}

		dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngHeader)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{
				map[string]any{"message": map[string]any{
					"images": []any{map[string]any{"image_url": map[string]any{"url": dataURL}}},
				}},
			},
		})
	}))
	defer server.Close()

	g := &OpenRouterGenerator{apiKey: "k", baseURL: server.URL, defaultModel: "google/gemini-2.5-flash-image", httpClient: server.Client()}
	res, err := g.Generate(context.Background(), GenerateOptions{
		Prompt:          "make Lao Wang stir-fry the noodles",
		ReferenceImages: [][]byte{pngHeader, jpgHeader},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if string(res.Data) != string(pngHeader) {
		t.Errorf("data mismatch")
	}
}

func TestSniffImageContentType(t *testing.T) {
	cases := []struct {
		head []byte
		want string
	}{
		{[]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, "image/png"},
		{[]byte{0xFF, 0xD8, 0xFF, 0xE0}, "image/jpeg"},
		{append([]byte("RIFF\x00\x00\x00\x00WEBP"), 0), "image/webp"},
		{[]byte("GIF89a..."), "image/gif"},
		{[]byte{0x00, 0x01, 0x02, 0x03}, "image/png"}, // unknown → png fallback
	}
	for _, c := range cases {
		if got := sniffImageContentType(c.head); got != c.want {
			t.Errorf("sniffImageContentType(%x): got %q want %q", c.head[:4], got, c.want)
		}
	}
}
