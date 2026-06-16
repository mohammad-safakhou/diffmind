package ui

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/catalog"
	"github.com/mohammad-safakhou/diffmind/internal/runner"
)

func (s *Server) handleArchitecture(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		doc, err := s.catalog.Load()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, doc)
	case http.MethodPut:
		var doc catalog.Document
		if err := json.NewDecoder(r.Body).Decode(&doc); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		saved, err := s.catalog.SaveManual(doc)
		switch {
		case errors.Is(err, catalog.ErrRevisionConflict):
			writeErr(w, http.StatusConflict, err)
		case err != nil:
			writeErr(w, http.StatusBadRequest, err)
		default:
			writeJSON(w, saved)
		}
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleArchitectureImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	req.RunID = strings.TrimSpace(req.RunID)
	if req.RunID == "" || filepath.Base(req.RunID) != req.RunID || req.RunID == "." {
		writeErr(w, http.StatusBadRequest, errors.New("invalid run_id"))
		return
	}
	if status := s.diskStatus(req.RunID); status != runner.StatusCompleted {
		writeErr(w, http.StatusConflict, errors.New("only completed runs can be imported"))
		return
	}
	input, err := catalog.LoadRun(filepath.Join(s.baseDir, req.RunID), req.RunID)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	doc, summary, err := s.catalog.Import(input)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, map[string]any{
		"document": doc,
		"summary":  summary,
	})
}
