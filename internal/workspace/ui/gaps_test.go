package ui

import (
	"encoding/json"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/store"
)

func TestImprovementGapHTTPWorkflow(t *testing.T) {
	server, st := newTestServer(t)
	defer server.Close()
	project, err := st.CreateProject(store.Project{Name: "Agent improvements"})
	if err != nil {
		t.Fatal(err)
	}
	base := server.URL + "/api/v1/projects/" + project.ID + "/improvement-gaps"
	resp, body := doJSON(t, "POST", base, map[string]any{"revision": 0, "gap": map[string]any{"category": "missing_fact", "expected": "catalog", "observed": "unknown", "source_pointers": []string{"client.py:12"}}})
	if resp.StatusCode != 201 {
		t.Fatalf("create=%d %s", resp.StatusCode, body)
	}
	var created struct {
		Revision int       `json:"revision"`
		Gap      store.Gap `json:"gap"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatal(err)
	}
	resp, body = doJSON(t, "PATCH", base+"/"+created.Gap.ID, map[string]any{"revision": created.Revision, "status": "reproduced", "reason": "synthetic fixture"})
	if resp.StatusCode != 200 {
		t.Fatalf("transition=%d %s", resp.StatusCode, body)
	}
	resp, body = doJSON(t, "GET", base, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("list=%d %s", resp.StatusCode, body)
	}
	var collection store.GapCollection
	if err := json.Unmarshal(body, &collection); err != nil || collection.Revision != 2 || collection.Gaps[0].Status != "reproduced" {
		t.Fatalf("collection=%+v err=%v", collection, err)
	}
}
