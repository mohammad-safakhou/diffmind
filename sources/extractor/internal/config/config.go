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
}

type Artifacts struct {
	BaseDir string `json:"base_dir"`
}

// Indexer configures the diffmind-indexer container that produces the
// SCIP index consumed by the connections stage. Defaults are tuned for
// a developer machine with Docker installed; CI / air-gapped
// environments can override Image, PullPolicy, and NetworkMode.
type Indexer struct {
	// Image is the container image reference. Empty falls back to
	// internal/indexer.DefaultImage (baked at build time by CI to
	// point at the matching ghcr.io/anomalyco/diffmind-indexer tag).
	Image string `json:"image"`

	// PullPolicy controls when Docker pulls the image:
	// "always", "if-absent" (default), or "never".
	PullPolicy string `json:"pull_policy"`

	// AutoBuild controls automatic image building from the embedded
	// indexerbuild context. Values: "missing" (default), "always",
	// "never". See internal/indexer/build.go for semantics.
	//
	// First-time setup story: with AutoBuild="missing" and a default
	// Image tag of "diffmind-indexer:dev", a fresh checkout of
	// diffmind runs end-to-end without any manual `docker build` —
	// the index stage detects the missing image and builds it inline.
	// Cold build is ~20 min, all subsequent runs reuse the local image.
	AutoBuild string `json:"auto_build"`

	// NetworkMode is the Docker --network flag. Defaults to "bridge"
	// (Docker default). Use "host" to share the host network when
	// indexers need to reach internal artifact registries that the
	// container network can't see. Use "none" to force air-gapped
	// indexing (some indexers will produce reduced indexes in this
	// mode).
	NetworkMode string `json:"network_mode"`

	// Languages explicitly restricts which indexers run. Empty (the
	// default) means "auto-detect from the source tree". Useful when
	// a polyglot repo contains, say, vendored Python that we don't
	// want to spend time indexing.
	Languages []string `json:"languages"`

	// PerIndexerTimeoutSec bounds wall-clock time of every individual
	// indexer process inside the container. Defaults to 1800 (30
	// minutes) when zero — enough for medium services, generous
	// enough that a slow Maven cold-pull won't trigger a false-timeout.
	PerIndexerTimeoutSec int `json:"per_indexer_timeout_seconds"`

	// Parallel is how many indexers the wrapper runs concurrently
	// inside the container. Defaults to 4 when zero.
	Parallel int `json:"parallel"`

	// Disabled, when true, skips the index stage entirely. The
	// connections stage then degrades to the no-paths heuristic
	// matcher. Useful for offline / smoke-test runs where the user
	// wants the extractor to complete even without Docker.
	Disabled bool `json:"disabled"`

	// ExtraEnv is appended verbatim to the container's environment.
	ExtraEnv map[string]string `json:"extra_env"`

	// ExtraMounts maps host paths to container paths (Docker volume
	// syntax). Useful for sharing a host's ~/.m2 / ~/.gradle / ~/.npm
	// caches across runs.
	ExtraMounts map[string]string `json:"extra_mounts"`
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
			IdleTimeoutSec:  120,
			MaxCallSeconds:  30 * 60,
			LivenessPollSec: 5,
		},
		Artifacts: Artifacts{BaseDir: ".diffmind/runs"},
		Indexer: Indexer{
			// Empty Image causes indexer.NewDockerIndexer to fall back
			// to indexer.DefaultImage. CI rewrites that constant at
			// release time via -ldflags.
			PullPolicy:           "if-absent",
			AutoBuild:            "missing", // build from embedded context if image missing
			NetworkMode:          "",        // Docker default (bridge)
			Languages:            nil,       // auto-detect
			PerIndexerTimeoutSec: 1800,      // 30 min
			Parallel:             4,
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
