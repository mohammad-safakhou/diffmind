package ui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/extractor/catalog"
)

func TestArchitectureAPIManualSave(t *testing.T) {
	base := t.TempDir()
	s := New(base, "127.0.0.1", 8080)
	mux := http.NewServeMux()
	s.routes(mux)

	getRes := httptest.NewRecorder()
	mux.ServeHTTP(getRes, httptest.NewRequest(http.MethodGet, "/api/architecture", nil))
	if getRes.Code != http.StatusOK {
		t.Fatalf("get = %d: %s", getRes.Code, getRes.Body.String())
	}
	var doc map[string]any
	if err := json.Unmarshal(getRes.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if got := int(doc["revision"].(float64)); got != 0 {
		t.Fatalf("revision = %d, want 0", got)
	}
	doc["name"] = "Curated Architecture"

	saveBody, _ := json.Marshal(doc)
	saveRes := httptest.NewRecorder()
	mux.ServeHTTP(saveRes, httptest.NewRequest(http.MethodPut, "/api/architecture", bytes.NewReader(saveBody)))
	if saveRes.Code != http.StatusOK {
		t.Fatalf("save = %d: %s", saveRes.Code, saveRes.Body.String())
	}

	staleRes := httptest.NewRecorder()
	mux.ServeHTTP(staleRes, httptest.NewRequest(http.MethodPut, "/api/architecture", bytes.NewReader(saveBody)))
	if staleRes.Code != http.StatusConflict {
		t.Fatalf("stale save = %d, want 409: %s", staleRes.Code, staleRes.Body.String())
	}
}

func TestArchitectureImportRunRouteRemoved(t *testing.T) {
	base := t.TempDir()
	s := New(base, "127.0.0.1", 8080)
	mux := http.NewServeMux()
	s.routes(mux)

	res := httptest.NewRecorder()
	mux.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/api/architecture/import-run", bytes.NewReader(nil)))
	if res.Code != http.StatusNotFound {
		t.Fatalf("import route = %d, want 404", res.Code)
	}
}

var _ = catalog.SchemaVersion
