package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type DeterministicDiscoverySetting string

const (
	DeterministicDiscoveryOff           = DeterministicDiscoverySetting("off")
	DeterministicDiscoveryObserve       = DeterministicDiscoverySetting("observe")
	DeterministicDiscoveryShadowCompare = DeterministicDiscoverySetting("shadow_compare")
	DeterministicDiscoveryActive        = DeterministicDiscoverySetting("active")
)

func (d *DeterministicDiscoverySetting) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*d = DeterministicDiscoverySetting(s)
		return nil
	}
	var enabled bool
	if err := json.Unmarshal(b, &enabled); err == nil {
		if enabled {
			*d = DeterministicDiscoveryActive
		} else {
			*d = DeterministicDiscoveryOff
		}
		return nil
	}
	return fmt.Errorf("deterministic_discovery must be a string mode or boolean")
}

type OpenCode struct {
	BaseURL      string `json:"base_url"`
	ProviderID   string `json:"provider_id"`
	ModelID      string `json:"model_id"`
	ModelVariant string `json:"model_variant"`
	TimeoutSec   int    `json:"timeout_seconds"`
	Username     string `json:"username"`
	Password     string `json:"password"`
}

type Quality struct {
	MinConfidence float64 `json:"min_confidence"`
}

type Runtime struct {
	Workers                 int  `json:"workers"`
	MaxCatalogItems         int  `json:"max_catalog_items"`
	CleanupOpenCodeSessions bool `json:"cleanup_opencode_sessions"`
	OpenCodeDeleteDelaySec  int  `json:"opencode_delete_delay_seconds"`
	ReuseOpenCodeSession    bool `json:"reuse_opencode_session"`
	SkipReexamination       bool `json:"skip_reexamination"`

	// PromptRetryCount is how many times DiffMind retries a prompt after
	// the liveness watchdog declares it stuck. The initial attempt is not
	// counted; default 3 means up to 4 total attempts. Set to 0 to fail
	// immediately after the first stuck verdict.
	PromptRetryCount int `json:"prompt_retry_count"`

	// IdleTimeoutSec is the maximum time (in seconds) the liveness
	// watchdog will tolerate WITHOUT observable progress on the
	// OpenCode session before declaring the in-flight prompt stuck and
	// aborting it. Progress = parts growing, tool transitioning state,
	// or session token counters advancing. While a permission is pending
	// for the session, the idle clock is paused because DiffMind's
	// permission watchdog is expected to resolve it.
	IdleTimeoutSec int `json:"idle_timeout_seconds"`
	// MaxCallSeconds is a hard ceiling: even with continuous progress,
	// no single LLM call may exceed this duration. Acts as a safety
	// belt for runaway loops; under normal conditions IdleTimeoutSec
	// is the one doing the work.
	MaxCallSeconds int `json:"max_call_seconds"`
	// LivenessPollSec is how often the watchdog polls
	// /session/{id}/message?limit=1 to check for progress. Defaults
	// to 5 seconds; smaller values catch hangs faster at the cost of
	// more HTTP traffic to localhost (the JSON payload is ~4 KB).
	LivenessPollSec int `json:"liveness_poll_seconds"`

	// DiscoveryASTHints controls whether the discovery/reexamine/detail
	// prompts are augmented with deterministic AST candidate hints
	// (symbols, framework bindings, datasource config). The hints are
	// advisory only — the LLM is never constrained to them — but they
	// raise recall on the mechanical majority. Default true; set false
	// to A/B the anchoring-bias hypothesis (prompts then match the
	// pre-grounding behaviour byte-for-byte).
	DiscoveryASTHints bool `json:"discovery_ast_hints"`

	// DeterministicDiscovery controls whether exact framework/config facts
	// are only observed, shadow-compared against LLM discovery, or promoted
	// into the normal discovery pipeline. Allowed values: off, observe,
	// shadow_compare, active. Default observe.
	DeterministicDiscovery DeterministicDiscoverySetting `json:"deterministic_discovery"`
}

