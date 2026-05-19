package ui

import (
	"encoding/json"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/config"
)

// REGRESSION: run 20260518T113418Z showed that the SPA was silently
// sending opencode.timeout_seconds: 300, which clobbered the 4-hour
// transport-timeout fail-safe and re-introduced the 300s wall that
// the liveness watchdog was supposed to replace.
//
// The contract is: a numeric field that is ZERO (or missing) in the
// request MUST NOT overwrite the config default. The SPA defaults
// all liveness knobs to 0 and the user only fills in what they want
// to override.
func TestBuildConfigFromRequest_ZeroFieldsKeepDefaults(t *testing.T) {
	// Empty request: every numeric field is zero-valued. The result
	// MUST equal the production default in every field that matters
	// for liveness / transport timeouts.
	var req startRunRequest
	got := buildConfigFromRequest(req)
	want := config.Default()
	checks := []struct {
		name string
		got  any
		want any
	}{
		{"OpenCode.TimeoutSec", got.OpenCode.TimeoutSec, want.OpenCode.TimeoutSec},
		{"Runtime.IdleTimeoutSec", got.Runtime.IdleTimeoutSec, want.Runtime.IdleTimeoutSec},
		{"Runtime.MaxCallSeconds", got.Runtime.MaxCallSeconds, want.Runtime.MaxCallSeconds},
		{"Runtime.LivenessPollSec", got.Runtime.LivenessPollSec, want.Runtime.LivenessPollSec},
		{"Runtime.Workers", got.Runtime.Workers, want.Runtime.Workers},
		{"Runtime.MaxCatalogItems", got.Runtime.MaxCatalogItems, want.Runtime.MaxCatalogItems},
		{"Runtime.OpenCodeDeleteDelaySec", got.Runtime.OpenCodeDeleteDelaySec, want.Runtime.OpenCodeDeleteDelaySec},
		{"Quality.MinConfidence", got.Quality.MinConfidence, want.Quality.MinConfidence},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: empty request changed default: got %v, want %v", c.name, c.got, c.want)
		}
	}
}

// Positive overrides applied through the request must propagate
// 1:1 into the resulting config. This is the "user actually
// changed a knob in the form" path.
func TestBuildConfigFromRequest_PositiveValuesOverrideDefaults(t *testing.T) {
	body := []byte(`{
        "opencode": {"base_url":"http://x","timeout_seconds":600},
        "runtime": {
            "workers": 16,
            "max_catalog_items": 50,
            "idle_timeout_seconds": 240,
            "max_call_seconds": 3600,
            "liveness_poll_seconds": 10,
            "opencode_delete_delay_seconds": 20,
            "skip_reexamination": true,
            "reuse_opencode_session": true,
            "cleanup_opencode_sessions": true
        },
        "quality": {"min_confidence": 0.5}
    }`)
	var req startRunRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := buildConfigFromRequest(req)
	if got.OpenCode.TimeoutSec != 600 {
		t.Errorf("TimeoutSec = %d, want 600", got.OpenCode.TimeoutSec)
	}
	if got.Runtime.IdleTimeoutSec != 240 {
		t.Errorf("IdleTimeoutSec = %d, want 240", got.Runtime.IdleTimeoutSec)
	}
	if got.Runtime.MaxCallSeconds != 3600 {
		t.Errorf("MaxCallSeconds = %d, want 3600", got.Runtime.MaxCallSeconds)
	}
	if got.Runtime.LivenessPollSec != 10 {
		t.Errorf("LivenessPollSec = %d, want 10", got.Runtime.LivenessPollSec)
	}
	if got.Runtime.Workers != 16 {
		t.Errorf("Workers = %d, want 16", got.Runtime.Workers)
	}
	if got.Runtime.MaxCatalogItems != 50 {
		t.Errorf("MaxCatalogItems = %d, want 50", got.Runtime.MaxCatalogItems)
	}
	if !got.Runtime.ReuseOpenCodeSession {
		t.Errorf("ReuseOpenCodeSession not honoured")
	}
	if !got.Runtime.CleanupOpenCodeSessions {
		t.Errorf("CleanupOpenCodeSessions not honoured")
	}
	if !got.Runtime.SkipReexamination {
		t.Errorf("SkipReexamination not honoured")
	}
	if got.Quality.MinConfidence != 0.5 {
		t.Errorf("MinConfidence = %f, want 0.5", got.Quality.MinConfidence)
	}
}

// The exact failing payload from the cautionary run, distilled.
// timeout_seconds=300 MUST be honoured as a user-explicit override
// (the user might still need that for legacy reasons), but the rest
// of the defaults must stay intact.
func TestBuildConfigFromRequest_LegacyTimeoutHonouredButLivenessDefaultsIntact(t *testing.T) {
	body := []byte(`{
        "opencode": {"base_url":"http://x","timeout_seconds":300},
        "runtime": {"workers": 48}
    }`)
	var req startRunRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := buildConfigFromRequest(req)
	if got.OpenCode.TimeoutSec != 300 {
		t.Errorf("user-set timeout 300 must propagate; got %d", got.OpenCode.TimeoutSec)
	}
	want := config.Default()
	if got.Runtime.IdleTimeoutSec != want.Runtime.IdleTimeoutSec {
		t.Errorf("IdleTimeoutSec corrupted: got %d, want %d", got.Runtime.IdleTimeoutSec, want.Runtime.IdleTimeoutSec)
	}
	if got.Runtime.MaxCallSeconds != want.Runtime.MaxCallSeconds {
		t.Errorf("MaxCallSeconds corrupted: got %d, want %d", got.Runtime.MaxCallSeconds, want.Runtime.MaxCallSeconds)
	}
}
