package ui

import (
	"io"
	"net/http"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/knowledge"
)

func (s *Server) handleListPacks(w http.ResponseWriter, r *http.Request) {
	bps, err := s.store.ListPacks(r.PathValue("pid"))
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"packs": bps})
}

func (s *Server) handleGetPack(w http.ResponseWriter, r *http.Request) {
	raw, err := s.store.GetPack(r.PathValue("pid"), r.PathValue("pack_id"))
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

// handleCreatePack validates and stores a new pack. The request body
// IS the pack JSON. On validation failure it returns 422 with the list of
// structured field errors so the editor can highlight them.
func (s *Server) handleCreatePack(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	bp, verrs := knowledge.ValidatePack(body, ".json")
	if len(verrs) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "pack validation failed", "validation": verrs})
		return
	}
	id, err := s.store.CreatePack(r.PathValue("pid"), bp.ID, body)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "name": bp.Name})
}

// handlePutPack validates and replaces an existing pack by id.
func (s *Server) handlePutPack(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	pack, verrs := knowledge.ValidatePack(body, ".json")
	if len(verrs) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "pack validation failed", "validation": verrs})
		return
	}
	if pack.ID != r.PathValue("pack_id") {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error":      "pack id is immutable",
			"validation": []knowledge.ValidationError{{Field: "id", Message: "must match " + r.PathValue("pack_id")}},
		})
		return
	}
	if err := s.store.PutPack(r.PathValue("pid"), r.PathValue("pack_id"), body); err != nil {
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": r.PathValue("pack_id")})
}

func (s *Server) handleDeletePack(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeletePack(r.PathValue("pid"), r.PathValue("pack_id")); err != nil {
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}
