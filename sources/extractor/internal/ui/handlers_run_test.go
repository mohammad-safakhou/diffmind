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
		{"Runtime.PromptRetryCount", got.Runtime.PromptRetryCount, want.Runtime.PromptRetryCount},
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
			"prompt_retry_count": 4,
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
	if got.Runtime.PromptRetryCount != 4 {
		t.Errorf("PromptRetryCount = %d, want 4", got.Runtime.PromptRetryCount)
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

func TestBuildConfigFromRequest_ZeroPromptRetryCountDisablesRetries(t *testing.T) {
	body := []byte(`{
        "opencode": {"base_url":"http://x"},
        "runtime": {"prompt_retry_count": 0}
    }`)
	var req startRunRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := buildConfigFromRequest(req)
	if got.Runtime.PromptRetryCount != 0 {
		t.Fatalf("PromptRetryCount = %d, want 0", got.Runtime.PromptRetryCount)
	}
}

// Indexer overrides from the dashboard form must propagate into the
// resulting config, and ZERO/empty fields must not clobber defaults.
// This is the equivalent of the OpenCode-timeout regression guard
// above but for the indexer block introduced in Sprint 3's auto-build.
func TestBuildConfigFromRequest_IndexerOverridesPropagate(t *testing.T) {
	t.Run("empty_keeps_defaults", func(t *testing.T) {
		var req startRunRequest
		got := buildConfigFromRequest(req)
		want := config.Default()
		if got.Indexer.Disabled != want.Indexer.Disabled {
			t.Errorf("Disabled changed: got %v, want %v", got.Indexer.Disabled, want.Indexer.Disabled)
		}
		if got.Indexer.Image != want.Indexer.Image {
			t.Errorf("Image changed: got %q, want %q", got.Indexer.Image, want.Indexer.Image)
		}
		if got.Indexer.AutoBuild != want.Indexer.AutoBuild {
			t.Errorf("AutoBuild changed: got %q, want %q", got.Indexer.AutoBuild, want.Indexer.AutoBuild)
		}
	})

	t.Run("explicit_overrides", func(t *testing.T) {
		body := []byte(`{
            "opencode": {"base_url": "http://x"},
            "indexer": {
                "disabled": true,
                "image": "custom-registry/diffmind-indexer:v9",
                "auto_build": "always"
            }
        }`)
		var req startRunRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("decode: %v", err)
		}
		got := buildConfigFromRequest(req)
		if !got.Indexer.Disabled {
			t.Error("Disabled override lost")
		}
		if got.Indexer.Image != "custom-registry/diffmind-indexer:v9" {
			t.Errorf("Image = %q", got.Indexer.Image)
		}
		if got.Indexer.AutoBuild != "always" {
			t.Errorf("AutoBuild = %q", got.Indexer.AutoBuild)
		}
	})

	t.Run("whitespace_only_image_ignored", func(t *testing.T) {
		// The SPA may send "   " from an empty input box; we should
		// treat that as "use the default" rather than overwriting
		// the default with an empty/whitespace string.
		body := []byte(`{
            "opencode": {"base_url": "http://x"},
            "indexer": {"image": "   ", "auto_build": ""}
        }`)
		var req startRunRequest
		_ = json.Unmarshal(body, &req)
		got := buildConfigFromRequest(req)
		want := config.Default()
		if got.Indexer.Image != want.Indexer.Image {
			t.Errorf("whitespace image clobbered default: got %q, want %q",
				got.Indexer.Image, want.Indexer.Image)
		}
		if got.Indexer.AutoBuild != want.Indexer.AutoBuild {
			t.Errorf("empty auto_build clobbered default: got %q, want %q",
				got.Indexer.AutoBuild, want.Indexer.AutoBuild)
		}
	})
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
