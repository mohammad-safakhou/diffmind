package discovery

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/extraction"
	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/objectives"
)

func qpObj() objectives.Objective {
	return objectives.Objective{
		ID: "dependency.queue_publish", Kind: model.KindDependency,
		Type: "queue_publish", HighVariance: true,
	}
}

func qpCand(platform, queue string, conf float64, file string) extraction.Candidate {
	c := extraction.Candidate{
		Type: "queue_publish", Name: "publish " + queue, Confidence: conf,
		Details: map[string]any{"platform": platform, "queue": queue},
	}
	if file != "" {
		c.Locations = []extraction.Location{{File: file, StartLine: 1, EndLine: 2}}
	}
	return c
}

func hasTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}

func TestMergeVerifyConfirmedKeepsHigherConfidenceAndUnionsLocations(t *testing.T) {
	obj := qpObj()
	orig := []extraction.Candidate{qpCand("sns", "order-events", 0.6, "a.go")}
	ver := []extraction.Candidate{qpCand("sns", "order-events", 0.9, "b.go")}

	out := MergeVerify(obj, orig, ver, 0.5)
	if len(out) != 1 {
		t.Fatalf("want 1 merged item, got %d", len(out))
	}
	if out[0].Confidence != 0.9 {
		t.Errorf("confirmed item should take higher confidence 0.9, got %v", out[0].Confidence)
	}
	if len(out[0].Locations) != 2 {
		t.Errorf("confirmed item should union both locations, got %d", len(out[0].Locations))
	}
	if hasTag(out[0].Tags, verifyDoubtedTag) {
		t.Errorf("confirmed item must not be tagged doubted")
	}
}

func TestMergeVerifyUnconfirmedEvidenceBackedRetainedDowngraded(t *testing.T) {
	obj := qpObj()
	orig := []extraction.Candidate{qpCand("sns", "order-events", 0.8, "a.go")}

	out := MergeVerify(obj, orig, nil, 0.5)
	if len(out) != 1 {
		t.Fatalf("evidence-backed unconfirmed item must be retained, got %d", len(out))
	}
	// 0.8 * 0.7 = 0.56, above the 0.5 floor.
	if out[0].Confidence >= 0.8 || out[0].Confidence < 0.5 {
		t.Errorf("retained item should be downgraded and floored, got %v", out[0].Confidence)
	}
	if !hasTag(out[0].Tags, verifyDoubtedTag) {
		t.Errorf("retained-but-doubted item must be tagged %q", verifyDoubtedTag)
	}
}

func TestMergeVerifyDowngradeRespectsMinConfidenceFloor(t *testing.T) {
	obj := qpObj()
	orig := []extraction.Candidate{qpCand("sns", "order-events", 0.6, "a.go")}
	// 0.6 * 0.7 = 0.42, below a 0.7 floor → clamped to 0.7 so it survives the gate.
	out := MergeVerify(obj, orig, nil, 0.7)
	if len(out) != 1 || out[0].Confidence != 0.7 {
		t.Fatalf("downgrade must floor at MinConfidence 0.7, got %+v", out)
	}
}

func TestMergeVerifyDropsUnconfirmedStructurallyUnverifiable(t *testing.T) {
	obj := qpObj()
	// No source location → can never be verified → dropped when unconfirmed.
	orig := []extraction.Candidate{qpCand("sns", "order-events", 0.8, "")}
	out := MergeVerify(obj, orig, nil, 0.5)
	if len(out) != 0 {
		t.Fatalf("locationless unconfirmed item should be dropped, got %d", len(out))
	}
}

func TestMergeVerifyAddsNewlyFoundOnlyWhenEvidenceBacked(t *testing.T) {
	obj := qpObj()
	orig := []extraction.Candidate{qpCand("sns", "order-events", 0.9, "a.go")}
	ver := []extraction.Candidate{
		qpCand("sns", "order-events", 0.9, "a.go"),   // confirms the original
		qpCand("sns", "payment-events", 0.9, "b.go"), // newly found, evidence-backed
		qpCand("sns", "ghost-events", 0.9, ""),       // newly found, NO location → dropped
	}
	out := MergeVerify(obj, orig, ver, 0.5)
	if len(out) != 2 {
		t.Fatalf("want original + one evidence-backed new item, got %d: %+v", len(out), out)
	}
	gotQueues := map[string]bool{}
	for _, c := range out {
		gotQueues[c.Details["queue"].(string)] = true
	}
	if !gotQueues["order-events"] || !gotQueues["payment-events"] {
		t.Errorf("expected order-events + payment-events, got %v", gotQueues)
	}
	if gotQueues["ghost-events"] {
		t.Errorf("locationless newly-found item must not be added")
	}
}

// promptStub returns a PromptFunc backed by a per-job responder, counting calls.
func promptStub(calls *int, respond func(jobID string) (map[string]any, error)) PromptFunc {
	return func(_ context.Context, jobID, _ string, _ map[string]any) (map[string]any, error) {
		*calls++
		return respond(jobID)
	}
}

func itemsPayload(cs ...extraction.Candidate) map[string]any {
	return map[string]any{"items": cs}
}

