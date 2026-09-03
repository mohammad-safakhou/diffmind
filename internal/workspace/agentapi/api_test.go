package agentapi

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCatalogAndRequestBoundaries(t *testing.T) {
	names := map[string]bool{}
	for _, op := range Operations {
		if names[op.Name] || op.Name == "" || op.Description == "" {
			t.Fatalf("bad catalog entry %+v", op)
		}
		names[op.Name] = true
		in := Input{Operation: op.Name, Selectors: map[string]string{}, Confirm: op.Name}
		for _, part := range strings.Split(op.Path, "/") {
			if strings.HasPrefix(part, "{") {
				in.Selectors[strings.Trim(part, "{}")] = "valid-id"
			}
		}
		r, e := Request(context.Background(), in, op.Method == "GET")
		if e != nil {
			t.Fatalf("%s: %v", op.Name, e)
		}
		if r.URL.IsAbs() || r.Method != op.Method {
			t.Fatal("escaped catalog")
		}
	}
	for _, in := range []Input{
		{Operation: "http://evil.test"},
		{Operation: "get_project"},
		{Operation: "get_project", Selectors: map[string]string{"pid": "../private"}},
		{Operation: "get_project", Selectors: map[string]string{"pid": "%2fprivate"}},
		{Operation: "get_project", Selectors: map[string]string{"pid": "x", "extra": "x"}},
		{Operation: "get_project", Selectors: map[string]string{"pid": "x"}, Body: map[string]any{"a": 1}},
		{Operation: "delete_project", Selectors: map[string]string{"pid": "x"}},
		{Operation: "create_project", Body: map[string]any{"name": strings.Repeat("x", MaxBody)}},
	} {
		op, _ := Find(in.Operation)
		if _, e := Request(context.Background(), in, op.Method == "GET"); e == nil {
			t.Fatalf("accepted %+v", in.Operation)
		}
	}
	if _, e := Request(context.Background(), Input{Operation: "create_project"}, true); e == nil {
		t.Fatal("read-only mutation")
	}
	if _, e := Request(context.Background(), Input{Operation: "list_project_records"}, false); e == nil {
		t.Fatal("write tool read")
	}
	if _, e := Decode(200, http.Header{}, strings.NewReader(strings.Repeat("x", MaxResponse+1))); e == nil {
		t.Fatal("unbounded output")
	}
	out, e := Decode(429, http.Header{"Retry-After": []string{"10"}}, strings.NewReader("{\"error\":\"busy\"}"))
	if e != nil || out.Status != 429 || out.RetryAfter != "10" {
		t.Fatal(out, e)
	}
	r, e := Request(context.Background(), Input{Operation: "create_project"}, false)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := io.ReadAll(r.Body)
	if string(b) != "{}" {
		t.Fatal(string(b))
	}
}
