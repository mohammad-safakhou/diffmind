package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"diffmind/internal/facts"
)

type FactStore struct {
	db *sql.DB
}

func NewFactStore(db *sql.DB) *FactStore {
	return &FactStore{db: db}
}

func (s *FactStore) PersistBundle(ctx context.Context, bundle facts.Bundle) error {
	if err := facts.ValidateBundle(bundle); err != nil {
		return fmt.Errorf("validate fact bundle: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, ev := range bundle.Evidence {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO evidences (
				id, snapshot_key, file_path, start_line, start_col, end_line, end_col,
				snippet_hash, ast_node_id, query_name, created_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
			ON CONFLICT (id) DO UPDATE SET
				snapshot_key = EXCLUDED.snapshot_key,
				file_path = EXCLUDED.file_path,
				start_line = EXCLUDED.start_line,
				start_col = EXCLUDED.start_col,
				end_line = EXCLUDED.end_line,
				end_col = EXCLUDED.end_col,
				snippet_hash = EXCLUDED.snippet_hash,
				ast_node_id = EXCLUDED.ast_node_id,
				query_name = EXCLUDED.query_name
		`, ev.ID, ev.SnapshotID, ev.FilePath, ev.StartLine, ev.StartCol, ev.EndLine, ev.EndCol, ev.SnippetHash,
			nullIfEmpty(ev.ASTNodeID), nullIfEmpty(ev.QueryName), ev.CreatedAtUTC,
		); err != nil {
			return fmt.Errorf("upsert evidence %q: %w", ev.ID, err)
		}
	}

	for _, fact := range bundle.Facts {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO facts (
				id, snapshot_key, fact_type, attributes, confidence,
				analyzer_id, analyzer_version, deterministic, inferred, created_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
			ON CONFLICT (id) DO UPDATE SET
				snapshot_key = EXCLUDED.snapshot_key,
				fact_type = EXCLUDED.fact_type,
				attributes = EXCLUDED.attributes,
				confidence = EXCLUDED.confidence,
				analyzer_id = EXCLUDED.analyzer_id,
				analyzer_version = EXCLUDED.analyzer_version,
				deterministic = EXCLUDED.deterministic,
				inferred = EXCLUDED.inferred
		`, fact.ID, pickSnapshot(fact.EvidenceIDs, bundle.Evidence), fact.Type, toJSONB(fact.Attributes), fact.Confidence,
			fact.Provenance.AnalyzerID, fact.Provenance.AnalyzerVersion, fact.Provenance.Deterministic,
			fact.Provenance.Inferred, fact.CreatedAtUTC,
		); err != nil {
			return fmt.Errorf("upsert fact %q: %w", fact.ID, err)
		}

		if _, err := tx.ExecContext(ctx, `DELETE FROM fact_evidence WHERE fact_id = $1`, fact.ID); err != nil {
			return fmt.Errorf("clear fact_evidence links for fact %q: %w", fact.ID, err)
		}
		for _, evidenceID := range fact.EvidenceIDs {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO fact_evidence (fact_id, evidence_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`,
				fact.ID, evidenceID,
			); err != nil {
				return fmt.Errorf("insert fact_evidence %q->%q: %w", fact.ID, evidenceID, err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

func pickSnapshot(evidenceIDs []string, evidence []facts.Evidence) string {
	for _, id := range evidenceIDs {
		for _, ev := range evidence {
			if ev.ID == id {
				return ev.SnapshotID
			}
		}
	}
	return ""
}

func toJSONB(value map[string]any) any {
	if value == nil {
		return []byte("{}")
	}
	data, err := json.Marshal(value)
	if err != nil {
		return []byte("{}")
	}
	return data
}
