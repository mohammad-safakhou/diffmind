package analyzers

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

func TestRunWithExtractorSelection(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, ".diffmind")

	mustWrite(t, root, "cmd/main.go", "package main\nimport \"net/http\"\nfunc main(){http.Get(\"https://example.com\")}\n")
	mustWrite(t, root, "internal/http/routes.js", "app.get('/health', handler)\n")
	mustWrite(t, root, "internal/config/app.go", "package config\nimport \"os\"\nfunc load(){ _ = os.Getenv(\"APP_ENV\") }\n")

	if err := Run(context.Background(), []string{"--source", root, "--out", out, "--snapshot-id", "snap-analyzer", "--extractors", "runtime,config"}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(out, "analyzers", "bundle.json"))
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	var bundle facts.Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}
	assertFactType(t, bundle, "RuntimeUnit")
	assertFactType(t, bundle, "ConfigKey")
	assertNoFactType(t, bundle, "Endpoint")
	assertNoFactType(t, bundle, "ExternalCall")

	reportData, err := os.ReadFile(filepath.Join(out, "analyzers", "report.json"))
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var report Report
	if err := json.Unmarshal(reportData, &report); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if len(report.Extractors) != 2 || report.Extractors[0] != "config" || report.Extractors[1] != "runtime" {
		t.Fatalf("unexpected report extractors: %#v", report.Extractors)
	}
}

