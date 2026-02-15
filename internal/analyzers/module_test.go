package analyzers

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"diffmind/internal/facts"
)

func TestAnalyzeProducesCoreFactTypes(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, ".diffmind")

	mustWrite(t, root, "go.mod", "module example.com/test\n")
	mustWrite(t, root, "cmd/main.go", "package main\nimport \"net/http\"\nfunc main(){http.Get(\"https://example.com\")}\n")
	mustWrite(t, root, "cmd/queue.go", "package main\nfunc q(){ writer.WriteMessages(ctx, kafka.Message{Topic:\"orders.events\"}); _,_ = ch.Consume(\"orders.events\", \"\", false, false, false, false, nil)}\n")
	mustWrite(t, root, "cmd/db.go", "package main\nimport \"database/sql\"\nfunc db(){ db, _ := sql.Open(\"postgres\", dsn); _ = db.Query(\"select 1\"); _ = db.Exec(\"insert into t values (1)\") }\n")
	mustWrite(t, root, "internal/http/routes.js", "app.get('/health', handler)\nfetch('https://api.example.com')\n")
	mustWrite(t, root, "internal/config/app.go", "package config\nimport \"os\"\nfunc load(){ _ = os.Getenv(\"APP_ENV\") }\n")
	mustWrite(t, root, ".github/workflows/ci.yml", "jobs:\n  build:\n    steps:\n      - uses: actions/checkout@v4\n      - run: go test ./...\n")
	mustWrite(t, root, "infra/main.tf", "resource \"aws_s3_bucket\" \"state\" {}\n")

	if err := Run(context.Background(), []string{"--source", root, "--out", out, "--snapshot-id", "snap-analyzer"}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	bundlePath := filepath.Join(out, "analyzers", "bundle.json")
	data, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}

	var bundle facts.Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}
	if err := facts.ValidateBundle(bundle); err != nil {
		t.Fatalf("bundle should validate: %v", err)
	}

	assertFactType(t, bundle, "RuntimeUnit")
	assertFactType(t, bundle, "Endpoint")
	assertFactType(t, bundle, "ExternalCall")
	assertFactType(t, bundle, "ConfigKey")
	assertFactType(t, bundle, "PipelineStep")
	assertFactType(t, bundle, "InfraResource")
	assertExternalCallProtocol(t, bundle, "queue")
	assertExternalCallProtocol(t, bundle, "db")
}

func assertFactType(t *testing.T, bundle facts.Bundle, factType string) {
	t.Helper()
	for _, f := range bundle.Facts {
		if f.Type == factType {
			return
		}
	}
	t.Fatalf("missing fact type %s", factType)
}

func assertExternalCallProtocol(t *testing.T, bundle facts.Bundle, protocol string) {
	t.Helper()
	for _, f := range bundle.Facts {
		if f.Type != "ExternalCall" {
			continue
		}
		if p, ok := f.Attributes["protocol"].(string); ok && p == protocol {
			return
		}
	}
	t.Fatalf("missing external call protocol %s", protocol)
}

func mustWrite(t *testing.T, root string, rel string, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}
