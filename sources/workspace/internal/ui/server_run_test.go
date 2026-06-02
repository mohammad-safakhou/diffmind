package ui

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/runmgr"
	"github.com/mohammad-safakhou/diffmind/internal/store"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// copyDir recursively copies src into dst.
func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatalf("copy: %v", err)
	}
}

// TestStaticServesSPA verifies the embedded SPA bundle is served for / and for
// unknown deep-link routes (SPA fallback).
func TestStaticServesSPA(t *testing.T) {
	srv, _ := newTestServer(t)
	defer srv.Close()

	for _, path := range []string{"/", "/projects/foo"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("GET %s = %d", path, resp.StatusCode)
		}
		if !strings.Contains(string(body), "<div id=\"app\">") {
			t.Fatalf("GET %s did not return the SPA shell: %s", path, truncate(string(body)))
		}
	}
}

func truncate(s string) string {
	if len(s) > 200 {
		return s[:200]
	}
	return s
}

// TestGraphRunOverHTTP drives a full graph run through the HTTP API: create a
// project + repos, start a run, stream SSE to the terminal event, then fetch
// the graph and run state.
func TestGraphRunOverHTTP(t *testing.T) {
	wd, _ := os.Getwd()
	root := filepath.Join(wd, "..", "..")

	beRuns := t.TempDir()
	copyDir(t, filepath.Join(root, "testdata", "sample_diffmind_output", "order-service", ".diffmind", "runs", "run_001"), filepath.Join(beRuns, "order-run"))
	copyDir(t, filepath.Join(root, "testdata", "sample_diffmind_output", "billing-service", ".diffmind", "runs", "run_001"), filepath.Join(beRuns, "billing-run"))

	st, _ := store.New(t.TempDir())
	mgr := runmgr.New(st, util.NewLogger(util.LevelInfo), beRuns)
	srv := httptest.NewServer(New(st, mgr, beRuns, "127.0.0.1", 0, util.NewLogger(util.LevelInfo)).Handler())
	defer srv.Close()

	// Project + repos.
	p, _ := st.CreateProject(store.Project{Name: "demo"})
	order, _ := st.CreateRepo(p.ID, store.Repo{Name: "order-service", Path: filepath.Join(root, "testdata", "sample_service_repos", "order-service"), Kind: "service_repo"})
	billing, _ := st.CreateRepo(p.ID, store.Repo{Name: "billing-service", Path: filepath.Join(root, "testdata", "sample_service_repos", "billing-service"), Kind: "service_repo"})

	// Start a run via the API.
	resp, data := doJSON(t, "POST", srv.URL+"/api/projects/"+p.ID+"/runs", map[string]any{
		"repos": []map[string]string{
			{"repo_id": order.ID, "diffmind_run_id": "order-run"},
			{"repo_id": billing.ID, "diffmind_run_id": "billing-run"},
		},
	})
	if resp.StatusCode != 201 {
		t.Fatalf("create run = %d: %s", resp.StatusCode, data)
	}
	var run store.RunManifest
	json.Unmarshal(data, &run)
	if run.ID == "" {
		t.Fatal("no run id")
	}

	// Stream SSE until run_completed.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/projects/"+p.ID+"/runs/"+run.ID+"/events", nil)
	req.Header.Set("Accept", "text/event-stream")
	sse, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer sse.Body.Close()

	seen := map[string]bool{}
	br := bufio.NewReader(sse.Body)
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			break
		}
		line = strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(line, "event: ") {
			seen[strings.TrimPrefix(line, "event: ")] = true
		}
		if seen["run_completed"] || seen["eof"] {
			break
		}
	}
	mgr.WaitFor(p.ID, run.ID)
	if !seen["run_started"] {
		t.Fatalf("did not observe run_started; saw %v", seen)
	}

	// Run state should be completed.
	resp, data = doJSON(t, "GET", srv.URL+"/api/projects/"+p.ID+"/runs/"+run.ID, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("get run = %d", resp.StatusCode)
	}
	var rr struct {
		Run store.RunManifest `json:"run"`
	}
	json.Unmarshal(data, &rr)
	if rr.Run.Status != store.RunCompleted {
		t.Fatalf("run status = %s (err=%s)", rr.Run.Status, rr.Run.Error)
	}

	// Graph endpoint should serve the persisted graph.
	resp, data = doJSON(t, "GET", srv.URL+"/api/projects/"+p.ID+"/runs/"+run.ID+"/graph", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("graph = %d: %s", resp.StatusCode, data)
	}
	var graph map[string]any
	if err := json.Unmarshal(data, &graph); err != nil {
		t.Fatalf("graph not valid json: %v", err)
	}
	if _, ok := graph["services"]; !ok {
		t.Fatal("graph missing services")
	}

	// Delete the run.
	resp, _ = doJSON(t, "DELETE", srv.URL+"/api/projects/"+p.ID+"/runs/"+run.ID, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("delete run = %d", resp.StatusCode)
	}
}
