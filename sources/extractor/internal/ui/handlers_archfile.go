package ui

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/archfile"
	"github.com/mohammad-safakhou/diffmind/internal/catalog"
	"github.com/mohammad-safakhou/diffmind/internal/runner"
)

// generatedFileName is the transient proposal file written next to the
// human-authored discovery file. It is consumed by merge-file.
const generatedFileName = ".diffmind.generated.yaml"

// fileRequest is the shared body for the discovery-file endpoints: an absolute
// path to the repo's diffmind.yaml.
type fileRequest struct {
	Path string `json:"path"`
}

func (req fileRequest) clean() (string, error) {
	p := strings.TrimSpace(req.Path)
	if p == "" {
		return "", errors.New("path is required")
	}
	if !filepath.IsAbs(p) {
		return "", errors.New("path must be absolute")
	}
	return filepath.Clean(p), nil
}

// handleArchitectureImportFile imports a human-authored discovery file as a
// manual source: its facts land as manual-owned and are protected from later
// automation runs. Identity matches run import, so a file fact and the same run
// fact collapse to one durable record.
func (s *Server) handleArchitectureImportFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req fileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	path, err := req.clean()
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	resolved, err := archfile.Resolve(path)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	input, err := archfile.ToModel(resolved, "file:"+filepath.Base(path))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	doc, summary, err := s.catalog.ImportManual(input)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, map[string]any{"document": doc, "summary": summary})
}

// handleArchitectureExportFile writes the automation-owned records not yet in
// the main file as a transient proposal next to it (.diffmind.generated.yaml).
func (s *Server) handleArchitectureExportFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req fileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	path, err := req.clean()
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	doc, err := s.catalog.Load()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	genPath := filepath.Join(filepath.Dir(path), generatedFileName)
	n, err := archfile.WriteGenerated(doc, path, genPath)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, map[string]any{"written": n, "generated_path": genPath})
}

// handleArchitectureProposePreview computes (without writing) the automation
// facts that would be proposed into the generated file, so the UI can show a
// diff before anything touches disk.
func (s *Server) handleArchitectureProposePreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req fileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	path, err := req.clean()
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	doc, err := s.catalog.Load()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	proposal, err := archfile.ProposalPreview(doc, path)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, map[string]any{"records": proposal, "count": proposal.Count()})
}

// handleArchitectureMergePreview computes (without applying) which entries from
// the generated proposal would be appended to the main file versus skipped.
func (s *Server) handleArchitectureMergePreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req fileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	path, err := req.clean()
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	genPath := filepath.Join(filepath.Dir(path), generatedFileName)
	plan, err := archfile.MergePreview(path, genPath)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, plan)
}

// handleArchitectureFileContent reads (GET) or writes (PUT) the raw discovery
// file content for the inline editor.
func (s *Server) handleArchitectureFileContent(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		path := strings.TrimSpace(r.URL.Query().Get("path"))
		if path == "" || !filepath.IsAbs(path) {
			writeErr(w, http.StatusBadRequest, errors.New("path must be absolute"))
			return
		}
		b, err := os.ReadFile(filepath.Clean(path))
		if errors.Is(err, os.ErrNotExist) {
			writeJSON(w, map[string]any{"exists": false, "content": "", "valid": false})
			return
		}
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		out := map[string]any{"exists": true, "content": string(b), "sha256": sha256Hex(b), "valid": true}
		if resolved, err := archfile.Resolve(filepath.Clean(path)); err != nil {
			out["valid"] = false
			out["error"] = err.Error()
		} else {
			out["service"] = resolved.Service
			out["counts"] = map[string]int{
				"exposures":    len(resolved.Exposures),
				"dependencies": len(resolved.Dependencies),
				"connections":  len(resolved.Connections),
			}
		}
		writeJSON(w, out)
	case http.MethodPut:
		var req struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		path := strings.TrimSpace(req.Path)
		if path == "" || !filepath.IsAbs(path) {
			writeErr(w, http.StatusBadRequest, errors.New("path must be absolute"))
			return
		}
		if err := archfile.Validate([]byte(req.Content)); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		if err := archfile.WriteRaw(filepath.Clean(path), []byte(req.Content)); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleArchitectureMergeFile folds the proposal file into the main file,
// preserving human content, and consumes the proposal.
func (s *Server) handleArchitectureMergeFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req fileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	path, err := req.clean()
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	genPath := filepath.Join(filepath.Dir(path), generatedFileName)
	n, err := archfile.MergeIntoMain(path, genPath)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, map[string]any{"merged": n})
}

