// Package llm is OpenMelon's pluggable LLM client surface.
//
// Why pluggable: OpenMelon is opinionated about content workflows but neutral
// about model vendors. Users bring their own credentials and pick their own
// vendor; OpenMelon does not embed a default vendor or price-rank them.
//
// Today: Anthropic (Claude) and OpenAI (GPT) — covers the two cases the
// agent loop actually exercises (structured prompt synthesis + image-prompt
// drafting). Google / xAI / OpenRouter slot in as additional Client
// implementations using the same interface.
//
// Implementations use only net/http + encoding/json — no vendor SDKs,
// because the surface OpenMelon uses (single-turn completion, optional
// JSON-only output) is small and SDK churn is a real maintenance tax.
package llm

import (
	"context"
	"errors"
	"fmt"
)

// Client is the cross-vendor completion surface used by the agent loop.
//
// One method intentionally: today the agent loop is single-turn (compile a
// skill, send compiled prompt + intent, receive structured JSON, done).
// Multi-turn / tool use will arrive when the REPL mode lands; that work
// extends this interface rather than reshapes it.
type Client interface {
	// Complete sends a single-turn completion request and returns the
	// model's text response. For structured-output use, set opts.JSONOnly
	// and embed the schema in opts.System or opts.User.
	Complete(ctx context.Context, opts CompleteOptions) (string, error)

	// Provider returns the vendor slug (e.g. "anthropic", "openai") for
	// telemetry and provenance.
	Provider() string

	// Model returns the model id this client will use when CompleteOptions.Model
	// is empty.
	Model() string
}

// CompleteOptions describes a single completion request.
//
// Empty values fall back to client defaults; only System or User must be
// non-empty.
type CompleteOptions struct {
	// System is the role-setting prompt. May be empty for vendors that
	// support only user-role messages, or when the entire instruction
	// fits in User.
	System string

	// User is the per-request input. Required.
	User string

	// Model overrides the client's default model id. Empty → client default.
	Model string

	// Temperature is the sampling temperature. Zero → client default
	// (typically 0.7 for drafting, 0.2 when JSONOnly is set).
	Temperature float64

	// MaxTokens caps response length. Zero → client default (4096).
	MaxTokens int

	// JSONOnly hints the client to enforce JSON-only output where the
	// vendor supports it (OpenAI response_format, Anthropic explicit
	// instruction). The caller must still validate the returned string
	// parses as JSON — this is a hint, not a guarantee.
	JSONOnly bool
}

// ErrNoAPIKey is returned by client constructors when no key is supplied
// AND the env fallback is empty.
var ErrNoAPIKey = errors.New("llm: no API key supplied and no env fallback set")

// completeError wraps vendor errors with context. Implementations construct
// this so the agent loop can present a unified failure surface.
type completeError struct {
	provider string
	status   int
	body     string
}

func (e *completeError) Error() string {
	return fmt.Sprintf("llm[%s]: HTTP %d: %s", e.provider, e.status, e.body)
}
