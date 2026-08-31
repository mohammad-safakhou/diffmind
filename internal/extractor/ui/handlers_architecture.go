package ui

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/mohammad-safakhou/diffmind/internal/extractor/catalog"
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