func TestVerifyReaskFailSoftKeepsOriginals(t *testing.T) {
	calls := 0
	r := Runner{
		VerifyMode:    "reask",
		MinConfidence: 0.5,
		Prompt:        promptStub(&calls, func(string) (map[string]any, error) { return nil, errors.New("boom") }),
	}
	in := []extraction.Candidate{qpCand("sns", "order-events", 0.8, "a.go")}
	out := r.verifyReask(context.Background(), qpObj(), nil, in)
	if calls != 1 {
		t.Fatalf("verify should make exactly one call, got %d", calls)
	}
	if len(out) != 1 || out[0].Confidence != 0.8 {
		t.Fatalf("fail-soft must return the un-verified items unchanged, got %+v", out)
	}
}

func TestVerifyReaskMergesCorrectionsAndNewItems(t *testing.T) {
	calls := 0
	r := Runner{
		VerifyMode:    "reask",
		MinConfidence: 0.5,
		Prompt: promptStub(&calls, func(string) (map[string]any, error) {
			return itemsPayload(
				qpCand("sns", "order-events", 0.95, "a.go"),  // corrects the original up
				qpCand("sns", "payment-events", 0.9, "b.go"), // newly found
			), nil
		}),
	}
	in := []extraction.Candidate{qpCand("sns", "order-events", 0.6, "a.go")}
	out := r.verifyReask(context.Background(), qpObj(), nil, in)
	if len(out) != 2 {
		t.Fatalf("want corrected original + new item, got %d: %+v", len(out), out)
	}
}

func TestVerifyKSampleUnionsSamples(t *testing.T) {
	calls := 0
	r := Runner{
		VerifyMode:    "ksample",
		VerifySamples: 2,
		Prompt: promptStub(&calls, func(string) (map[string]any, error) {
			// The extra sample finds a queue the first pass missed.
			return itemsPayload(qpCand("sns", "payment-events", 0.9, "b.go")), nil
		}),
	}
	in := []extraction.Candidate{qpCand("sns", "order-events", 0.9, "a.go")}
	out := r.verifyKSample(context.Background(), qpObj(), nil, in)
	if calls != 1 {
		t.Fatalf("K=2 means one EXTRA sample call, got %d", calls)
	}
	if len(out) != 2 {
		t.Fatalf("union of the two samples should hold both queues, got %d: %+v", len(out), out)
	}
}

func TestVerifyKSampleFailSoftPerSample(t *testing.T) {
	calls := 0
	r := Runner{
		VerifyMode:    "ksample",
		VerifySamples: 2,
		Prompt:        promptStub(&calls, func(string) (map[string]any, error) { return nil, errors.New("boom") }),
	}
	in := []extraction.Candidate{qpCand("sns", "order-events", 0.9, "a.go")}
	out := r.verifyKSample(context.Background(), qpObj(), nil, in)
	if len(out) != 1 {
		t.Fatalf("a failed sample must be skipped, leaving the first pass, got %d", len(out))
	}
}

func TestVerifyItemsModeOffIsNoOp(t *testing.T) {
	calls := 0
	r := Runner{
		VerifyMode: "",
		Prompt:     promptStub(&calls, func(string) (map[string]any, error) { return itemsPayload(), nil }),
	}
	in := []extraction.Candidate{qpCand("sns", "order-events", 0.9, "a.go")}
	out := r.verifyItems(context.Background(), qpObj(), nil, in)
	if calls != 0 {
		t.Fatalf("mode off must make no calls, got %d", calls)
	}
	if len(out) != 1 {
		t.Fatalf("mode off must pass items through unchanged, got %d", len(out))
	}
}

// TestRunObjectiveVerifyGatedByHighVariance proves the verify pass fires only
// for HighVariance objectives: a low-variance objective makes the single
// discovery call, a high-variance one makes discovery + verify.
func TestRunObjectiveVerifyGatedByHighVariance(t *testing.T) {
	mk := func(obj objectives.Objective) int {
		calls := 0
		r := Runner{
			VerifyMode:    "reask",
			MinConfidence: 0.5,
			Prompt: promptStub(&calls, func(jobID string) (map[string]any, error) {
				// Return one matching item for whichever objective is asked.
				c := extraction.Candidate{Type: obj.Type, Name: "x", Confidence: 0.9,
					Locations: []extraction.Location{{File: "a.go", StartLine: 1}}}
				if obj.Type == "queue_publish" {
					c.Details = map[string]any{"platform": "sns", "queue": "q"}
				} else {
					c.Details = map[string]any{"method": "GET", "path": "/x"}
				}
				return itemsPayload(c), nil
			}),
		}
		if _, err := r.RunObjective(context.Background(), obj, nil); err != nil {
			t.Fatalf("RunObjective(%s): %v", obj.Type, err)
		}
		return calls
	}

	lowVar := objectives.Objective{ID: "exposure.http_route", Kind: model.KindExposure, Type: "http_route"}
	if n := mk(lowVar); n != 1 {
		t.Errorf("low-variance objective should make only the discovery call, got %d", n)
	}
	if n := mk(qpObj()); n != 2 {
		t.Errorf("high-variance objective should make discovery + verify, got %d", n)
	}
}

func TestVerifyDoubtedTagStable(t *testing.T) {
	// Guard the public-ish contract: the doubted tag string is what downstream
	// consumers filter on; keep it stable and namespaced.
	if !strings.HasPrefix(verifyDoubtedTag, "discovery_verify") {
		t.Fatalf("unexpected doubted tag %q", verifyDoubtedTag)
	}
}