// handleArchitectureFileGraph resolves a repository's diffmind.yaml and returns
// model records that the visual graph can render. A missing file is a valid empty
// graph; parse/identity errors are surfaced for the inline editor to fix.
func (s *Server) handleArchitectureFileGraph(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	if path == "" || !filepath.IsAbs(path) {
		writeErr(w, http.StatusBadRequest, errors.New("path must be absolute"))
		return
	}
	path = filepath.Clean(path)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		writeJSON(w, map[string]any{"exposures": []any{}, "dependencies": []any{}, "connections": []any{}})
		return
	} else if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	resolved, err := archfile.Resolve(path)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	graph, err := archfile.ToGraph(resolved, "file:"+filepath.Base(path))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, graph)
}

func (s *Server) handleArchitectureFileDraft(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Path    string           `json:"path"`
		BaseSHA string           `json:"base_sha"`
		Edits   archfile.EditSet `json:"edits"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	path, err := fileRequest{Path: req.Path}.clean()
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if req.BaseSHA != "" {
		current, err := os.ReadFile(path)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		if sha256Hex(current) != strings.TrimSpace(req.BaseSHA) {
			writeErr(w, http.StatusConflict, errors.New("file changed since it was loaded"))
			return
		}
	}
	draft, err := archfile.Draft(path, req.Edits)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, draft)
}

func (s *Server) handleArchitectureFileApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Path    string `json:"path"`
		BaseSHA string `json:"base_sha"`
		YAML    string `json:"yaml"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	path, err := fileRequest{Path: req.Path}.clean()
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	current, err := os.ReadFile(path)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(req.BaseSHA) == "" || sha256Hex(current) != strings.TrimSpace(req.BaseSHA) {
		writeErr(w, http.StatusConflict, errors.New("file changed since it was loaded"))
		return
	}
	if err := archfile.Validate([]byte(req.YAML)); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := archfile.WriteRaw(path, []byte(req.YAML)); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "sha256": sha256Hex([]byte(req.YAML))})
}

// handleArchitectureRunProposal builds a transient generated file directly from
// one completed run and returns the merge preview against the repository file.
func (s *Server) handleArchitectureRunProposal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Path  string `json:"path"`
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	path, err := fileRequest{Path: req.Path}.clean()
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	runID := strings.TrimSpace(req.RunID)
	if runID == "" || filepath.Base(runID) != runID || runID == "." {
		writeErr(w, http.StatusBadRequest, errors.New("invalid run_id"))
		return
	}
	if status := s.diskStatus(runID); status != runner.StatusCompleted {
		writeErr(w, http.StatusConflict, errors.New("only completed runs can generate a proposal"))
		return
	}
	input, err := catalog.LoadRun(filepath.Join(s.baseDir, runID), runID)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	doc := catalog.Document{
		SchemaVersion: catalog.SchemaVersion,
		Name:          "Run " + runID,
		Exposures:     input.Exposures,
		Dependencies:  input.Dependencies,
		Connections:   input.Connections,
	}
	genPath := filepath.Join(filepath.Dir(path), generatedFileName)
	if err := archfile.WriteProposalDoc(doc, genPath); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	plan, err := archfile.MergePreview(path, genPath)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, map[string]any{
		"generated_path": genPath,
		"append":         plan.Append,
		"skip":           plan.Skip,
	})
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
