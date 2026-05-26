package agents

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/config"
	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/objectives"
)

func httpRouteObjective() objectives.Objective {
	return objectives.Objective{ID: "exposure.http_route", Kind: model.KindExposure, Type: "http_route"}
}

func TestShouldReexamineFlagsLowConfidence(t *testing.T) {
	e := llmEntity{
		Type: "http_route", Name: "GET /x", Confidence: 0.5,
		Details:   map[string]any{"method": "GET", "path": "/x"},
		Locations: []llmLocation{{File: "a.go", StartLine: 1}},
	}
	reason, _, needs := shouldReexamine(httpRouteObjective(), &e, 0.7)
	if !needs {
		t.Fatalf("expected low-confidence seed to be flagged")
	}
	if reason != "low_confidence" {
		t.Fatalf("unexpected reason: %s", reason)
	}
}

func TestShouldReexamineFlagsMissingLocation(t *testing.T) {
	e := llmEntity{
		Type: "http_route", Name: "GET /x", Confidence: 0.9,
		Details: map[string]any{"method": "GET", "path": "/x"},
	}
	reason, _, needs := shouldReexamine(httpRouteObjective(), &e, 0.7)
	if !needs || reason != "no_source_location" {
		t.Fatalf("expected no_source_location trigger, got reason=%q needs=%v", reason, needs)
	}
}

// With the back-fill logic, a name like "GET /x" should NOT trigger
// missing_required_details: we parse method/path out of the name and
// keep the seed clean for downstream stages.
func TestShouldReexamineDerivesHTTPMethodAndPathFromName(t *testing.T) {
	e := llmEntity{
		Type: "http_route", Name: "GET /accounts/{id}", Confidence: 0.95,
		Locations: []llmLocation{{File: "a.go", StartLine: 1}},
	}
	reason, _, needs := shouldReexamine(httpRouteObjective(), &e, 0.7)
	if needs {
		t.Fatalf("derived details from name should keep seed clean; reason=%q", reason)
	}
	if e.Details["method"] != "GET" || e.Details["path"] != "/accounts/{id}" {
		t.Fatalf("expected method/path to be back-filled, got %+v", e.Details)
	}
}

// Names without a clear method prefix should still flag re-examination.
func TestShouldReexamineFlagsHTTPRouteWithProseName(t *testing.T) {
	e := llmEntity{
		Type: "http_route", Name: "user search endpoint", Confidence: 0.95,
		Locations: []llmLocation{{File: "a.go", StartLine: 1}},
	}
	reason, _, needs := shouldReexamine(httpRouteObjective(), &e, 0.7)
	if !needs || reason != "missing_required_details" {
		t.Fatalf("prose name should still trigger; reason=%q needs=%v", reason, needs)
	}
}

func TestShouldReexamineClean(t *testing.T) {
	e := llmEntity{
		Type: "http_route", Name: "GET /x", Confidence: 0.9,
		Details:   map[string]any{"method": "GET", "path": "/x"},
		Locations: []llmLocation{{File: "a.go", StartLine: 1}},
	}
	_, _, needs := shouldReexamine(httpRouteObjective(), &e, 0.7)
	if needs {
		t.Fatalf("expected clean seed to pass")
	}
}

// Targeted tests for the splitMethodPath / splitServiceMethod helpers.
func TestSplitMethodPath(t *testing.T) {
	cases := []struct {
		in           string
		method, path string
		ok           bool
	}{
		{"GET /users/{id}", "GET", "/users/{id}", true},
		{"POST: /charge", "POST", "/charge", true},
		{"DELETE /v2/users/{uid}/sessions", "DELETE", "/v2/users/{uid}/sessions", true},
		{"GET /v1/foo (FooHandler)", "GET", "/v1/foo", true},
		{"POST relative-path", "", "", false},
		{"hello world", "", "", false},
		{"", "", "", false},
	}
	for _, c := range cases {
		m, p, ok := splitMethodPath(c.in)
		if ok != c.ok || m != c.method || p != c.path {
			t.Errorf("splitMethodPath(%q) = (%q,%q,%v) want (%q,%q,%v)",
				c.in, m, p, ok, c.method, c.path, c.ok)
		}
	}
}

func TestSplitServiceMethod(t *testing.T) {
	cases := []struct {
		in     string
		svc, m string
		ok     bool
	}{
		{"FooService.bar", "FooService", "bar", true},
		{"foo/bar", "foo", "bar", true},
		{"AccountService#list", "AccountService", "list", true},
		{"singletoken", "", "", false},
	}
	for _, c := range cases {
		s, m, ok := splitServiceMethod(c.in)
		if ok != c.ok || s != c.svc || m != c.m {
			t.Errorf("splitServiceMethod(%q) = (%q,%q,%v) want (%q,%q,%v)",
				c.in, s, m, ok, c.svc, c.m, c.ok)
		}
	}
}

func TestHasDetailKeyAcceptsCommonAliases(t *testing.T) {
	details := map[string]any{"queue_name": "my-q"}
	if !hasDetailKey(details, "queue") {
		t.Fatalf("expected queue_name alias to satisfy 'queue'")
	}
	details2 := map[string]any{"http_method": "GET"}
	if !hasDetailKey(details2, "method") {
		t.Fatalf("expected http_method alias to satisfy 'method'")
	}
}

