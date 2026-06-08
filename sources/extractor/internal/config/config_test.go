package config

import (
	"testing"
)

// REGRESSION: runs 20260518T113418Z and 20260518T115925Z both failed
// at exactly 300s because the SPA's localStorage carried over an old
// opencode.timeout_seconds=300 that silently fired before the
// watchdog's 1800s hard ceiling could kick in. Sanitize() is the
// defensive belt-and-braces: even if a stale config arrives with a
// nonsensical small transport timeout, we raise it above the
// watchdog's MaxCallSeconds so the watchdog gets a chance to abort
// stuck calls instead of the transport always winning.
func TestSanitize_RaisesTransportTimeoutBelowMaxCall(t *testing.T) {
	c := Default()
	c.OpenCode.TimeoutSec = 300     // the bad value from history
	c.Runtime.MaxCallSeconds = 1800 // default watchdog ceiling
	c.Runtime.LivenessPollSec = 5

	fixes := c.Sanitize()
	if len(fixes) == 0 {
		t.Fatalf("Sanitize must report at least one fix when transport timeout < max_call")
	}
	if c.OpenCode.TimeoutSec < c.Runtime.MaxCallSeconds {
		t.Fatalf("TimeoutSec=%d still below MaxCallSeconds=%d after Sanitize", c.OpenCode.TimeoutSec, c.Runtime.MaxCallSeconds)
	}
	// At least one fix should reference the transport timeout field.
	found := false
	for _, f := range fixes {
		if f.Field == "opencode.timeout_seconds" {
			found = true
			if f.Was != 300 {
				t.Errorf("fix.Was = %d, want 300", f.Was)
			}
			if f.Adjusted < 1800 {
				t.Errorf("fix.Adjusted = %d, want >= 1800", f.Adjusted)
			}
		}
	}
	if !found {
		t.Errorf("expected an opencode.timeout_seconds fix; got %+v", fixes)
	}
}

// Sanitize must NOT mutate a config that is already consistent. This
// is the canary for the production happy path: the default config
// should pass through unchanged.
func TestSanitize_IdempotentOnDefault(t *testing.T) {
	c := Default()
	fixes := c.Sanitize()
	if len(fixes) != 0 {
		t.Errorf("Sanitize must be a no-op on Default(); got fixes: %+v", fixes)
	}
	// Run it twice; second call must also produce no fixes.
	fixes = c.Sanitize()
	if len(fixes) != 0 {
		t.Errorf("Sanitize must be idempotent; got fixes on second call: %+v", fixes)
	}
}

// Zero values for liveness fields must be reset to defaults — a
// totally absent liveness config (e.g. an old saved config from
// before the watchdog existed) must not disable the watchdog.
func TestSanitize_ZeroLivenessFieldsResetToDefaults(t *testing.T) {
	c := Default()
	c.Runtime.IdleTimeoutSec = 0
	c.Runtime.MaxCallSeconds = 0
	c.Runtime.LivenessPollSec = 0
	def := Default()

	fixes := c.Sanitize()
	if c.Runtime.IdleTimeoutSec != def.Runtime.IdleTimeoutSec {
		t.Errorf("IdleTimeoutSec not reset: got %d", c.Runtime.IdleTimeoutSec)
	}
	if c.Runtime.MaxCallSeconds != def.Runtime.MaxCallSeconds {
		t.Errorf("MaxCallSeconds not reset: got %d", c.Runtime.MaxCallSeconds)
	}
	if c.Runtime.LivenessPollSec != def.Runtime.LivenessPollSec {
		t.Errorf("LivenessPollSec not reset: got %d", c.Runtime.LivenessPollSec)
	}
	if c.Runtime.PromptRetryCount != def.Runtime.PromptRetryCount {
		t.Errorf("PromptRetryCount changed: got %d", c.Runtime.PromptRetryCount)
	}
	// We expect 3 zero-value fixes plus a transport timeout fix (since
	// MaxCall went from 0 → 1800 and the transport stays at default,
	// which may or may not need bumping depending on order). Just
	// assert that at least 3 fixes were reported.
	if len(fixes) < 3 {
		t.Errorf("expected at least 3 fixes for the three zero liveness fields; got %d: %+v", len(fixes), fixes)
	}
}

func TestSanitize_NegativeRetryCountResetsToDefault(t *testing.T) {
	c := Default()
	c.Runtime.PromptRetryCount = -1
	fixes := c.Sanitize()
	if c.Runtime.PromptRetryCount != Default().Runtime.PromptRetryCount {
		t.Fatalf("PromptRetryCount = %d, want default %d", c.Runtime.PromptRetryCount, Default().Runtime.PromptRetryCount)
	}
	found := false
	for _, f := range fixes {
		if f.Field == "runtime.prompt_retry_count" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected runtime.prompt_retry_count fix; got %+v", fixes)
	}
}

func TestSanitize_AllowsZeroRetryCount(t *testing.T) {
	c := Default()
	c.Runtime.PromptRetryCount = 0
	fixes := c.Sanitize()
	if c.Runtime.PromptRetryCount != 0 {
		t.Fatalf("PromptRetryCount = %d, want 0", c.Runtime.PromptRetryCount)
	}
	for _, f := range fixes {
		if f.Field == "runtime.prompt_retry_count" {
			t.Fatalf("zero retry count should disable retries, not sanitize: %+v", fixes)
		}
	}
}

// User explicitly chose a transport timeout that's LARGER than
// MaxCall. That's fine — no sanitization needed.
func TestSanitize_RespectsExplicitlyLargerTransportTimeout(t *testing.T) {
	c := Default()
	c.OpenCode.TimeoutSec = 6 * 60 * 60 // 6h — user wants headroom
	fixes := c.Sanitize()
	for _, f := range fixes {
		if f.Field == "opencode.timeout_seconds" {
			t.Errorf("must not adjust user-supplied larger timeout; got fix %+v", f)
		}
	}
	if c.OpenCode.TimeoutSec != 6*60*60 {
		t.Errorf("user value clobbered: got %d", c.OpenCode.TimeoutSec)
	}
}

