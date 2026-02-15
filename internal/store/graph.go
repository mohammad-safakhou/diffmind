package store

import (
	"context"
	"database/sql"
	"fmt"

	"diffmind/internal/graphschema"
)

type GraphStore struct {
	db *sql.DB
}

func NewGraphStore(db *sql.DB) *GraphStore {
	return &GraphStore{db: db}
}

func (s *GraphStore) PersistGraph(ctx context.Context, graph graphschema.Graph, artifactPath string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin graph tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var runID int64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO graph_runs (status, mode)
		VALUES ($1, $2)
		RETURNING id
	`, "completed", graph.Mode).Scan(&runID); err != nil {
		return fmt.Errorf("insert graph_run: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO graphs (graph_id, graph_run_id, mode, generated_at, artifact_path)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (graph_id) DO UPDATE SET
			graph_run_id = EXCLUDED.graph_run_id,
			mode = EXCLUDED.mode,
			generated_at = EXCLUDED.generated_at,
			artifact_path = EXCLUDED.artifact_path
	`, graph.GraphID, runID, graph.Mode, graph.GeneratedAt, artifactPath); err != nil {
		return fmt.Errorf("upsert graph: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM graph_edge_evidence WHERE graph_id = $1`, graph.GraphID); err != nil {
		return fmt.Errorf("clear graph_edge_evidence: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM graph_edges WHERE graph_id = $1`, graph.GraphID); err != nil {
		return fmt.Errorf("clear graph_edges: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM graph_nodes WHERE graph_id = $1`, graph.GraphID); err != nil {
		return fmt.Errorf("clear graph_nodes: %w", err)
	}

	for _, n := range graph.Nodes {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO graph_nodes (graph_id, node_id, node_type, label, service_id, attributes, confidence, inferred)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		`, graph.GraphID, n.ID, n.Type, n.Label, nullIfEmpty(n.ServiceID), toJSONB(n.Attributes), n.Confidence, n.Inferred); err != nil {
			return fmt.Errorf("insert graph node %s: %w", n.ID, err)
		}
	}

	for _, e := range graph.Edges {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO graph_edges (graph_id, edge_id, edge_type, source_id, target_id, attributes, confidence, inferred)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		`, graph.GraphID, e.ID, e.Type, e.SourceID, e.TargetID, toJSONB(e.Attributes), e.Confidence, e.Inferred); err != nil {
			return fmt.Errorf("insert graph edge %s: %w", e.ID, err)
		}
		for _, ref := range e.EvidenceRefs {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO graph_edge_evidence (
					graph_id, edge_id, snapshot_key, file_path, start_line, start_col, end_line, end_col, fact_id, evidence_id
				) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
			`, graph.GraphID, e.ID, nullIfEmpty(ref.SnapshotID), nullIfEmpty(ref.FilePath), ref.StartLine, ref.StartCol, ref.EndLine, ref.EndCol, nullIfEmpty(ref.FactID), nullIfEmpty(ref.EvidenceID)); err != nil {
				return fmt.Errorf("insert graph edge evidence %s: %w", e.ID, err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit graph tx: %w", err)
	}
	return nil
}
