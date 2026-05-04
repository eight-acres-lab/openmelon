package llm

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// SSE parsing helpers shared by Anthropic and OpenAI streaming clients.
//
// Both vendors emit Server-Sent Events. The framing rules are the same:
// each event is a sequence of "field: value" lines terminated by a blank
// line. The "data: " field carries a JSON payload (or the literal
// "[DONE]" sentinel for OpenAI). Anthropic also emits an "event: " field
// naming the event type, which we use to ignore non-content events.
//
// We hand-parse rather than pull in a dep: the format is small and we
// already deliberately avoid vendor SDKs (see internal/llm/client.go).

// sseEvent is one parsed SSE event. Only the fields we care about.
type sseEvent struct {
	event string // OpenAI omits this; Anthropic uses content_block_delta etc.
	data  string // raw JSON or "[DONE]"
}

// readSSE parses an SSE response stream and invokes onEvent for each
// complete event. Returns when the stream ends, the context is
// cancelled, or onEvent returns false.
func readSSE(ctx context.Context, body io.Reader, onEvent func(sseEvent) bool) error {
	scanner := bufio.NewScanner(body)
	// SSE events can be large; default 64 KiB buffer is too small for
	// some Anthropic message_start payloads.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var ev sseEvent
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line := scanner.Text()
		if line == "" {
			// End of event. Dispatch and reset.
			if ev.data != "" {
				if !onEvent(ev) {
					return nil
				}
			}
			ev = sseEvent{}
			continue
		}
		if strings.HasPrefix(line, ":") {
			// SSE comment line, e.g. ": ping". Ignore.
			continue
		}
		if strings.HasPrefix(line, "event:") {
			ev.event = strings.TrimSpace(line[len("event:"):])
			continue
		}
		if strings.HasPrefix(line, "data:") {
			payload := strings.TrimSpace(line[len("data:"):])
			if ev.data == "" {
				ev.data = payload
			} else {
				// Multi-line data fields concatenate with \n per spec.
				ev.data = ev.data + "\n" + payload
			}
			continue
		}
		// Ignore other field types (id:, retry:).
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("sse: read: %w", err)
	}
	// Dispatch any unterminated event at EOF.
	if ev.data != "" {
		_ = onEvent(ev)
	}
	return nil
}

// jsonDecode decodes a JSON payload into v, ignoring the [DONE] sentinel.
// Returns (true, nil) on [DONE], (false, nil) on success, (false, err) on
// parse failure.
func jsonDecode(payload string, v any) (done bool, err error) {
	if payload == "[DONE]" {
		return true, nil
	}
	if err := json.Unmarshal([]byte(payload), v); err != nil {
		return false, fmt.Errorf("sse: parse data: %w (payload: %s)", err, payload)
	}
	return false, nil
}
