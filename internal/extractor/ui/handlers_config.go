package ui

import (
	"net/http"

	"github.com/mohammad-safakhou/diffmind/internal/extractor/config"
)

// handleConfig serves GET /api/config: the defaults used to prefill the New Run
// form. Values come from ~/.diffmind/config.json (or built-in defaults when it
// is absent).
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
		"runtime": map[string]any{
			"pipeline": cfg.Pipeline(),
			"workers":  cfg.Runtime.Workers,
		},
		"quality": map[string]any{
			"min_confidence": cfg.Quality.MinConfidence,
		},
	})
}
