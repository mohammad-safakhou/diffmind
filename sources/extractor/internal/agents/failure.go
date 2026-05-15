package agents

import (
	"context"
	"errors"
	"net"
	"regexp"
	"strconv"
	"strings"
)

// classifyError returns a coarse, machine-readable label for an error
// surfaced by promptAgent. The classifier is intentionally narrow: it
// only labels patterns that are unambiguous; everything else falls back
// to "unknown" so we don't lie about the cause in the failure report.
//
// Returned classes (kept stable; the SPA will switch on them):
//   - cancelled: caller cancelled the run (Ctrl-C, SIGTERM, ctx deadline)
//   - timeout:   net.Error.Timeout() OR clear timeout substring
//   - http_4xx / http_5xx: HTTP status pattern in the error string
//   - rate_limit: 429 / "rate limit" / "overloaded"
//   - schema:    "no structured payload" or json schema mismatch
//   - network:   socket / dns / TLS errors that aren't timeouts
//   - unknown:   anything else
func classifyError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "cancelled"
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "timeout"
	}
	msg := err.Error()
	lower := strings.ToLower(msg)

	if strings.Contains(lower, "rate limit") ||
		strings.Contains(lower, "rate_limit") ||
		strings.Contains(lower, "overloaded") ||
		strings.Contains(lower, " 429 ") ||
		strings.HasSuffix(lower, " 429") {
		return "rate_limit"
	}
	if strings.Contains(lower, "no structured payload") ||
		strings.Contains(lower, "json schema") ||
		strings.Contains(lower, "schema validation") ||
		strings.Contains(lower, "invalid json") {
		return "schema"
	}
	if strings.Contains(lower, "client.timeout") ||
		strings.Contains(lower, "i/o timeout") ||
		strings.Contains(lower, "deadline exceeded") ||
		strings.Contains(lower, "tls handshake timeout") {
		return "timeout"
	}
	if strings.Contains(lower, "connection reset") ||
		strings.Contains(lower, "connection refused") ||
		strings.Contains(lower, "no such host") ||
		strings.Contains(lower, "broken pipe") ||
		strings.Contains(lower, "eof") ||
		strings.Contains(lower, "dial tcp") {
		return "network"
	}
	if status := extractHTTPStatus(msg); status >= 400 {
		switch {
		case status == 429:
			return "rate_limit"
		case status >= 500:
			return "http_5xx"
		default:
			return "http_4xx"
		}
	}
	return "unknown"
}

var statusRegex = regexp.MustCompile(`(?:\b|status[ =:]|code[ =:]| )(\d{3})\b`)

// extractHTTPStatus pulls a 3-digit HTTP status out of an error message
// when one is clearly present (e.g. "prompt failed: 502 Bad Gateway").
// Returns 0 when no recognisable status is found.
func extractHTTPStatus(msg string) int {
	matches := statusRegex.FindAllStringSubmatch(msg, -1)
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		if n >= 100 && n <= 599 {
			return n
		}
	}
	return 0
}
