// Package eval is diffmind's golden-set accuracy harness. It scores the
// extractor's output (exposures / dependencies / connections) against
// hand-labeled ground truth, computing per-objective and overall
// precision/recall/F1. Matching uses the SAME architectural-identity keys the
// pipeline dedups with (see internal/reconcile.SemanticKeyLoose), so LLM
// phrasing differences ("orders" vs "order", "SELECT" vs "read") never count as
// misses. The harness has two modes: a hermetic deterministic-floor mode
// (RunCheap, no LLM) for CI, and a full-pipeline mode for measured accuracy +
// variance over K live runs.
package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mohammad-safakhou/diffmind/internal/model"
)

// ExpectedEntity is one ground-truth exposure or dependency. A human labels
// only the architectural-identity fields the semantic key consumes (type, plus
// the 2-3 fields per type — e.g. details.method/path for http_route,
// details.table/operation for db_operation); everything else is ignored by the
// matcher. Deterministic marks whether the deterministic floor is EXPECTED to
// recover this item: cheap-mode scoring only counts deterministic:true items,
// so a non-JVM db op (LLM-only) is not charged as a floor miss.
type ExpectedEntity struct {
	Type          string         `json:"type"`
	Name          string         `json:"name,omitempty"`
	Platform      string         `json:"platform,omitempty"`
	Instance      string         `json:"instance,omitempty"`
	Operation     string         `json:"operation,omitempty"`
	Details       map[string]any `json:"details,omitempty"`
	Deterministic bool           `json:"deterministic"`
}

func (e ExpectedEntity) toBase() model.BaseEntity {
	return model.BaseEntity{
		Type:      e.Type,
		Name:      e.Name,
		Platform:  e.Platform,
		Instance:  e.Instance,
		Operation: e.Operation,
		Details:   e.Details,
	}
}

// ExpectedConnection is a ground-truth exposure→dependency edge, labeled by the
// identity of its endpoints (not by opaque hash IDs, which differ per run).
type ExpectedConnection struct {
	From          ExpectedEntity `json:"from"`
	To            ExpectedEntity `json:"to"`
	Deterministic bool           `json:"deterministic"`
}

// ExpectedSet is the full label for one fixture repo.
type ExpectedSet struct {
	Fixture      string               `json:"fixture"`
	RepoPath     string               `json:"repo_path"` // relative to the label dir, or absolute
	ServiceName  string               `json:"service_name,omitempty"`
	Exposures    []ExpectedEntity     `json:"exposures"`
	Dependencies []ExpectedEntity     `json:"dependencies"`
	Connections  []ExpectedConnection `json:"connections"`

	// Dir is the directory expected.json was loaded from. Not serialized;
	// used to resolve RepoPath. Set by LoadExpected.
	Dir string `json:"-"`
}

// ResolvedRepoPath returns RepoPath resolved against the label directory.
func (s ExpectedSet) ResolvedRepoPath() string {
	if filepath.IsAbs(s.RepoPath) {
		return s.RepoPath
	}
	return filepath.Clean(filepath.Join(s.Dir, s.RepoPath))
}

// LoadExpected reads and validates <dir>/expected.json.
func LoadExpected(dir string) (ExpectedSet, error) {
	b, err := os.ReadFile(filepath.Join(dir, "expected.json"))
	if err != nil {
		return ExpectedSet{}, fmt.Errorf("read expected.json in %s: %w", dir, err)
	}
	var set ExpectedSet
	if err := json.Unmarshal(b, &set); err != nil {
		return ExpectedSet{}, fmt.Errorf("parse expected.json in %s: %w", dir, err)
	}
	set.Dir = dir
	if set.Fixture == "" {
		set.Fixture = filepath.Base(dir)
	}
	if set.RepoPath == "" {
		return ExpectedSet{}, fmt.Errorf("%s: expected.json missing repo_path", dir)
	}
	for i, e := range set.Exposures {
		if e.Type == "" {
			return ExpectedSet{}, fmt.Errorf("%s: exposures[%d] missing type", dir, i)
		}
	}
	for i, e := range set.Dependencies {
		if e.Type == "" {
			return ExpectedSet{}, fmt.Errorf("%s: dependencies[%d] missing type", dir, i)
		}
	}
	for i, c := range set.Connections {
		if c.From.Type == "" || c.To.Type == "" {
			return ExpectedSet{}, fmt.Errorf("%s: connections[%d] missing from/to type", dir, i)
		}
	}
	return set, nil
}
