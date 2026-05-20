package agents

import (
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/objectives"
)

// mkSeed builds a detailJob for tests. Path is the primary
// source_location.file used by the grouping affinity logic; name is
// what we expect the model to see and the affinity rules to
// tokenize / prefix-match against.
func mkSeed(kind model.EntityKind, typ, name, path string) detailJob {
	return detailJob{
		Objective: objectives.Objective{Kind: kind, Type: typ, ID: string(kind) + "." + typ},
		Seed: llmEntity{
			Name:      name,
			Type:      typ,
			Locations: []llmLocation{{File: path}},
		},
	}
}

// All batches must be (kind, type)-homogenous. Mixing
// http_route with queue_consumer in one prompt forces the model
// to context-switch mid-call.
func TestDetailGroups_HomogenousByObjective(t *testing.T) {
	jobs := []detailJob{
		mkSeed(model.KindExposure, "http_route", "GET /a", "src/controller/A.go"),
		mkSeed(model.KindExposure, "http_route", "GET /b", "src/controller/A.go"),
		mkSeed(model.KindDependency, "db_operation", "X.findById", "src/repo/X.go"),
		mkSeed(model.KindDependency, "db_operation", "X.save", "src/repo/X.go"),
		mkSeed(model.KindExposure, "queue_consumer", "consume.foo", "src/queue/Foo.go"),
	}
	batches := detailGroups(jobs)
	for i, b := range batches {
		k0, t0 := b[0].Objective.Kind, b[0].Objective.Type
		for _, j := range b[1:] {
			if j.Objective.Kind != k0 || j.Objective.Type != t0 {
				t.Errorf("batch %d mixes %s.%s with %s.%s", i, k0, t0, j.Objective.Kind, j.Objective.Type)
			}
		}
	}
}

// Entities in the same source file SHOULD land in the same batch.
// This is the cheapest signal and the one the model benefits from
// most (one file read, many entities answered).
func TestDetailGroups_SameFileClusters(t *testing.T) {
	jobs := []detailJob{
		mkSeed(model.KindExposure, "http_route", "GET /a", "src/controller/A.go"),
		mkSeed(model.KindExposure, "http_route", "GET /b", "src/controller/A.go"),
		mkSeed(model.KindExposure, "http_route", "POST /c", "src/controller/A.go"),
		mkSeed(model.KindExposure, "http_route", "GET /x", "src/controller/X.go"),
	}
	batches := detailGroups(jobs)
	// Find the batch containing "GET /a"; "GET /b" and "POST /c" must be there too.
	var hostBatch []detailJob
	for _, b := range batches {
		for _, j := range b {
			if j.Seed.Name == "GET /a" {
				hostBatch = b
				break
			}
		}
		if hostBatch != nil {
			break
		}
	}
	if hostBatch == nil {
		t.Fatal("could not find batch containing GET /a")
	}
	names := map[string]bool{}
	for _, j := range hostBatch {
		names[j.Seed.Name] = true
	}
	for _, want := range []string{"GET /a", "GET /b", "POST /c"} {
		if !names[want] {
			t.Errorf("expected %q in same-file batch with GET /a; got %v", want, names)
		}
	}
}

// No batch may exceed the hard cap, even when all entities are
// strongly related. Cap protects against bad LLM behaviour on
// over-large prompts.
func TestDetailGroups_HardCap(t *testing.T) {
	// 30 entities all in the same file. Without a cap they'd
	// collapse into one batch.
	jobs := make([]detailJob, 30)
	for i := range jobs {
		name := "method" + string(rune('A'+i))
		jobs[i] = mkSeed(model.KindDependency, "db_operation", name, "src/repo/Big.go")
	}
	batches := detailGroups(jobs)
	for i, b := range batches {
		if len(b) > detailBatchHardCap {
			t.Errorf("batch %d has %d entities; hard cap is %d", i, len(b), detailBatchHardCap)
		}
	}
}

