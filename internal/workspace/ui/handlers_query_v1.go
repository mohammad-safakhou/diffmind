package ui

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/query"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/store"
)

func (s *Server) handleV1Projects(w http.ResponseWriter, _ *http.Request) {
	projects, err := s.query.Projects()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"api_version": "v1", "projects": projects})
}

func (s *Server) handleV1GraphSummary(w http.ResponseWriter, r *http.Request) {
	out, err := s.query.Summary(r.PathValue("pid"), r.URL.Query().Get("run"))
	writeV1Result(w, out, err)
}

func (s *Server) handleV1Services(w http.ResponseWriter, r *http.Request) {
	items, err := s.query.Services(r.PathValue("pid"), r.URL.Query().Get("run"))
	if err != nil {
		writeV1Result(w, nil, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"api_version": "v1", "project_id": r.PathValue("pid"), "services": items})
}

func (s *Server) handleV1Service(w http.ResponseWriter, r *http.Request) {
	service, err := url.PathUnescape(r.PathValue("service"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("invalid service path"))
		return
	}
	out, err := s.query.Service(r.PathValue("pid"), r.URL.Query().Get("run"), service)
	writeV1Result(w, out, err)
}

func (s *Server) handleV1Dependencies(w http.ResponseWriter, r *http.Request) {
	out, err := s.query.Dependencies(r.PathValue("pid"), r.URL.Query().Get("run"), r.URL.Query().Get("service"), r.URL.Query().Get("direction"))
	writeV1Result(w, out, err)
}

func (s *Server) handleV1Impact(w http.ResponseWriter, r *http.Request) {
	target := firstNonEmpty(r.URL.Query().Get("target"), r.URL.Query().Get("service"), r.URL.Query().Get("node"))
	out, err := s.query.Impact(r.PathValue("pid"), r.URL.Query().Get("run"), target, positiveInt(r.URL.Query().Get("depth"), 6))
	writeV1Result(w, out, err)
}

func (s *Server) handleV1Search(w http.ResponseWriter, r *http.Request) {
	out, err := s.query.Search(r.PathValue("pid"), r.URL.Query().Get("run"), r.URL.Query().Get("q"), positiveInt(r.URL.Query().Get("limit"), 50))
	writeV1Result(w, out, err)
}

func writeV1Result(w http.ResponseWriter, value any, err error) {
	if err == nil {
		writeJSON(w, http.StatusOK, value)
		return
	}
	status := http.StatusBadRequest
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, query.ErrServiceNotFound) {
		status = http.StatusNotFound
	}
	if errors.Is(err, query.ErrNoCompletedGraph) {
		status = http.StatusConflict
	}
	writeErr(w, status, err)
}

func positiveInt(raw string, fallback int) int {
	v, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || v <= 0 {
		return fallback
	}
	return v
}
