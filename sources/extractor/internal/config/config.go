package config

import (
	"encoding/json"
	"os"
)

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
	// SkipInfrastructure skips the Stage-0c infrastructure LLM call. That
	// stage's output (state/infrastructure.json) is consumed only by the UI,
	// not by core extraction, so skipping it removes a full LLM call per run on
	// repos with config files when the UI inventory isn't needed (X6).
	SkipInfrastructure bool `json:"skip_infrastructure"`

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

	// DiscoveryVerify enables the optional Stage-1.5 discovery verification
	// pass. It is gated to the HighVariance (LLM-only) objectives so cost
	// stays bounded, and is fail-soft + KEEP-biased: a verify error keeps the
	// un-verified items, and a doubted item is downgraded+tagged
	// ("discovery_verify_doubted") rather than dropped (only a structurally
	// unverifiable, unconfirmed item is dropped). Default false so the first
	// eval A/B captures a clean discovery baseline before this is enabled.
	DiscoveryVerify bool `json:"discovery_verify"`

	// DiscoveryVerifyMode selects the verification strategy when
	// DiscoveryVerify is on:
	//   - "reask":   re-open the discovered items' locations to confirm/correct,
	//                and search for any MISSED items of the same type, in ONE
	//                extra call per HighVariance objective.
	//   - "ksample": run the objective's discovery K times (DiscoveryVerifySamples)
	//                and UNION the results — never intersect, so a sample can only
	//                ADD recall. Targets run-to-run recall wobble.
	// Unknown values are coerced to "reask" by Sanitize. Default "reask".
	DiscoveryVerifyMode string `json:"discovery_verify_mode"`

	// DiscoveryVerifySamples is K for the ksample verify mode (the first sample
	// is the normal discovery run; K-1 extra whole-repo samples are unioned in).
	// Floored to [1,5] in Sanitize. Default 2.
	DiscoveryVerifySamples int `json:"discovery_verify_samples"`

	// DiscoveryFrameworkScope drops framework-labelled discovery-prompt bullets
	// for frameworks the repo clearly does not use (from repo_facts). This is
	// the riskiest prompt trim — a wrong drop blinds the model to a real
	// construct — so it defaults false; validate with floor-coverage before
	// enabling. Language-based scoping (ScopeFrameworkPatterns) is always on and
	// independent of this knob.
	DiscoveryFrameworkScope bool `json:"discovery_framework_scope"`
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
			IdleTimeoutSec:    120,
			MaxCallSeconds:    30 * 60,
			LivenessPollSec:   5,
			DiscoveryASTHints: true,
			// Discovery verification ships OFF by default (see field docs);
			// the mode/samples are sensible values used only once it is enabled.
			DiscoveryVerify:         false,
			DiscoveryVerifyMode:     "reask",
			DiscoveryVerifySamples:  2,
			DiscoveryFrameworkScope: false,
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

	// Bound the verification sample count to [1,5]. Below 1 a "sample" is
	// meaningless (the first run is always the seed sample); above 5 is pure
	// cost with diminishing recall. Defensive against a stale/hand-written
	// config so the verify pass can never multiply LLM calls without bound.
	if c.Runtime.DiscoveryVerifySamples < 1 {
		fixes = append(fixes, SanitizationFix{
			Field: "runtime.discovery_verify_samples",
			Was:   c.Runtime.DiscoveryVerifySamples, Adjusted: def.Runtime.DiscoveryVerifySamples,
			Reason: "value was < 1; reset to default (2)",
		})
		c.Runtime.DiscoveryVerifySamples = def.Runtime.DiscoveryVerifySamples
	} else if c.Runtime.DiscoveryVerifySamples > 5 {
		fixes = append(fixes, SanitizationFix{
			Field: "runtime.discovery_verify_samples",
			Was:   c.Runtime.DiscoveryVerifySamples, Adjusted: 5,
			Reason: "value exceeded the safe ceiling; capped at 5",
		})
		c.Runtime.DiscoveryVerifySamples = 5
	}
	// Coerce an unknown verify mode to the safe default. SanitizationFix is
	// int-only, so this correction is applied silently (the effective mode is
	// logged with the rest of the run config).
	switch c.Runtime.DiscoveryVerifyMode {
	case "reask", "ksample":
	default:
		c.Runtime.DiscoveryVerifyMode = def.Runtime.DiscoveryVerifyMode
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
