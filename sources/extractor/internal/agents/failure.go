package agents

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
)

// ErrStuck is the sentinel returned by promptAgent when the liveness
// watchdog determined that an in-flight prompt is stuck (no observable
// progress on the OpenCode session for IdleTimeout, with no running
// tool call and no pending permission). The orchestrator surfaces it
// as error_class=stuck so the failure report and dashboard can render
// the right remediation tip.
var ErrStuck = errors.New("stuck")

// stuckError wraps ErrStuck with a human-readable cause string. The
// orchestrator includes the cause in the failure report so the
// operator can see *why* we declared the call stuck (e.g. "no part
// growth for 127s; last tool: read /path/to/big.go (completed 130s
// ago)").
type stuckError struct {
	cause string
}

func (e *stuckError) Error() string { return fmt.Sprintf("stuck: %s", e.cause) }
func (e *stuckError) Unwrap() error { return ErrStuck }

// newStuckError constructs a stuckError that satisfies errors.Is(_,
// ErrStuck). Used by the liveness watchdog when it decides to abort.
func newStuckError(cause string) error { return &stuckError{cause: cause} }

// classifyError returns a coarse, machine-readable label for an error
// surfaced by promptAgent. The classifier is intentionally narrow: it
// only labels patterns that are unambiguous; everything else falls back
// to "unknown" so we don't lie about the cause in the failure report.
//
// Returned classes (kept stable; the SPA will switch on them):
//   - cancelled: caller cancelled the run (Ctrl-C, SIGTERM, ctx deadline)
//   - timeout:   net.Error.Timeout() OR clear timeout substring
//   - stuck:     liveness watchdog observed no progress for IdleTimeout
//     AND no tool was running / no permission was pending
//   - auth:      OAuth token expired, invalid api key, 401, etc.
//     (matches nested provider errors too, not just the
//     top-level HTTP status)
//   - quota:     credits exhausted, billing problem, "insufficient
//     quota", "you've reached your limit", etc.
//   - http_4xx / http_5xx: HTTP status pattern in the error string
//   - rate_limit: 429 / "rate limit" / "overloaded"
//   - schema:    "no structured payload" or json schema mismatch
//   - network:   socket / dns / TLS errors that aren't timeouts
//   - unknown:   anything else
//
// ORDER MATTERS: net.Error.Timeout() is checked BEFORE the
// errors.Is(context.DeadlineExceeded) sentinel match. Go's net/http
// wraps its own http.Client.Timeout firing inside a *url.Error whose
// Err field is context.DeadlineExceeded; without this ordering, those
// per-call HTTP timeouts get misclassified as "cancelled" (caller
// asked us to stop) when they are actually root-cause "timeout"
// (request itself ran too long). See
// .diffmind/runs/20260515T123031Z for the cautionary tale.
func classifyError(err error) string {
	if err == nil {
		return ""
	}
	// "Stuck" is a DiffMind-specific verdict produced by the liveness
	// watchdog, NOT a real network/transport error. Check it first so
	// other matchers can't mislabel it.
	if errors.Is(err, ErrStuck) {
		return "stuck"
	}
	// Check net.Error.Timeout() first so wrapped per-call timeouts are
	// classified as "timeout" rather than the more generic "cancelled".
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "cancelled"
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
	// Auth/quota are checked BEFORE schema because providers
	// frequently return the real cause inside a wrapper that ALSO
	// says "no structured payload" or "APIError: ..." — and we want
	// to surface the actionable cause, not the wrapper. Examples we
	// have seen in the wild:
	//
	//   APIError: (Encountered invalidated oauth token for user, ...)
	//   {"error":{"code":"insufficient_quota","message":"You exceeded your current quota..."}}
	//
	// The schema classifier would happily eat those; the user is
	// then told "the model didn't honour the schema" when the real
	// fix is "renew your token" or "top up credits".
	if isAuthFailure(lower) {
		return "auth"
	}
	if isQuotaFailure(lower) {
		return "quota"
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

// statusRegex matches a 3-digit number that looks like it appears in a
// status-line context: at the start of the message, immediately after
// a colon, after "status"/"code", or after a phrase like "got" / "returned".
// This is intentionally MORE restrictive than the previous version,
// which matched any 3-digit number on a word boundary — that caught
// token counts like "output:419" embedded in error messages.
var statusRegex = regexp.MustCompile(`(?i)(?:^|status[ =:]|code[ =:]|status code |HTTP[/ ]|got |returned |response[ =:]|: )(\d{3})\b`)

// extractHTTPStatus pulls a 3-digit HTTP status out of an error message
// when one is clearly present (e.g. "prompt failed: 502 Bad Gateway").
// Returns 0 when no recognisable status is found.
//
// Callers MUST also gate on classifyError returning an HTTP-shaped
// class (http_4xx / http_5xx / rate_limit). Without that gate, this
// function will happily match a 3-digit number embedded in a JSON
// payload dump and produce a fictitious status — see runs/2026-05-15
// for an example where token-count 419 leaked into http_status.
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
		if n >= 400 && n <= 599 {
			return n
		}
	}
	return 0
}

// httpErrorClasses is the closed set of error classes for which an
// HTTP status code is meaningful. Used by the orchestrator to decide
// whether to attach a status to the failure report.
var httpErrorClasses = map[string]struct{}{
	"http_4xx":   {},
	"http_5xx":   {},
	"rate_limit": {},
}

// shouldReportHTTPStatus reports whether the given error class is one
// for which a numeric HTTP status is meaningful.
func shouldReportHTTPStatus(class string) bool {
	_, ok := httpErrorClasses[class]
	return ok
}

// authPatterns is the closed catalogue of substrings that signal an
// authentication failure regardless of how the provider wrapped it.
// Each one is a phrase we have observed in real OpenCode/provider
// error envelopes; we will extend this list as new patterns appear.
//
// All patterns are matched case-insensitively against the lowercased
// error message in classifyError. They should be specific enough not
// to collide with unrelated text — generic words like "auth" by
// themselves are too noisy and were left out on purpose.
var authPatterns = []string{
	"invalidated oauth token",
	"oauth token",
	"invalid api key",
	"invalid_api_key",
	"unauthorized", // not "401" alone — we have http_4xx for that
	"forbidden",
	"authentication failed",
	"authentication error",
	"authentication required",
	"could not authenticate",
	"missing api key",
	"api key not found",
	"token has expired",
	"expired token",
	"invalid bearer token",
	"signature verification failed",
}

// quotaPatterns is the closed catalogue of substrings that signal a
// credit / quota / billing exhaustion. Distinct from rate_limit:
// rate_limit means "slow down", quota means "you have run out".
var quotaPatterns = []string{
	"insufficient_quota",
	"insufficient quota",
	"exceeded your current quota",
	"out of credit",
	"insufficient credit",
	"billing",
	"payment required",
	"hard limit reached",
	"you've reached your usage limit",
	"reached your monthly limit",
	"reached your limit",
	"quota exceeded",
}

// isAuthFailure reports whether `lower` (already lowercased) contains
// any auth-failure signature.
func isAuthFailure(lower string) bool {
	for _, p := range authPatterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// isQuotaFailure reports whether `lower` (already lowercased)
// contains any quota / credit-exhaustion signature.
func isQuotaFailure(lower string) bool {
	for _, p := range quotaPatterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}