func TestMissingRequiredDetailsUnknownTypeReturnsEmpty(t *testing.T) {
	if got := missingRequiredDetails("random_type", map[string]any{}); got != "" {
		t.Fatalf("unknown objective types should not require fields, got %q", got)
	}
}

// TestReexaminationCheckpointSkipsCompletedSuspects is the regression guard
// for the "retry re-runs all reexaminations" bug.
//
// Scenario: 3 suspects. Items A and B succeed on the first run; item C
// fails (stuck/timeout). The stage halts. On retry, only C should be
// re-asked — A and B must be restored from the checkpoint without issuing
// any new LLM calls.
func TestReexaminationCheckpointSkipsCompletedSuspects(t *testing.T) {
	runDir := t.TempDir()

	obj := objectives.Objective{
		ID:   "exposure.http_route",
		Kind: model.KindExposure,
		Type: "http_route",
	}

	// Build 3 seeds that all look suspect (low confidence so shouldReexamine flags them).
	makeJob := func(name string) detailJob {
		return detailJob{
			Objective: obj,
			Seed: llmEntity{
				Type:       "http_route",
				Name:       name,
				Confidence: 0.5, // below MinConfidence → flagged as suspect
				Locations:  []llmLocation{{File: "api.go", StartLine: 1}},
			},
		}
	}
	seeds := []detailJob{makeJob("GET /a"), makeJob("GET /b"), makeJob("GET /c")}

	// Pre-populate the checkpoint as if A (confirmed) and B (rejected) already ran.
	cfg := config.Default()
	o := &orchestrator{cfg: cfg, runDir: runDir}

	o.appendReexamEntity(reexamCheckpointEntry{
		Key:     reexamKey(obj.ID, "GET /a"),
		Outcome: "confirmed",
		Seed: &llmEntity{
			Type: "http_route", Name: "GET /a", Confidence: 0.85,
			Locations: []llmLocation{{File: "api.go", StartLine: 1}},
			Details:   map[string]any{"method": "GET", "path": "/a"},
		},
	})
	o.appendReexamEntity(reexamCheckpointEntry{
		Key:     reexamKey(obj.ID, "GET /b"),
		Outcome: "rejected",
		Unresolved: &model.UnresolvedItem{
			Kind: model.KindExposure, Type: "http_route", Name: "GET /b",
			ReasonCode: "rejected_on_reexamination",
		},
	})

	// Count how many times the LLM is actually called.
	var llmCalls atomic.Int32

	// Override the orchestrator with a fake OpenCode that tracks calls.
	// We use a fakeOpenCode-style fake that confirms item C.
	fakeOC := &fakeReexamOC{onPrompt: func() { llmCalls.Add(1) }}
	o.oc = fakeOC

	stateFilePath := filepath.Join(runDir, stateDir)
	checkpoint := o.loadReexaminationCheckpoint(stateFilePath)
	if len(checkpoint) != 2 {
		t.Fatalf("expected 2 checkpoint entries (A and B), got %d", len(checkpoint))
	}

	// Run reexamination. C is still suspect and not in the checkpoint, so
	// the orchestrator MUST ask the LLM exactly once (for C only).
	rf := &repoFacts{}
	cleaned, _, err, _ := o.runReexamination(context.Background(), seeds, rf, nil)
	if err != nil {
		t.Fatalf("runReexamination returned error: %v", err)
	}

	calls := int(llmCalls.Load())
	if calls != 1 {
		t.Errorf("expected exactly 1 LLM call (for C only), got %d — A and B were not skipped from checkpoint", calls)
	}

	// A should be in cleanJobs (confirmed), B should be absent (rejected).
	foundA := false
	for _, j := range cleaned {
		if j.Seed.Name == "GET /a" {
			foundA = true
		}
		if j.Seed.Name == "GET /b" {
			t.Errorf("rejected item B should not appear in cleanJobs")
		}
	}
	if !foundA {
		t.Errorf("confirmed item A should appear in cleanJobs")
	}
}

// fakeReexamOC is a minimal OpenCode fake for reexamination tests that
// calls onPrompt on every PromptStructured invocation and returns a
// confirmed entity so runReexamineOne succeeds.
type fakeReexamOC struct {
	onPrompt func()
}

func (f *fakeReexamOC) Enabled() bool { return true }
func (f *fakeReexamOC) CreateSession(_ context.Context, _ string) (string, error) {
	return "s", nil
}
func (f *fakeReexamOC) DeleteSession(_ context.Context, _, _ string) error { return nil }
func (f *fakeReexamOC) PromptText(_ context.Context, _, _, _ string) (string, error) {
	return "{}", nil
}
func (f *fakeReexamOC) PromptStructured(_ context.Context, _, _, _ string, _ map[string]any) (map[string]any, error) {
	if f.onPrompt != nil {
		f.onPrompt()
	}
	// Return a confirmed entity so runReexamineOne treats item C as confirmed.
	return map[string]any{
		"items": []any{map[string]any{
			"type": "http_route", "name": "GET /c", "confidence": 0.9,
			"details":          map[string]any{"method": "GET", "path": "/c"},
			"source_locations": []any{map[string]any{"file": "api.go", "start_line": 1}},
		}},
	}, nil
}
