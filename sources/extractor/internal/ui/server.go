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

	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

type Server struct {
	baseDir string
	host    string
	port    int
}

type RunData struct {
	RunID        string                      `json:"run_id"`
	Manifest     model.RunManifest           `json:"manifest"`
	Exposures    map[string][]map[string]any `json:"exposures"`
	Dependencies map[string][]map[string]any `json:"dependencies"`
	Connections  map[string][]map[string]any `json:"connections"`
	Unresolved   map[string][]map[string]any `json:"unresolved"`
	Counts       map[string]map[string]int   `json:"counts"`
}

func New(baseDir, host string, port int) *Server {
	if strings.TrimSpace(baseDir) == "" {
		baseDir = ".diffmind/runs"
	}
	if strings.TrimSpace(host) == "" {
		host = "127.0.0.1"
	}
	if port <= 0 {
		port = 8080
	}
	return &Server{baseDir: baseDir, host: host, port: port}
}

func (s *Server) Addr() string {
	return fmt.Sprintf("%s:%d", s.host, s.port)
}

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
		util.Info("ui", "dashboard listening", map[string]any{"addr": s.Addr(), "base_dir": s.baseDir})
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

func (s *Server) loadRun(runID string) (RunData, error) {
	runDir := filepath.Join(s.baseDir, runID)
	manifestPath := filepath.Join(runDir, "run_manifest.json")
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		return RunData{}, err
	}
	var manifest model.RunManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return RunData{}, fmt.Errorf("parse manifest: %w", err)
	}
	exposures, err := readObjectArrayDir(filepath.Join(runDir, "exposures"))
	if err != nil {
		return RunData{}, err
	}
	deps, err := readObjectArrayDir(filepath.Join(runDir, "dependencies"))
	if err != nil {
		return RunData{}, err
	}
	connections, err := readObjectArrayDir(filepath.Join(runDir, "connections"))
	if err != nil {
		return RunData{}, err
	}
	unresolved, err := readObjectArrayDir(filepath.Join(runDir, "unresolved"))
	if err != nil {
		return RunData{}, err
	}

	return RunData{
		RunID:        runID,
		Manifest:     manifest,
		Exposures:    exposures,
		Dependencies: deps,
		Connections:  connections,
		Unresolved:   unresolved,
		Counts: map[string]map[string]int{
			"exposures":    countByFile(exposures),
			"dependencies": countByFile(deps),
			"connections":  countByFile(connections),
			"unresolved":   countByFile(unresolved),
		},
	}, nil
}

func readObjectArrayDir(dir string) (map[string][]map[string]any, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string][]map[string]any{}, nil
		}
		return nil, err
	}
	out := make(map[string][]map[string]any, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var items []map[string]any
		if err := json.Unmarshal(b, &items); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		name := strings.TrimSuffix(e.Name(), ".json")
		out[name] = items
	}
	return out, nil
}

