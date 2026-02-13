package analyzers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"diffmind/internal/facts"
)

type mockLLMClient struct {
	response string
	err      error
}

func (m mockLLMClient) CompleteJSON(context.Context, string, string, string) (string, error) {
	return m.response, m.err
}

func TestAugmentWithClientAddsInferredFactsWithEvidence(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, root, "api/routes.js", "router.get('/health', handler)\n")
	mustWrite(t, root, "config/app.go", "v := os.Getenv(\"APP_ENV\")\n")

	base := facts.Bundle{Facts: []facts.Fact{}, Evidence: []facts.Evidence{}}
	pack, _, err := buildEvidencePack(root, "snap1", 20, 5000)
	if err != nil {
		t.Fatalf("buildEvidencePack error: %v", err)
	}
	if len(pack) == 0 {
		t.Fatalf("expected non-empty evidence pack")
	}

	validID := pack[0].Evidence.ID
	client := mockLLMClient{response: fmt.Sprintf(`{"facts":[{"type":"Endpoint","attributes":{"method":"GET","path":"/health"},"evidence_ids":["%s"],"confidence":0.7}]}`, validID)}

	aug, added, tracePath, err := augmentWithClient(context.Background(), client, root, base, llmOptions{
		Enabled:        true,
		Model:          "mock-model",
		Task:           "augment-routes",
		MaxFiles:       20,
		MaxChars:       5000,
		DefaultConf:    0.55,
		TraceOutputDir: filepath.Join(root, ".diffmind", "llm", "traces"),
	}, "snap1")
	if err != nil {
		t.Fatalf("augmentWithClient error: %v", err)
	}
	if added != 1 {
		t.Fatalf("expected 1 added fact, got %d", added)
	}
	if len(aug.Facts) != 1 {
		t.Fatalf("expected exactly 1 fact, got %d", len(aug.Facts))
	}
	if !aug.Facts[0].Provenance.Inferred || aug.Facts[0].Provenance.Deterministic {
		t.Fatalf("expected inferred llm provenance")
	}
	if len(aug.Facts[0].EvidenceIDs) == 0 {
		t.Fatalf("expected evidence references")
	}
	if tracePath == "" {
		t.Fatalf("expected trace path")
	}
	if _, err := os.Stat(tracePath); err != nil {
		t.Fatalf("expected trace file, got %v", err)
	}
	if err := facts.ValidateBundle(aug); err != nil {
		t.Fatalf("augmented bundle should validate: %v", err)
	}
}

func TestAugmentWithClientIgnoresUnknownEvidenceIDs(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, root, "api/routes.js", "router.get('/health', handler)\n")
	base := facts.Bundle{}

	client := mockLLMClient{response: `{"facts":[{"type":"Endpoint","attributes":{"method":"GET","path":"/health"},"evidence_ids":["unknown"],"confidence":0.7}]}`}
	aug, added, _, err := augmentWithClient(context.Background(), client, root, base, llmOptions{
		Enabled:        true,
		Model:          "mock-model",
		Task:           "augment-routes",
		MaxFiles:       20,
		MaxChars:       5000,
		DefaultConf:    0.55,
		TraceOutputDir: filepath.Join(root, ".diffmind", "llm", "traces"),
	}, "snap1")
	if err != nil {
		t.Fatalf("augmentWithClient error: %v", err)
	}
	if added != 0 || len(aug.Facts) != 0 {
		t.Fatalf("expected no facts added when evidence ids are invalid")
	}
}

func TestParseLLMResponseCodeFence(t *testing.T) {
	raw := "```json\n{\"facts\":[{\"type\":\"ConfigKey\",\"attributes\":{\"key\":\"X\"},\"evidence_ids\":[\"a\"]}]}\n```"
	out, err := parseLLMResponse(raw)
	if err != nil {
		t.Fatalf("parseLLMResponse error: %v", err)
	}
	if len(out.Facts) != 1 || !strings.EqualFold(out.Facts[0].Type, "ConfigKey") {
		t.Fatalf("unexpected parse output: %+v", out)
	}
}
