package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Server serves the DiffMind graph visualization dashboard.
type Server struct {
	baseDir string
	host    string
	port    int
}

// New creates a new UI server.
func New(baseDir, host string, port int) *Server {
	if strings.TrimSpace(baseDir) == "" {
		baseDir = ".diffmind/runs"
	}
	if strings.TrimSpace(host) == "" {
		host = "127.0.0.1"
	}
	if port <= 0 {
		port = 8090
	}
	return &Server{baseDir: baseDir, host: host, port: port}
}

// Addr returns the listen address.
func (s *Server) Addr() string {
	return fmt.Sprintf("%s:%d", s.host, s.port)
}

// Start begins serving. Blocks until ctx is cancelled.
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/runs", s.handleRuns)
	mux.HandleFunc("/api/run/", s.handleRun)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	srv := &http.Server{Addr: s.Addr(), Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	errCh := make(chan error, 1)
	go func() {
		fmt.Printf("DiffMind dashboard: http://%s\n", s.Addr())
		err := srv.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return nil
	case err := <-errCh:
		return err
	}
}

func (s *Server) handleIndex(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(indexHTML))
}

func (s *Server) handleRuns(w http.ResponseWriter, _ *http.Request) {
	runs, err := s.listRuns()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, map[string]any{"runs": runs})
}

func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	runID := strings.TrimPrefix(r.URL.Path, "/api/run/")
	runID = strings.TrimSpace(runID)
	if runID == "" || runID == "latest" {
		var err error
		runID, err = s.latestRunID()
		if err != nil {
			writeErr(w, http.StatusNotFound, err)
			return
		}
	}
	data, err := s.loadRun(runID)
	if err != nil {
		status := http.StatusInternalServerError
		if os.IsNotExist(err) {
			status = http.StatusNotFound
		}
		writeErr(w, status, err)
		return
	}
	writeJSON(w, data)
}

func (s *Server) listRuns() ([]string, error) {
	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	runs := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			runs = append(runs, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(runs)))
	return runs, nil
}

func (s *Server) latestRunID() (string, error) {
	runs, err := s.listRuns()
	if err != nil {
		return "", err
	}
	if len(runs) == 0 {
		return "", fmt.Errorf("no runs found in %s", s.baseDir)
	}
	return runs[0], nil
}

func (s *Server) loadRun(runID string) (map[string]any, error) {
	runDir := filepath.Join(s.baseDir, runID)

	// Read graph.json
	graphPath := filepath.Join(runDir, "graph.json")
	graphBytes, err := os.ReadFile(graphPath)
	if err != nil {
		return nil, err
	}
	var graph map[string]any
	if err := json.Unmarshal(graphBytes, &graph); err != nil {
		return nil, fmt.Errorf("parse graph.json: %w", err)
	}

	// Read manifest.json
	manifestPath := filepath.Join(runDir, "manifest.json")
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, err
	}
	var manifest map[string]any
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return nil, fmt.Errorf("parse manifest.json: %w", err)
	}

	return map[string]any{
		"run_id":   runID,
		"manifest": manifest,
		"graph":    graph,
	}, nil
}

func writeErr(w http.ResponseWriter, code int, err error) {
	w.WriteHeader(code)
	writeJSON(w, map[string]any{"error": err.Error()})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