func TestRunRejectsUnknownExtractor(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, ".diffmind")
	mustWrite(t, root, "cmd/main.go", "package main\nfunc main(){}\n")

	err := Run(context.Background(), []string{"--source", root, "--out", out, "--extractors", "runtime,unknown"})
	if err == nil {
		t.Fatalf("expected error for unknown extractor")
	}
	if got := err.Error(); got == "" || !strings.Contains(got, `unsupported extractor "unknown"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGoSemanticExtractionHandlesAliasedNetHTTP(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, ".diffmind")

	mustWrite(t, root, "cmd/main.go", `
package main

import nethttp "net/http"

func main() {
	mux := nethttp.NewServeMux()
	mux.HandleFunc("/healthz", func(w nethttp.ResponseWriter, r *nethttp.Request) {})
	endpoint := "https://api.example.com/users"
	req, _ := nethttp.NewRequest(nethttp.MethodPut, endpoint, nil)
	_ = req
}
`)

	if err := Run(context.Background(), []string{"--source", root, "--out", out, "--snapshot-id", "snap-semantic", "--extractors", "endpoint,external_http"}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(out, "analyzers", "bundle.json"))
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	var bundle facts.Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}

	if !hasEndpoint(bundle, "ANY", "/healthz") {
		t.Fatalf("expected semantic endpoint for HandleFunc")
	}
	if !hasExternalCall(bundle, "PUT", "endpoint") {
		t.Fatalf("expected semantic outbound call for aliased net/http NewRequest")
	}
}

func TestJSTSSemanticExtractionRouterAndAxiosClient(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, ".diffmind")

	mustWrite(t, root, "internal/http/routes.ts", `
import express from "express";
import axios from "axios";

const api = express.Router();
api.get("/healthz", (req, res) => res.send("ok"));

const client = axios.create();
client.post("https://svc.local/orders", { id: 1 });
`)

	if err := Run(context.Background(), []string{"--source", root, "--out", out, "--snapshot-id", "snap-jsts-semantic", "--extractors", "endpoint,external_http"}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(out, "analyzers", "bundle.json"))
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	var bundle facts.Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}

	if !hasEndpoint(bundle, "GET", "/healthz") {
		t.Fatalf("expected semantic endpoint for express router variable")
	}
	if !hasExternalCall(bundle, "POST", "https://svc.local/orders") {
		t.Fatalf("expected semantic outbound call for axios client variable")
	}
}

func TestPythonSemanticExtractionFlaskRouteAndRequests(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, ".diffmind")

	mustWrite(t, root, "app.py", `
from flask import Flask
import requests as rq

app = Flask(__name__)

@app.route("/health", methods=["GET"])
def health():
    return "ok"

rq.post("https://svc.local/orders", json={"id": 1})
`)

	if err := Run(context.Background(), []string{"--source", root, "--out", out, "--snapshot-id", "snap-py-semantic", "--extractors", "endpoint,external_http"}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(out, "analyzers", "bundle.json"))
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	var bundle facts.Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}

	if !hasEndpoint(bundle, "GET", "/health") {
		t.Fatalf("expected semantic endpoint from @app.route")
	}
	if !hasExternalCall(bundle, "POST", "https://svc.local/orders") {
		t.Fatalf("expected semantic outbound call from requests alias")
	}
}

func TestJavaSemanticExtractionSpringAndRestTemplate(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, ".diffmind")

	mustWrite(t, root, "src/main/java/com/acme/Controller.java", `
package com.acme;

import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestMethod;
import org.springframework.web.bind.annotation.RestController;
import org.springframework.web.client.RestTemplate;

@RestController
public class Controller {
  private final RestTemplate restTemplate = new RestTemplate();

  @GetMapping("/users")
  public String users() {
    return "ok";
  }

  @RequestMapping(value = "/orders", method = RequestMethod.POST)
  public String orders() {
    return restTemplate.postForObject("https://svc.local/orders", null, String.class);
  }
}
`)

	if err := Run(context.Background(), []string{"--source", root, "--out", out, "--snapshot-id", "snap-java-semantic", "--extractors", "endpoint,external_http"}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(out, "analyzers", "bundle.json"))
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	var bundle facts.Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}

	if !hasEndpoint(bundle, "GET", "/users") {
		t.Fatalf("expected semantic endpoint from @GetMapping")
	}
	if !hasEndpoint(bundle, "POST", "/orders") {
		t.Fatalf("expected semantic endpoint from @RequestMapping(method=POST)")
	}
	if !hasExternalCall(bundle, "POST", "https://svc.local/orders") {
		t.Fatalf("expected semantic outbound call from RestTemplate.postForObject")
	}
}

func TestSemanticModelProducesUnifiedCodeFactsAcrossLanguages(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, ".diffmind")

	mustWrite(t, root, "go/main.go", `
package main
func ping() {}
func main(){ ping() }
`)
	mustWrite(t, root, "ts/app.ts", `
function pingTs() {}
pingTs()
`)
	mustWrite(t, root, "py/app.py", `
def ping_py():
    return None
ping_py()
`)
	mustWrite(t, root, "java/App.java", `
class App {
  void pingJava() {}
  void run() { pingJava(); }
}
`)

	if err := Run(context.Background(), []string{"--source", root, "--out", out, "--snapshot-id", "snap-unified-model", "--extractors", "semantic_model"}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(out, "analyzers", "bundle.json"))
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	var bundle facts.Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}

	assertFactType(t, bundle, "CodeSymbol")
	assertFactType(t, bundle, "CodeCall")

	for _, lang := range []string{"go", "typescript", "python", "java"} {
		if !hasFactWithLanguage(bundle, "CodeSymbol", lang) {
			t.Fatalf("expected CodeSymbol for language=%s", lang)
		}
		if !hasFactWithLanguage(bundle, "CodeCall", lang) {
			t.Fatalf("expected CodeCall for language=%s", lang)
		}
	}

	reportData, err := os.ReadFile(filepath.Join(out, "analyzers", "report.json"))
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var report Report
	if err := json.Unmarshal(reportData, &report); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if report.CodeSymbols == 0 || report.CodeCalls == 0 {
		t.Fatalf("expected non-zero semantic model counters in report: %+v", report)
	}
}

func TestM5RuntimeBuildDeployFactsExtraction(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, ".diffmind")

	mustWrite(t, root, "Dockerfile", "FROM golang:1.23\nRUN go build -o app ./cmd/app\n")
	mustWrite(t, root, "docker-compose.yml", "services:\n  api:\n    image: ghcr.io/acme/api:1.0\n")
	mustWrite(t, root, ".github/workflows/ci.yml", "jobs:\n  build:\n    steps:\n      - run: docker build -t ghcr.io/acme/api:1.0 .\n")
	mustWrite(t, root, "k8s/deployment.yaml", "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: api\nspec:\n  template:\n    spec:\n      containers:\n      - name: api\n        image: ghcr.io/acme/api:1.0\n")
	mustWrite(t, root, "infra/main.tf", "resource \"aws_ecs_service\" \"api\" {}\n")

	if err := Run(context.Background(), []string{"--source", root, "--out", out, "--snapshot-id", "snap-m5"}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(out, "analyzers", "bundle.json"))
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	var bundle facts.Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}
	assertFactType(t, bundle, "PipelineStep")
	assertFactType(t, bundle, "InfraResource")
	assertFactType(t, bundle, "BuildArtifact")
	assertFactType(t, bundle, "Deployment")
}

func TestM6ConfigEnvironmentAndSensitiveExtraction(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, ".diffmind")

	mustWrite(t, root, ".env.prod", "DB_PASSWORD=super-secret\nLOG_LEVEL=info\n")
	mustWrite(t, root, "config/application-staging.yaml", "api_key: ${API_KEY}\nfeature_flag: true\n")
	mustWrite(t, root, ".github/workflows/ci.yml", "jobs:\n  test:\n    steps:\n      - run: echo hi\n      - run: echo ${{ secrets.DEPLOY_TOKEN }}\n")
	mustWrite(t, root, "app/main.go", "package main\nimport \"os\"\nfunc main(){ _ = os.Getenv(\"AUTH_TOKEN\") }\n")

	if err := Run(context.Background(), []string{"--source", root, "--out", out, "--snapshot-id", "snap-m6"}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(out, "analyzers", "bundle.json"))
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	var bundle facts.Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}
	assertFactType(t, bundle, "ConfigKey")
	assertFactType(t, bundle, "SensitiveSurface")
	if !hasConfigKeyWithEnv(bundle, "DB_PASSWORD", "prod") {
		t.Fatalf("expected DB_PASSWORD config key with prod scope")
	}
	if !hasSensitiveKind(bundle, "config_key") {
		t.Fatalf("expected sensitive surface for secret-like config key")
	}
	if !hasSensitiveKind(bundle, "pipeline_secret") {
		t.Fatalf("expected sensitive surface for pipeline secret reference")
	}
}

func TestM7DependencyOwnershipAndRiskExtraction(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, ".diffmind")

	mustWrite(t, root, "go.mod", "module example.com/acme/service\n\nrequire (\n\tgithub.com/gin-gonic/gin v1.10.0\n)\n")
	mustWrite(t, root, "package.json", "{\n  \"dependencies\": {\"axios\": \"^1.7.0\"},\n  \"devDependencies\": {\"typescript\": \"5.5.4\"}\n}\n")
	mustWrite(t, root, "requirements.txt", "requests>=2.0\n")
	mustWrite(t, root, "CODEOWNERS", "/go/ @platform-team\n/package.json @frontend-team\n")

	if err := Run(context.Background(), []string{"--source", root, "--out", out, "--snapshot-id", "snap-m7"}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(out, "analyzers", "bundle.json"))
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	var bundle facts.Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}

	assertFactType(t, bundle, "Dependency")
	assertFactType(t, bundle, "OwnershipRule")
	assertFactType(t, bundle, "DependencyRisk")
	if !hasDependency(bundle, "npm", "axios") {
		t.Fatalf("expected npm dependency axios")
	}
	if !hasDependency(bundle, "go", "github.com/gin-gonic/gin") {
		t.Fatalf("expected go dependency github.com/gin-gonic/gin")
	}
	if !hasOwnership(bundle, "@frontend-team") {
		t.Fatalf("expected ownership rule for @frontend-team")
	}
	if !hasDependencyRisk(bundle, "axios") {
		t.Fatalf("expected dependency risk for floating axios version")
	}
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

func assertNoFactType(t *testing.T, bundle facts.Bundle, factType string) {
	t.Helper()
	for _, f := range bundle.Facts {
		if f.Type == factType {
			t.Fatalf("unexpected fact type %s", factType)
		}
	}
}

func hasEndpoint(bundle facts.Bundle, method string, path string) bool {
	for _, f := range bundle.Facts {
		if f.Type != "Endpoint" {
			continue
		}
		m, _ := f.Attributes["method"].(string)
		p, _ := f.Attributes["path"].(string)
		if m == method && p == path {
			return true
		}
	}
	return false
}

func hasExternalCall(bundle facts.Bundle, method string, target string) bool {
	for _, f := range bundle.Facts {
		if f.Type != "ExternalCall" {
			continue
		}
		m, _ := f.Attributes["method"].(string)
		t, _ := f.Attributes["target"].(string)
		if m == method && t == target {
			return true
		}
	}
	return false
}

func hasFactWithLanguage(bundle facts.Bundle, factType string, language string) bool {
	for _, f := range bundle.Facts {
		if f.Type != factType {
			continue
		}
		lang, _ := f.Attributes["language"].(string)
		if lang == language {
			return true
		}
	}
	return false
}

func hasConfigKeyWithEnv(bundle facts.Bundle, key string, env string) bool {
	for _, f := range bundle.Facts {
		if f.Type != "ConfigKey" {
			continue
		}
		k, _ := f.Attributes["key"].(string)
		e, _ := f.Attributes["environment"].(string)
		if k == key && e == env {
			return true
		}
	}
	return false
}

func hasSensitiveKind(bundle facts.Bundle, kind string) bool {
	for _, f := range bundle.Facts {
		if f.Type != "SensitiveSurface" {
			continue
		}
		k, _ := f.Attributes["kind"].(string)
		if k == kind {
			return true
		}
	}
	return false
}

func hasDependency(bundle facts.Bundle, ecosystem string, name string) bool {
	for _, f := range bundle.Facts {
		if f.Type != "Dependency" {
			continue
		}
		e, _ := f.Attributes["ecosystem"].(string)
		n, _ := f.Attributes["name"].(string)
		if e == ecosystem && n == name {
			return true
		}
	}
	return false
}

func hasOwnership(bundle facts.Bundle, owner string) bool {
	for _, f := range bundle.Facts {
		if f.Type != "OwnershipRule" {
			continue
		}
		o, _ := f.Attributes["owner"].(string)
		if o == owner {
			return true
		}
	}
	return false
}

func hasDependencyRisk(bundle facts.Bundle, name string) bool {
	for _, f := range bundle.Facts {
		if f.Type != "DependencyRisk" {
			continue
		}
		n, _ := f.Attributes["name"].(string)
		if n == name {
			return true
		}
	}
	return false
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
