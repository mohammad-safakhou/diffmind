package ui

import (
	"errors"
	"net/http"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/store"
)

func (s *Server) handleListProjects(w http.ResponseWriter, _ *http.Request) {
	ps, err := s.store.ListProjects()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": ps})
}

type createProjectRequest struct {
	Name        string   `json:"name"`
	SearchRoots []string `json:"search_roots"`
	Instruction string   `json:"instruction"`
	// StarterBlueprints are blueprint bodies to seed the new project with
	// (the SPA copies these from diffmind/blueprints/*.json templates).
	StarterBlueprints []starterBlueprint `json:"starter_blueprints"`
}

type starterBlueprint struct {
	Name string `json:"name"`
	Body any    `json:"body"`
}

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var req createProjectRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeErr(w, http.StatusBadRequest, errors.New("name is required"))
		return
	}
	p, err := s.store.CreateProject(store.Project{
		Name:        req.Name,
		SearchRoots: req.SearchRoots,
		Instruction: req.Instruction,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// Seed starter blueprints (best effort; a bad one is skipped).
	for _, sb := range req.StarterBlueprints {
		body, err := marshalBody(sb.Body)
		if err != nil {
			continue
		}
		_, _ = s.store.CreateBlueprint(p.ID, sb.Name, body)
	}
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) handleGetProject(w http.ResponseWriter, r *http.Request) {
	p, err := s.store.GetProject(r.PathValue("pid"))
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

type patchProjectRequest struct {
	Name        *string   `json:"name"`
	SearchRoots *[]string `json:"search_roots"`
	Instruction *string   `json:"instruction"`
}

func (s *Server) handlePatchProject(w http.ResponseWriter, r *http.Request) {
	var req patchProjectRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	p, err := s.store.UpdateProject(r.PathValue("pid"), func(p *store.Project) {
		if req.Name != nil {
			p.Name = *req.Name
		}
		if req.SearchRoots != nil {
			p.SearchRoots = *req.SearchRoots
		}
		if req.Instruction != nil {
			p.Instruction = *req.Instruction
		}
	})
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteProject(r.PathValue("pid")); err != nil {
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

func (s *Server) writeStoreErr(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	writeErr(w, http.StatusInternalServerError, err)
}
