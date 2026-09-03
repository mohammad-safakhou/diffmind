package ui

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/store"
)

// Keep this check at the handler boundary as well as the route wrapper. Tokens
// must never be issued in legacy mode, where project restrictions are disabled.
func (s *Server) tokenAdmin(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	if identityFromContext(r.Context()).Role != RoleAdmin {
		writeErr(w, 403, errors.New("administrator required"))
		return false
	}
	return true
}
func (s *Server) handleListTokens(w http.ResponseWriter, r *http.Request) {
	if !s.tokenAdmin(w, r) {
		return
	}
	tokens, err := s.store.ListProjectTokens(r.PathValue("pid"))
	if err != nil {
		s.writeTokenError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"enabled": s.projectAccessScoped, "tokens": tokens})
}
func (s *Server) handleIssueToken(w http.ResponseWriter, r *http.Request) {
	if !s.tokenAdmin(w, r) {
		return
	}
	if !s.projectAccessScoped {
		writeErr(w, 409, errors.New("enable scoped project access before issuing tokens"))
		return
	}
	var req struct {
		Name             string `json:"name"`
		Role             string `json:"role"`
		ExpiresInSeconds int64  `json:"expires_in_seconds"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(&req); err != nil {
		writeErr(w, 400, errors.New("invalid token request"))
		return
	}
	var extra any
	if d.Decode(&extra) != io.EOF || req.ExpiresInSeconds < 60 || req.ExpiresInSeconds > 31536000 {
		writeErr(w, 400, errors.New("expires_in_seconds must be 60–31536000; no trailing data"))
		return
	}
	token, secret, err := s.store.IssueProjectToken(r.PathValue("pid"), req.Name, req.Role, identityFromContext(r.Context()).User, time.Duration(req.ExpiresInSeconds)*time.Second)
	if err != nil {
		s.writeTokenError(w, err)
		return
	}
	writeJSON(w, 201, struct {
		Token  *store.ProjectToken `json:"token"`
		Secret string              `json:"secret"`
	}{token, secret})
}
func (s *Server) handleRevokeToken(w http.ResponseWriter, r *http.Request) {
	if !s.tokenAdmin(w, r) {
		return
	}
	token, err := s.store.RevokeProjectToken(r.PathValue("pid"), r.PathValue("tid"), identityFromContext(r.Context()).User)
	if err != nil {
		s.writeTokenError(w, err)
		return
	}
	writeJSON(w, 200, token)
}
func (s *Server) writeTokenError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, 404, store.ErrNotFound)
	case errors.Is(err, store.ErrInvalidTokenRequest):
		writeErr(w, 400, store.ErrInvalidTokenRequest)
	case errors.Is(err, store.ErrTokenCapacity):
		writeErr(w, 409, store.ErrTokenCapacity)
	default:
		writeErr(w, 503, errors.New("token registry unavailable; check storage before retrying"))
	}
}