type Artifacts struct {
	BaseDir string `json:"base_dir"`
}

// Indexer holds AST analysis configuration. The tree-sitter engine
// auto-detects languages from file extensions; Languages can be set
// to give a primary-language hint for repos where detection is ambiguous.
type Indexer struct {
	// Languages provides a primary-language hint for the AST engine.
	// Empty (the default) means auto-detect from the source tree.
	Languages []string `json:"languages"`
}

type Config struct {
	OpenCode  OpenCode  `json:"opencode"`
	Quality   Quality   `json:"quality"`
	Runtime   Runtime   `json:"runtime"`
	Artifacts Artifacts `json:"artifacts"`
	Indexer   Indexer   `json:"indexer"`
}

func Default() Config {
	return Config{
		// TimeoutSec is now a fail-safe ceiling, not the primary
		// liveness control. The intelligent liveness watchdog
		// (IdleTimeoutSec) is what stops stuck calls; this is just
		// here so a totally broken connection eventually surfaces.
		// 4 hours is plenty for any single LLM call.
		OpenCode: OpenCode{TimeoutSec: 4 * 60 * 60},
		Quality: Quality{
			MinConfidence: 0.70,
		},
		Runtime: Runtime{
			// 6 workers is the right default for a single-user dashboard:
			// - It's enough to fan out the 13 discovery objectives in 2-3
			//   waves rather than serially.
			// - It keeps the OpenCode server's CPU manageable. With 16
			//   workers we saw heavy resource use because each session
			//   spawns ripgrep / LSP / file globbers in parallel.
			// - Provider rate limits are also less likely to trip.
			// Power users can still bump it via --workers / the form.
			Workers:                 6,
			MaxCatalogItems:         80,
			CleanupOpenCodeSessions: false,
			OpenCodeDeleteDelaySec:  5,
			ReuseOpenCodeSession:    false,
			PromptRetryCount:        3,
			// Liveness watchdog defaults. See the field docs on Runtime.
			IdleTimeoutSec:         120,
			MaxCallSeconds:         30 * 60,
			LivenessPollSec:        5,
			DiscoveryASTHints:      true,
			DeterministicDiscovery: DeterministicDiscoveryObserve,
		},
		// Artifacts default to the central ~/.diffmind/runs directory so runs
		// are discoverable independent of the scanned repository. Override
		// with `diffmind run --out` or artifacts.base_dir in a config file.
		Artifacts: Artifacts{BaseDir: RunsDir()},
		Indexer: Indexer{
			Languages: nil, // auto-detect from source tree
		},
	}
}