func countByFile(in map[string][]map[string]any) map[string]int {
	out := make(map[string]int, len(in))
	for k, items := range in {
		out[k] = len(items)
	}
	return out
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

const indexHTML = `<!doctype html>
<html>
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>DiffMind Dashboard</title>
  <style>
    :root { --bg:#0f172a; --panel:#111827; --panel2:#1f2937; --text:#e5e7eb; --muted:#9ca3af; --accent:#22c55e; }
    * { box-sizing: border-box; }
    body { margin:0; font-family: "IBM Plex Sans", "Segoe UI", sans-serif; background: linear-gradient(135deg,#0b1220,#121a2d); color: var(--text); }
    header { padding: 16px 20px; border-bottom: 1px solid #233046; background: rgba(15,23,42,.8); position: sticky; top:0; backdrop-filter: blur(8px); }
    .row { display:flex; gap:12px; align-items:center; flex-wrap:wrap; }
    .container { padding: 16px 20px; display:grid; gap:14px; }
    .card { background: var(--panel); border: 1px solid #273449; border-radius: 12px; padding: 14px; }
    .summary { display:grid; grid-template-columns: repeat(auto-fit,minmax(180px,1fr)); gap:10px; }
    .metric { background: var(--panel2); border-radius:10px; padding:10px; }
    .label { color: var(--muted); font-size: 12px; }
    .value { font-size: 24px; font-weight: 700; margin-top: 4px; }
    select, button { background:#0b1322; color:var(--text); border:1px solid #334155; border-radius:8px; padding:8px 10px; }
    button { cursor:pointer; }
    .section-title { margin:0 0 8px 0; font-size: 14px; color:#cbd5e1; letter-spacing:.03em; text-transform:uppercase; }
    details { border:1px solid #324156; border-radius:8px; padding:8px 10px; margin-bottom:8px; }
    summary { cursor:pointer; font-weight:600; }
    pre { white-space: pre-wrap; word-break: break-word; background:#0b1322; padding:10px; border-radius:8px; border:1px solid #2a3a50; overflow:auto; }
    .muted { color: var(--muted); }
  </style>
</head>
<body>
<header>
  <div class="row">
    <strong>DiffMind Dashboard</strong>
    <span class="muted">Run:</span>
    <select id="runSelect"></select>
    <button onclick="loadRun()">Refresh</button>
    <span id="status" class="muted"></span>
  </div>
</header>
<div class="container">
  <div class="card" id="manifestCard"></div>
  <div class="card" id="exposuresCard"></div>
  <div class="card" id="depsCard"></div>
  <div class="card" id="connsCard"></div>
  <div class="card" id="unresolvedCard"></div>
</div>
<script>
const fmt = (obj) => JSON.stringify(obj, null, 2);

async function fetchJSON(url) {
  const r = await fetch(url);
  if (!r.ok) throw new Error(await r.text());
  return await r.json();
}

function renderGroup(title, data, counts) {
  const keys = Object.keys(data || {}).sort();
  let html = '<h3 class="section-title">' + title + '</h3>';
  if (!keys.length) return html + '<div class="muted">No data</div>';
  for (const k of keys) {
    const items = data[k] || [];
    const c = counts && counts[k] !== undefined ? counts[k] : items.length;
    html += '<details><summary>' + k + ' (' + c + ')</summary><pre>' + fmt(items) + '</pre></details>';
  }
  return html;
}

async function loadRuns() {
  const runs = await fetchJSON('/api/runs');
  const sel = document.getElementById('runSelect');
  sel.innerHTML = '';
  for (const r of (runs.runs || [])) {
    const opt = document.createElement('option');
    opt.value = r;
    opt.textContent = r;
    sel.appendChild(opt);
  }
}

async function loadRun() {
  const status = document.getElementById('status');
  status.textContent = 'Loading...';
  try {
    const runID = document.getElementById('runSelect').value || 'latest';
    const data = await fetchJSON('/api/run/' + encodeURIComponent(runID));
    document.getElementById('manifestCard').innerHTML =
      '<h3 class=\"section-title\">Run Manifest</h3>' +
      '<div class=\"summary\">' +
        '<div class=\"metric\"><div class=\"label\">Run ID</div><div class=\"value\" style=\"font-size:16px\">' + data.run_id + '</div></div>' +
        '<div class=\"metric\"><div class=\"label\">Exposures</div><div class=\"value\">' + (data.manifest.counts.exposures || 0) + '</div></div>' +
        '<div class=\"metric\"><div class=\"label\">Dependencies</div><div class=\"value\">' + (data.manifest.counts.dependencies || 0) + '</div></div>' +
        '<div class=\"metric\"><div class=\"label\">Connections</div><div class=\"value\">' + (data.manifest.counts.connections || 0) + '</div></div>' +
        '<div class=\"metric\"><div class=\"label\">Unresolved</div><div class=\"value\">' + (data.manifest.counts.unresolved || 0) + '</div></div>' +
      '</div>' +
      '<pre>' + fmt(data.manifest) + '</pre>';

    document.getElementById('exposuresCard').innerHTML = renderGroup('Exposures', data.exposures, data.counts.exposures);
    document.getElementById('depsCard').innerHTML = renderGroup('Dependencies', data.dependencies, data.counts.dependencies);
    document.getElementById('connsCard').innerHTML = renderGroup('Connections', data.connections, data.counts.connections);
    document.getElementById('unresolvedCard').innerHTML = renderGroup('Unresolved', data.unresolved, data.counts.unresolved);
    status.textContent = 'Loaded';
  } catch (e) {
    status.textContent = 'Error';
    document.getElementById('manifestCard').innerHTML = '<pre>' + String(e) + '</pre>';
  }
}

(async function init() {
  await loadRuns();
  await loadRun();
  setInterval(loadRun, 10000);
})();
</script>
</body>
</html>`
