package ui

import (
	"fmt"
	"net/http"
	"strconv"
)

func queryInteger(r *http.Request, key string, fallback int) (int, error) {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", key)
	}
	return n, nil
}

func (s *Server) handleV1GraphRuns(w http.ResponseWriter, r *http.Request) {
	offset, err := queryInteger(r, "offset", 0)
	if err != nil {
		writeV1Result(w, nil, err)
		return
	}
	limit, err := queryInteger(r, "limit", 100)
	if err != nil {
		writeV1Result(w, nil, err)
		return
	}
	out, err := s.query.GraphRuns(r.PathValue("pid"), offset, limit)
	writeV1Result(w, out, err)
}

func (s *Server) handleV1GraphCompare(w http.ResponseWriter, r *http.Request) {
	offset, err := queryInteger(r, "offset", 0)
	if err != nil {
		writeV1Result(w, nil, err)
		return
	}
	limit, err := queryInteger(r, "limit", 100)
	if err != nil {
		writeV1Result(w, nil, err)
		return
	}
	out, err := s.query.CompareGraphs(r.Context(), r.PathValue("pid"), r.URL.Query().Get("from"), r.URL.Query().Get("to"), offset, limit)
	writeV1Result(w, out, err)
}

func (s *Server) handleV1GraphPath(w http.ResponseWriter, r *http.Request) {
	depth, err := queryInteger(r, "depth", 6)
	if err != nil {
		writeV1Result(w, nil, err)
		return
	}
	out, err := s.query.FindPath(r.Context(), r.PathValue("pid"), r.URL.Query().Get("run"), r.URL.Query().Get("from"), r.URL.Query().Get("to"), depth)
	writeV1Result(w, out, err)
}

func (s *Server) handleV1ObjectTrace(w http.ResponseWriter, r *http.Request) {
	out, err := s.query.TraceObject(r.Context(), r.PathValue("pid"), r.URL.Query().Get("run"), r.URL.Query().Get("service"), r.URL.Query().Get("object_id"))
	writeV1Result(w, out, err)
}
