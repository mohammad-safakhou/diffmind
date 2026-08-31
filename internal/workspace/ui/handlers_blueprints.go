package ui

import (
	"io"
	"net/http"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/blueprints"
)

func (s *Server) handleListBlueprints(w http.ResponseWriter, r *http.Request) {
	bps, err := s.store.ListBlueprints(r.PathValue("pid"))
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"blueprints": bps})
}

func (s *Server) handleGetBlueprint(w http.ResponseWriter, r *http.Request) {
	raw, err := s.store.GetBlueprint(r.PathValue("pid"), r.PathValue("bid"))
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

// handleCreateBlueprint validates and stores a new blueprint. The request body
// IS the blueprint JSON. On validation failure it returns 422 with the list of
// structured field errors so the editor can highlight them.
func (s *Server) handleCreateBlueprint(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	bp, verrs := blueprints.ValidateBlueprint(body)
	if len(verrs) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "blueprint validation failed", "validation": verrs})
		return
	}
	id, err := s.store.CreateBlueprint(r.PathValue("pid"), bp.Name, body)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "name": bp.Name})
}

// handlePutBlueprint validates and replaces an existing blueprint by id.
func (s *Server) handlePutBlueprint(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	_, verrs := blueprints.ValidateBlueprint(body)
	if len(verrs) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "blueprint validation failed", "validation": verrs})
		return
	}
	if err := s.store.PutBlueprint(r.PathValue("pid"), r.PathValue("bid"), body); err != nil {
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": r.PathValue("bid")})
}

func (s *Server) handleDeleteBlueprint(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteBlueprint(r.PathValue("pid"), r.PathValue("bid")); err != nil {
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}