func Load(path string) (Config, error) {
	cfg := Default()
	if path == "" {
		return cfg, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// DeterministicDiscoveryMode returns the normalized deterministic-discovery
// rollout mode. Unknown values degrade to observe so a typo cannot silently
// promote deterministic output into final artifacts.
func (r Runtime) DeterministicDiscoveryMode() string {
	switch DeterministicDiscoverySetting(strings.TrimSpace(strings.ToLower(string(r.DeterministicDiscovery)))) {
	case DeterministicDiscoveryOff:
		return string(DeterministicDiscoveryOff)
	case DeterministicDiscoveryShadowCompare:
		return string(DeterministicDiscoveryShadowCompare)
	case DeterministicDiscoveryActive:
		return string(DeterministicDiscoveryActive)
	case DeterministicDiscoveryObserve, "":
		return string(DeterministicDiscoveryObserve)
	default:
		return string(DeterministicDiscoveryObserve)
	}
}

// Sanitization is the last line of defence against a stale or
// malformed config crippling a run. It enforces an invariant: the
// raw HTTP transport timeout MUST exceed the orchestrator-level
// MaxCallSeconds hard ceiling, otherwise the transport will fire
// first and we end up back at the 300-second wall that the liveness
// watchdog was supposed to make impossible.
//
// We have seen this failure mode in production runs more than once
// (20260518T113418Z, 20260518T115925Z). Every time it traced back to
// either (a) the SPA's localStorage holding a stale 300 from a
// previous version of the form, or (b) a CLI/JSON config someone
// hand-wrote without thinking about the relationship between the
// transport timeout and the watchdog's ceiling. Treating those as
// "user knows best" was wrong: the user has no way to know that
// setting `timeout_seconds: 300` silently neuters the watchdog.
//
// Sanitize is idempotent. It mutates the receiver and returns the
// list of (field, reason) corrections that were applied so callers
// can log them.

// SanitizationFix records a single defensive correction made by
// Sanitize. It lives in the manifest's metadata and the run_started
// event so misconfigurations are visible without diff'ing files.
type SanitizationFix struct {
	Field    string `json:"field"`
	Was      int    `json:"was"`
	Adjusted int    `json:"adjusted"`
	Reason   string `json:"reason"`
}

// Sanitize enforces invariants between the timeout fields and bumps
// nonsensical values up to safe minimums. It returns the list of
// fixes it applied so the caller can log/surface them.
func (c *Config) Sanitize() []SanitizationFix {
	var fixes []SanitizationFix

	def := Default()

	// Floor liveness fields at 1 second so a zero stored value (from
	// pre-watchdog configs) doesn't shut the watchdog off entirely.
	if c.Runtime.IdleTimeoutSec <= 0 {
		fixes = append(fixes, SanitizationFix{
			Field: "runtime.idle_timeout_seconds",
			Was:   c.Runtime.IdleTimeoutSec, Adjusted: def.Runtime.IdleTimeoutSec,
			Reason: "value was <= 0; reset to default (120s)",
		})
		c.Runtime.IdleTimeoutSec = def.Runtime.IdleTimeoutSec
	}
	if c.Runtime.MaxCallSeconds <= 0 {
		fixes = append(fixes, SanitizationFix{
			Field: "runtime.max_call_seconds",
			Was:   c.Runtime.MaxCallSeconds, Adjusted: def.Runtime.MaxCallSeconds,
			Reason: "value was <= 0; reset to default (1800s)",
		})
		c.Runtime.MaxCallSeconds = def.Runtime.MaxCallSeconds
	}
	if c.Runtime.LivenessPollSec <= 0 {
		fixes = append(fixes, SanitizationFix{
			Field: "runtime.liveness_poll_seconds",
			Was:   c.Runtime.LivenessPollSec, Adjusted: def.Runtime.LivenessPollSec,
			Reason: "value was <= 0; reset to default (5s)",
		})
		c.Runtime.LivenessPollSec = def.Runtime.LivenessPollSec
	}
	if c.Runtime.PromptRetryCount < 0 {
		fixes = append(fixes, SanitizationFix{
			Field: "runtime.prompt_retry_count",
			Was:   c.Runtime.PromptRetryCount, Adjusted: def.Runtime.PromptRetryCount,
			Reason: "value was < 0; reset to default (3)",
		})
		c.Runtime.PromptRetryCount = def.Runtime.PromptRetryCount
	}

	// The invariant: transport timeout must exceed MaxCallSeconds plus
	// a small headroom (so a per-call timeout caused by genuine
	// network failure surfaces AFTER MaxCallSeconds, not before). The
	// headroom is one watchdog poll cycle so the watchdog has a
	// guaranteed chance to fire its hard-ceiling abort first.
	headroom := c.Runtime.LivenessPollSec
	if headroom < 5 {
		headroom = 5
	}
	minTransport := c.Runtime.MaxCallSeconds + headroom
	if c.OpenCode.TimeoutSec < minTransport {
		fixes = append(fixes, SanitizationFix{
			Field: "opencode.timeout_seconds",
			Was:   c.OpenCode.TimeoutSec, Adjusted: minTransport,
			Reason: "transport timeout was lower than max_call_seconds + poll headroom; raised to keep the liveness watchdog in charge of stuck-call detection",
		})
		c.OpenCode.TimeoutSec = minTransport
	}

	return fixes
}
