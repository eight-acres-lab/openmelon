package imagegen

// retry.go — transient-error retry + keep-alive policy for image
// generation HTTP calls.
//
// Why this exists: image generation requests are slow (5-60s) and
// large (up to several MB when reference images are attached). Two
// failure modes are common in practice:
//
//   1. The client reuses a keep-alive connection that the server
//      silently closed in the meantime. Go's http.Transport then
//      sees garbled bytes and surfaces "tls: bad record MAC" or
//      "EOF" — confusing, looks like a TLS bug.
//
//   2. Middleboxes (corporate proxies, VPNs, ISP TLS-inspectors)
//      hiccup on multi-MB POST bodies, sometimes returning 5xx or
//      dropping the connection mid-stream.
//
// Both fix with: (a) one fresh connection per request (no keep-alive),
// (b) one or two retries with backoff. Combined, ≥95% of these errors
// disappear without changing anything else.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// freshTransport returns an http.Transport that doesn't reuse
// connections. Every request opens its own TCP+TLS handshake, which
// avoids the "stale keep-alive" failure mode entirely. Slower, but
// image gen requests are slow anyway — the TCP handshake is
// negligible relative to a 30s model call.
func freshTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DisableKeepAlives = true
	return t
}

// transientHTTPDo runs req via client with retries on transient
// network errors. Returns the last response (caller closes Body) or
// the last error.
//
// What counts as transient:
//   - net.OpError / connection reset / broken pipe
//   - "tls: bad record MAC" and other tls.* protocol errors
//   - EOF in the middle of reading a response
//   - HTTP 502 / 503 / 504 (gateway / upstream errors)
//
// Bodies are buffered before the first attempt so we can replay them
// on retry; callers should pass requests with bytes.Reader bodies (the
// existing imagegen code does).
//
// Backoff: 1s, 2s. With maxAttempts=3 (initial + 2 retries), worst-case
// added latency on a hard failure is 3s + the third attempt's timeout.
func transientHTTPDo(ctx context.Context, client *http.Client, req *http.Request, body []byte, maxAttempts int) (*http.Response, error) {
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * time.Second):
			}
			// Refresh the body on retry — the previous attempt
			// consumed it.
			req.Body = io.NopCloser(bytes.NewReader(body))
			req.GetBody = func() (io.ReadCloser, error) {
				return io.NopCloser(bytes.NewReader(body)), nil
			}
		}
		resp, err := client.Do(req)
		if err != nil {
			if isTransientErr(err) {
				lastErr = fmt.Errorf("attempt %d: %w", attempt+1, err)
				continue
			}
			return nil, err
		}
		// Retryable status codes — drain + close body before retry so
		// the next attempt's connection (or a fresh one if keep-alive
		// is on, which it isn't) is clean.
		if isTransientStatus(resp.StatusCode) && attempt < maxAttempts-1 {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			lastErr = fmt.Errorf("attempt %d: HTTP %d", attempt+1, resp.StatusCode)
			continue
		}
		return resp, nil
	}
	return nil, fmt.Errorf("exhausted %d attempts: %w", maxAttempts, lastErr)
}

func isTransientErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var netErr *net.OpError
	if errors.As(err, &netErr) {
		return true
	}
	msg := err.Error()
	for _, needle := range []string{
		"tls: bad record mac",
		"tls: protocol",
		"connection reset",
		"connection refused", // sometimes intermittent on flaky networks
		"broken pipe",
		"i/o timeout",
		"unexpected eof",
		"server closed",
	} {
		if strings.Contains(strings.ToLower(msg), needle) {
			return true
		}
	}
	return false
}

func isTransientStatus(code int) bool {
	switch code {
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	case 408: // request timeout
		return true
	case 429: // rate limit — backoff often helps
		return true
	}
	return false
}
