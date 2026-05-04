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
// Two completion methods today:
//   - Complete: single-turn, returns the full response when done. Used by
//     callers that don't care about per-token output (tests, CI, future
//     batch workflows).
//   - Stream: same single-turn semantics, but invokes a handler for each
//     text delta as it arrives, returning the full accumulated response
//     at the end. Used by the agent loop in TTY mode so the user sees
//     progress instead of staring at a blank terminal for 30 seconds.
//
// Multi-turn / tool use will extend this interface rather than reshape it.
type Client interface {
	// Complete sends a single-turn completion request and returns the
	// model's text response. For structured-output use, set opts.JSONOnly
	// and embed the schema in opts.System or opts.User.
	Complete(ctx context.Context, opts CompleteOptions) (string, error)

	// Stream is like Complete, but invokes handler with each text delta
	// as it arrives. Returns the full accumulated response.
	//
	// handler may be nil — in that case Stream behaves like Complete.
	// Implementations must be tolerant of slow handlers (do not buffer
	// unbounded; let the network back-pressure naturally).
	Stream(ctx context.Context, opts CompleteOptions, handler StreamHandler) (string, error)

	// Provider returns the vendor slug (e.g. "anthropic", "openai") for
	// telemetry and provenance.
	Provider() string

	// Model returns the model id this client will use when CompleteOptions.Model
	// is empty.
	Model() string
}

// StreamHandler is invoked for each text delta during streaming.
// Empty deltas are filtered out before the handler is called.
type StreamHandler func(delta string)

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