// Empty input must not panic.
func TestDetailGroups_Empty(t *testing.T) {
	if got := detailGroups(nil); got != nil {
		t.Errorf("nil input → %v; want nil", got)
	}
	if got := detailGroups([]detailJob{}); got != nil {
		t.Errorf("empty input → %v; want nil", got)
	}
}

// Singletons get their own batch and do not collapse together.
// Tests the "no affinity → new batch" branch.
func TestDetailGroups_UnrelatedEntitiesGetSeparateBatches(t *testing.T) {
	jobs := []detailJob{
		mkSeed(model.KindExposure, "http_route", "GET /a", "src/aaa/A.go"),
		mkSeed(model.KindExposure, "http_route", "DELETE /z", "src/zzz/Z.go"),
		mkSeed(model.KindExposure, "http_route", "PATCH /m", "src/mmm/M.go"),
	}
	batches := detailGroups(jobs)
	if len(batches) != 3 {
		t.Errorf("expected 3 batches for 3 unrelated entities; got %d (%v)", len(batches), batches)
	}
}

// Determinism: running detailGroups twice on the same input must
// produce the same batches. Otherwise dashboard diffs across runs
// would be noise.
func TestDetailGroups_Deterministic(t *testing.T) {
	jobs := []detailJob{
		mkSeed(model.KindDependency, "db_operation", "X.save", "src/repo/X.go"),
		mkSeed(model.KindDependency, "db_operation", "X.findById", "src/repo/X.go"),
		mkSeed(model.KindDependency, "db_operation", "Y.save", "src/repo/Y.go"),
		mkSeed(model.KindExposure, "http_route", "GET /a", "src/api/A.go"),
		mkSeed(model.KindExposure, "http_route", "GET /b", "src/api/A.go"),
	}
	first := detailGroups(jobs)
	second := detailGroups(jobs)
	if len(first) != len(second) {
		t.Fatalf("non-deterministic batch counts: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if len(first[i]) != len(second[i]) {
			t.Errorf("batch %d size diverged: %d vs %d", i, len(first[i]), len(second[i]))
			continue
		}
		for j := range first[i] {
			if first[i][j].Seed.Name != second[i][j].Seed.Name {
				t.Errorf("batch %d position %d diverged: %q vs %q", i, j, first[i][j].Seed.Name, second[i][j].Seed.Name)
			}
		}
	}
}

// Name-token affinity must catch repository methods on the same
// class even when file paths happen to differ. The catalogue
// repo had a few `CampaignSettingRepository.*` methods that live
// in service/abstracts/AbstractService.java (not in the obvious
// repository/ directory); name overlap binds them.
func TestDetailGroups_NameTokenAffinity(t *testing.T) {
	jobs := []detailJob{
		mkSeed(model.KindDependency, "db_operation", "CampaignSettingRepository.findOne", "src/service/abstracts/AbstractService.go"),
		mkSeed(model.KindDependency, "db_operation", "CampaignSettingRepository.findAll", "src/service/abstracts/AbstractService.go"),
		mkSeed(model.KindDependency, "db_operation", "CampaignSettingRepository.save", "src/service/abstracts/AbstractService.go"),
	}
	batches := detailGroups(jobs)
	if len(batches) != 1 {
		t.Errorf("3 methods on the same repo class should batch together; got %d batches", len(batches))
	}
}

func TestNameTokens(t *testing.T) {
	cases := map[string][]string{
		"AccountRepository.findByEmail":          {"account", "repository", "find", "email"},
		"GET /campaigns/{id}/ad-sets":            {"get", "campaigns", "sets"}, // "id" + "ad" too short
		"campaign_settings_change_event_publish": {"campaign", "settings", "change", "event", "publish"},
		"a.b.c":                                  nil, // all tokens too short
		"":                                       nil,
	}
	for in, want := range cases {
		got := nameTokens(in)
		if len(want) == 0 {
			if len(got) != 0 {
				t.Errorf("nameTokens(%q) = %v; want empty", in, got)
			}
			continue
		}
		for _, w := range want {
			if _, ok := got[w]; !ok {
				t.Errorf("nameTokens(%q) missing %q; got %v", in, w, got)
			}
		}
	}
}
