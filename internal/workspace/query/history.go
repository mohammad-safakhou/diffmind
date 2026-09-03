package query

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/archgraph"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/store"
)

type GraphRun struct {
	ID             string    `json:"id"`
	Status         string    `json:"status"`
	StartedAt      time.Time `json:"started_at"`
	FinishedAt     time.Time `json:"finished_at"`
	GraphAvailable bool      `json:"graph_available"`
	ServiceCount   int       `json:"service_count"`
	EdgeCount      int       `json:"edge_count"`
	PackSetDigest  string    `json:"pack_set_digest,omitempty"`
}

type RunsResult struct {
	ProjectID  string     `json:"project_id"`
	Runs       []GraphRun `json:"runs"`
	Total      int        `json:"total"`
	NextOffset *int       `json:"next_offset,omitempty"`
}

func pageBounds(offset, limit, total int) (int, int, *int, error) {
	if offset < 0 || limit < 0 || limit > 500 {
		return 0, 0, nil, fmt.Errorf("offset must be nonnegative and limit between 1 and 500 (0 uses default)")
	}
	if limit == 0 {
		limit = 100
	}
	start := min(offset, total)
	end := start + min(limit, total-start)
	var next *int
	if end < total {
		next = &end
	}
	return start, end, next, nil
}

func (s *Service) GraphRuns(projectID string, offset, limit int) (*RunsResult, error) {
	if _, _, _, err := pageBounds(offset, limit, 0); err != nil {
		return nil, err
	}
	p, err := s.ResolveProject(projectID)
	if err != nil {
		return nil, err
	}
	runs, err := s.store.ListRuns(p.ID)
	if err != nil {
		return nil, err
	}
	sort.Slice(runs, func(i, j int) bool {
		if !runs[i].StartedAt.Equal(runs[j].StartedAt) {
			return runs[i].StartedAt.After(runs[j].StartedAt)
		}
		return runs[i].ID > runs[j].ID
	})
	start, end, next, _ := pageBounds(offset, limit, len(runs))
	out := &RunsResult{ProjectID: p.ID, Runs: []GraphRun{}, Total: len(runs), NextOffset: next}
	for _, run := range runs[start:end] {
		item := graphRun(run)
		info, err := os.Stat(filepath.Join(s.store.RunDir(p.ID, run.ID), "graph.json"))
		item.GraphAvailable = run.Status == store.RunCompleted && err == nil && info.Mode().IsRegular()
		out.Runs = append(out.Runs, item)
	}
	return out, nil
}

func graphRun(run store.RunManifest) GraphRun {
	digest, _ := run.Options["knowledge_pack_set_digest"].(string)
	return GraphRun{ID: run.ID, Status: run.Status, StartedAt: run.StartedAt, FinishedAt: run.FinishedAt, ServiceCount: run.ServiceCount, EdgeCount: run.EdgeCount, PackSetDigest: digest}
}

type Comparison struct {
	ProjectID                  string             `json:"project_id"`
	From                       GraphRun           `json:"from"`
	To                         GraphRun           `json:"to"`
	Counts                     map[string]int     `json:"counts"`
	Changes                    []archgraph.Change `json:"changes"`
	Total                      int                `json:"total"`
	NextOffset                 *int               `json:"next_offset,omitempty"`
	RepositoryArtifactsChanged []string           `json:"repository_artifacts_changed"`
	Notes                      []string           `json:"notes"`
}

func (s *Service) CompareGraphs(ctx context.Context, projectID, from, to string, offset, limit int) (*Comparison, error) {
	if strings.TrimSpace(from) == "" || strings.TrimSpace(to) == "" {
		return nil, fmt.Errorf("from and to completed run IDs are required")
	}
	if _, _, _, err := pageBounds(offset, limit, 0); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p, err := s.ResolveProject(projectID)
	if err != nil {
		return nil, err
	}
	a, left, err := s.loadGraph(p.ID, from)
	if err != nil {
		return nil, err
	}
	b, right, err := s.loadGraph(p.ID, to)
	if err != nil {
		return nil, err
	}
	changes, err := archgraph.Compare(ctx, left, right)
	if err != nil {
		return nil, err
	}
	start, end, next, _ := pageBounds(offset, limit, len(changes))
	out := &Comparison{ProjectID: p.ID, From: graphRun(*a), To: graphRun(*b), Counts: map[string]int{"added": 0, "removed": 0, "modified": 0}, Changes: changes[start:end], Total: len(changes), NextOffset: next, RepositoryArtifactsChanged: []string{}, Notes: []string{
		"Compares saved graph facts, not runtime traffic or source diffs. Changes do not establish their cause.",
		"Top-level generated object IDs, layout, counts, checkout paths and freshness are ignored. Nested evidence and flow content are retained; changed evidence can produce a modified fact.",
	}}
	out.From.GraphAvailable = true
	out.To.GraphAvailable = true
	for _, change := range changes {
		out.Counts[change.Change]++
	}
	refs := map[string]string{}
	other := map[string]string{}
	for _, ref := range a.Repos {
		refs[ref.RepoID] = ref.DiffMindRunID
	}
	for _, ref := range b.Repos {
		other[ref.RepoID] = ref.DiffMindRunID
	}
	ids := map[string]bool{}
	for id := range refs {
		ids[id] = true
	}
	for id := range other {
		ids[id] = true
	}
	for id := range ids {
		av, aok := refs[id]
		bv, bok := other[id]
		if av != bv || aok != bok {
			out.RepositoryArtifactsChanged = append(out.RepositoryArtifactsChanged, id)
		}
	}
	sort.Strings(out.RepositoryArtifactsChanged)
	if out.From.PackSetDigest != out.To.PackSetDigest {
		out.Notes = append(out.Notes, "The recorded knowledge-pack set differs (or is missing in one run); this is context, not proof that packs caused a particular change.")
	}
	return out, nil
}
