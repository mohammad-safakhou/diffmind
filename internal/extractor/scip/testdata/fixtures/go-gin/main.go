// Package main is a minimal HTTP server used as a fixture for the
// SCIP integration test. The structure deliberately mirrors a real
// Go web service: handler → service → repository → database access.
//
// The integration test (internal/scip/integration_test.go) runs
// scip-go against this fixture and verifies the walker can trace a
// path from the handler entry to the repository.Save call.
package main

import (
	"fmt"
	"net/http"
)

// CampaignRepository is the "database" layer. The repository test
// helper extracts SCIP symbols for methods declared here.
type CampaignRepository struct {
	store map[string]string
}

// FindByID is the read path. Called by CampaignService.GetByID.
func (r *CampaignRepository) FindByID(id string) (string, bool) {
	v, ok := r.store[id]
	return v, ok
}

// Save is the write path. Called by CampaignService.Create.
func (r *CampaignRepository) Save(id, value string) {
	if r.store == nil {
		r.store = map[string]string{}
	}
	r.store[id] = value
}

// CampaignService is the business-logic layer.
type CampaignService struct {
	repo *CampaignRepository
}

// GetByID fetches a campaign by id. Calls into the repository.
func (s *CampaignService) GetByID(id string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("empty id")
	}
	v, ok := s.repo.FindByID(id)
	if !ok {
		return "", fmt.Errorf("not found")
	}
	return v, nil
}

// Create persists a new campaign. Calls into the repository.
func (s *CampaignService) Create(id, value string) error {
	if id == "" || value == "" {
		return fmt.Errorf("invalid input")
	}
	s.repo.Save(id, value)
	return nil
}

// CampaignHandler is the HTTP layer. Each route is a thin wrapper
// around a service method.
type CampaignHandler struct {
	svc *CampaignService
}

// GetCampaign handles GET /campaigns/{id}. Calls GetByID on the
// service. Lives on line 71-79 in this file (handler entry).
func (h *CampaignHandler) GetCampaign(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	v, err := h.svc.GetByID(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	fmt.Fprint(w, v)
}

// CreateCampaign handles POST /campaigns. Calls Create on the
// service.
func (h *CampaignHandler) CreateCampaign(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	v := r.URL.Query().Get("value")
	if err := h.svc.Create(id, v); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func main() {
	repo := &CampaignRepository{}
	svc := &CampaignService{repo: repo}
	h := &CampaignHandler{svc: svc}
	http.HandleFunc("/campaigns/get", h.GetCampaign)
	http.HandleFunc("/campaigns/create", h.CreateCampaign)
	_ = http.ListenAndServe(":8080", nil)
}
