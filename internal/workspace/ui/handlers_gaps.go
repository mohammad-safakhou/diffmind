package ui

import (
	"net/http"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/store"
)

func (s *Server) handleListGaps(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListGaps(r.PathValue("pid"))
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleCreateGap(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Revision int       `json:"revision"`
		Gap      store.Gap `json:"gap"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if request.Gap.Author == "" {
		request.Gap.Author = identityFromContext(r.Context()).User
	}
	collection, gap, err := s.store.CreateGap(r.PathValue("pid"), request.Revision, request.Gap)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"revision": collection.Revision, "gap": gap})
}

func (s *Server) handleTransitionGap(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Revision int      `json:"revision"`
		Status   string   `json:"status"`
		Reason   string   `json:"reason,omitempty"`
		Tests    []string `json:"tests,omitempty"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	actor := identityFromContext(r.Context()).User
	collection, gap, err := s.store.TransitionGap(r.PathValue("pid"), r.PathValue("gid"), request.Revision, request.Status, actor, request.Reason, request.Tests)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revision": collection.Revision, "gap": gap})
}
