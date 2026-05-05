package generation

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/eight-acres-lab/openmelon/internal/llm"
)

// LLMProvider implements Provider by calling an llm.Client.
// The compiled prompt is sent as the System role; the request Intent as the User role.
type LLMProvider struct {
	client llm.Client
}

// NewLLMProvider returns a LLMProvider backed by the given client.
func NewLLMProvider(client llm.Client) *LLMProvider {
	return &LLMProvider{client: client}
}

// Generate sends req.Prompt as the system prompt and req.Intent as the user message
// to the LLM client. When stdout is a TTY the response is streamed to stderr so the
// user sees progress. Non-TTY (CI, pipes) falls back to a single blocking Complete call.
func (p *LLMProvider) Generate(ctx context.Context, req *Request) (string, *Trace, error) {
	start := time.Now()

	userMsg := req.Intent
	if userMsg == "" {
		fmt.Fprintln(os.Stderr, "warning: --intent is empty; using placeholder text for LLM user message")
		userMsg = "(no intent provided)"
	}

	opts := llm.CompleteOptions{
		System: req.Prompt,
		User:   userMsg,
	}

	var content string
	var err error

	if isTTY() {
		content, err = p.client.Stream(ctx, opts, func(delta string) {
			os.Stderr.WriteString(delta) //nolint:errcheck
		})
	} else {
		content, err = p.client.Complete(ctx, opts)
	}

	if err != nil {
		if ctx.Err() != nil {
			return "", nil, &ProviderError{
				Code:    "timeout",
				Message: fmt.Sprintf("llm: request timed out (%s)", p.client.Provider()),
				Wrapped: ctx.Err(),
			}
		}
		return "", nil, &ProviderError{
			Code:    "llm_error",
			Message: fmt.Sprintf("llm: %s returned error: %v", p.client.Provider(), err),
			Wrapped: err,
		}
	}

	if content == "" {
		return "", nil, &ProviderError{
			Code:    "empty_output",
			Message: fmt.Sprintf("llm: %s/%s returned empty content", p.client.Provider(), p.client.Model()),
		}
	}

	trace := &Trace{
		ProviderType: "llm",
		Model:        p.client.Model(),
		DurationSec:  time.Since(start).Seconds(),
	}
	return content, trace, nil
}

// isTTY reports whether stdout is a character device (interactive terminal).
func isTTY() bool {
	info, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
