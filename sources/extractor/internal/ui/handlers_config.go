package ui

import (
	"net/http"

	"github.com/mohammad-safakhou/diffmind/internal/config"
)

// handleConfig serves GET /api/config: the defaults used to prefill the New Run
// form. Values come from ~/.diffmind/config.json (or built-in defaults when it
// is absent). The password is deliberately omitted — it is never round-tripped
// through the browser; users supply it per run (or via the OPENCODE_SERVER_*
// environment variables on the server side).
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cfg, err := config.LoadCentral("")
	if err != nil {
		// A malformed central config should not break the form — fall back
		// to defaults and let the user override.
		cfg = config.Default()
	}
	writeJSON(w, map[string]any{
		"base_dir":    s.baseDir,
		"config_path": config.FilePath(),
		"opencode": map[string]any{
			"base_url":        cfg.OpenCode.BaseURL,
			"username":        cfg.OpenCode.Username,
			"provider_id":     cfg.OpenCode.ProviderID,
			"model_id":        cfg.OpenCode.ModelID,
			"model_variant":   cfg.OpenCode.ModelVariant,
			"timeout_seconds": cfg.OpenCode.TimeoutSec,
		},
		"runtime": map[string]any{
			"workers":                   cfg.Runtime.Workers,
			"max_catalog_items":         cfg.Runtime.MaxCatalogItems,
			"idle_timeout_seconds":      cfg.Runtime.IdleTimeoutSec,
			"max_call_seconds":          cfg.Runtime.MaxCallSeconds,
			"liveness_poll_seconds":     cfg.Runtime.LivenessPollSec,
			"prompt_retry_count":        cfg.Runtime.PromptRetryCount,
			"skip_reexamination":        cfg.Runtime.SkipReexamination,
			"skip_detail":               cfg.Runtime.SkipDetail,
			"discovery_verify":          cfg.Runtime.DiscoveryVerify,
			"discovery_verify_mode":     cfg.Runtime.DiscoveryVerifyMode,
			"discovery_verify_samples":  cfg.Runtime.DiscoveryVerifySamples,
			"discovery_framework_scope": cfg.Runtime.DiscoveryFrameworkScope,
			"reuse_opencode_session":    cfg.Runtime.ReuseOpenCodeSession,
		},
		"quality": map[string]any{
			"min_confidence": cfg.Quality.MinConfidence,
		},
	})
}
