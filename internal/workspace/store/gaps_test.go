package store

import "testing"

func TestGapLifecycleIsRevisionedAndGuarded(t *testing.T) {
	st, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	project, err := st.CreateProject(Project{Name: "Gap lifecycle"})
	if err != nil {
		t.Fatal(err)
	}
	collection, gap, err := st.CreateGap(project.ID, 0, Gap{Category: "missing_fact", Expected: "catalog", Observed: "unknown", Author: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	if collection.Revision != 1 || gap.Status != "observed" || gap.ID == "" {
		t.Fatalf("created=%+v collection=%+v", gap, collection)
	}
	if _, _, err := st.CreateGap(project.ID, 0, Gap{Category: "missing_fact", Expected: "x", Observed: "y"}); err == nil {
		t.Fatal("stale revision accepted")
	}
	if _, _, err := st.TransitionGap(project.ID, gap.ID, 1, "active", "agent", "skip review", nil); err == nil {
		t.Fatal("invalid transition accepted")
	}
	states := []string{"reproduced", "proposed", "tested", "accepted", "active", "rolled_back"}
	revision := 1
	for _, state := range states {
		collection, gap, err = st.TransitionGap(project.ID, gap.ID, revision, state, "reviewer", state, []string{"TestFixture"})
		if err != nil {
			t.Fatalf("%s: %v", state, err)
		}
		revision = collection.Revision
	}
	if gap.Status != "rolled_back" || len(gap.History) != 7 {
		t.Fatalf("gap=%+v", gap)
	}
	loaded, err := st.ListGaps(project.ID)
	if err != nil || loaded.Revision != revision || len(loaded.Gaps) != 1 {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}
}

func TestGapFingerprintSuppressesDuplicateObservation(t *testing.T) {
	st, _ := New(t.TempDir())
	project, _ := st.CreateProject(Project{Name: "Dedupe"})
	input := Gap{Category: "incorrect_fact", RepositoryID: "api", RunID: "run-one", ObjectID: "call.one", Expected: "unresolved", Observed: "external service"}
	_, _, err := st.CreateGap(project.ID, 0, input)
	if err != nil {
		t.Fatal(err)
	}
	input.RunID = "run-two"
	if _, _, err := st.CreateGap(project.ID, 1, input); err == nil {
		t.Fatal("duplicate fingerprint from a later run accepted")
	}
}
