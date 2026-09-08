package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Gap records a concrete mismatch between observed and expected architecture
// facts. It stores source identities and review state, never source contents.
type Gap struct {
	ID              string          `json:"id"`
	Category        string          `json:"category"`
	RepositoryID    string          `json:"repository_id,omitempty"`
	RunID           string          `json:"run_id,omitempty"`
	ObjectID        string          `json:"object_id,omitempty"`
	Expected        string          `json:"expected"`
	Observed        string          `json:"observed"`
	SourcePointers  []string        `json:"source_pointers,omitempty"`
	DetectorVersion string          `json:"detector_version,omitempty"`
	PackVersion     string          `json:"pack_version,omitempty"`
	Status          string          `json:"status"`
	Tests           []string        `json:"tests,omitempty"`
	Author          string          `json:"author,omitempty"`
	Reviewer        string          `json:"reviewer,omitempty"`
	Reason          string          `json:"reason,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
	History         []GapTransition `json:"history,omitempty"`
}

type GapTransition struct {
	From   string    `json:"from,omitempty"`
	To     string    `json:"to"`
	At     time.Time `json:"at"`
	Actor  string    `json:"actor,omitempty"`
	Reason string    `json:"reason,omitempty"`
}

type GapCollection struct {
	Version  int   `json:"version"`
	Revision int   `json:"revision"`
	Gaps     []Gap `json:"gaps"`
}

func (s *Store) gapsPath(pid string) string {
	return filepath.Join(s.projectDir(pid), "improvement-gaps.json")
}

func (s *Store) ListGaps(pid string) (*GapCollection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadGaps(pid)
}

func (s *Store) CreateGap(pid string, expectedRevision int, gap Gap) (*GapCollection, *Gap, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	collection, err := s.loadGaps(pid)
	if err != nil {
		return nil, nil, err
	}
	if collection.Revision != expectedRevision {
		return nil, nil, fmt.Errorf("%w: gap records changed; reload before saving", ErrConflict)
	}
	gap.Category = strings.TrimSpace(gap.Category)
	gap.Expected, gap.Observed = strings.TrimSpace(gap.Expected), strings.TrimSpace(gap.Observed)
	if gap.Category == "" || gap.Expected == "" || gap.Observed == "" {
		return nil, nil, errors.New("category, expected, and observed are required")
	}
	if len(gap.Category) > 64 || len(gap.Expected) > 4096 || len(gap.Observed) > 4096 || len(gap.SourcePointers) > 50 {
		return nil, nil, errors.New("gap fields exceed configured limits")
	}
	if len(collection.Gaps) >= 10000 {
		return nil, nil, errors.New("gap record limit reached")
	}
	if gap.ID == "" {
		gap.ID = gapFingerprint(pid, gap)
	}
	if !validID(gap.ID) {
		return nil, nil, errors.New("invalid gap id")
	}
	for i := range collection.Gaps {
		if collection.Gaps[i].ID == gap.ID {
			return nil, nil, fmt.Errorf("%w: gap %s already recorded", ErrConflict, gap.ID)
		}
	}
	now := time.Now().UTC()
	gap.Status, gap.CreatedAt, gap.UpdatedAt = "observed", now, now
	gap.History = []GapTransition{{To: "observed", At: now, Actor: gap.Author, Reason: gap.Reason}}
	collection.Gaps = append(collection.Gaps, gap)
	collection.Revision++
	sort.Slice(collection.Gaps, func(i, j int) bool { return collection.Gaps[i].ID < collection.Gaps[j].ID })
	if err := writeJSON(s.gapsPath(pid), collection); err != nil {
		return nil, nil, err
	}
	return collection, &gap, nil
}

func (s *Store) TransitionGap(pid, id string, expectedRevision int, status, actor, reason string, tests []string) (*GapCollection, *Gap, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	collection, err := s.loadGaps(pid)
	if err != nil {
		return nil, nil, err
	}
	if collection.Revision != expectedRevision {
		return nil, nil, fmt.Errorf("%w: gap records changed; reload before saving", ErrConflict)
	}
	status, actor, reason = strings.TrimSpace(status), strings.TrimSpace(actor), strings.TrimSpace(reason)
	if (status == "rejected" || status == "deferred" || status == "superseded" || status == "rolled_back") && reason == "" {
		return nil, nil, errors.New("a reason is required for terminal or rollback transitions")
	}
	if len(reason) > 4096 || len(tests) > 100 {
		return nil, nil, errors.New("transition fields exceed configured limits")
	}
	for i := range collection.Gaps {
		gap := &collection.Gaps[i]
		if gap.ID != id {
			continue
		}
		if !validGapTransition(gap.Status, status) {
			return nil, nil, fmt.Errorf("invalid gap transition %s -> %s", gap.Status, status)
		}
		now := time.Now().UTC()
		gap.History = append(gap.History, GapTransition{From: gap.Status, To: status, At: now, Actor: actor, Reason: reason})
		gap.Status, gap.UpdatedAt, gap.Reason = status, now, reason
		if len(tests) > 0 {
			gap.Tests = append([]string(nil), tests...)
		}
		if status == "accepted" || status == "active" {
			gap.Reviewer = actor
		}
		collection.Revision++
		if err := writeJSON(s.gapsPath(pid), collection); err != nil {
			return nil, nil, err
		}
		copy := *gap
		return collection, &copy, nil
	}
	return nil, nil, ErrNotFound
}

func (s *Store) loadGaps(pid string) (*GapCollection, error) {
	if !validID(pid) {
		return nil, ErrNotFound
	}
	if _, err := s.GetProject(pid); err != nil {
		return nil, err
	}
	body, err := os.ReadFile(s.gapsPath(pid))
	if errors.Is(err, os.ErrNotExist) {
		return &GapCollection{Version: 1, Gaps: []Gap{}}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(body) > 4<<20 {
		return nil, errors.New("gap records exceed 4 MiB")
	}
	var out GapCollection
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	if out.Version != 1 || out.Revision < 0 {
		return nil, errors.New("invalid gap record version or revision")
	}
	if out.Gaps == nil {
		out.Gaps = []Gap{}
	}
	return &out, nil
}

func gapFingerprint(pid string, gap Gap) string {
	// A run is observation context, not gap identity. Omitting it prevents an
	// unchanged detector problem from being proposed again after every rebuild.
	sum := sha256.Sum256([]byte(strings.Join([]string{pid, gap.Category, gap.RepositoryID, gap.ObjectID, gap.Expected, gap.Observed}, "\x00")))
	return "gap-" + hex.EncodeToString(sum[:8])
}

func validGapTransition(from, to string) bool {
	if to == "rejected" || to == "deferred" || to == "superseded" {
		return from != "active"
	}
	allowed := map[string]string{"observed": "reproduced", "reproduced": "proposed", "proposed": "tested", "tested": "accepted", "accepted": "active", "active": "rolled_back"}
	return allowed[from] == to
}
