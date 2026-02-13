CREATE TABLE IF NOT EXISTS evidences (
    id TEXT PRIMARY KEY,
    snapshot_key TEXT NOT NULL,
    file_path TEXT NOT NULL,
    start_line INTEGER NOT NULL,
    start_col INTEGER NOT NULL,
    end_line INTEGER NOT NULL,
    end_col INTEGER NOT NULL,
    snippet_hash TEXT NOT NULL,
    ast_node_id TEXT,
    query_name TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_evidence_lines CHECK (start_line >= 1 AND end_line >= 1),
    CONSTRAINT chk_evidence_cols CHECK (start_col >= 1 AND end_col >= 1),
    CONSTRAINT chk_evidence_span CHECK (
        end_line > start_line OR (end_line = start_line AND end_col >= start_col)
    )
);

CREATE TABLE IF NOT EXISTS facts (
    id TEXT PRIMARY KEY,
    snapshot_key TEXT NOT NULL,
    fact_type TEXT NOT NULL,
    attributes JSONB NOT NULL DEFAULT '{}'::jsonb,
    confidence DOUBLE PRECISION NOT NULL,
    analyzer_id TEXT NOT NULL,
    analyzer_version TEXT NOT NULL,
    deterministic BOOLEAN NOT NULL,
    inferred BOOLEAN NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_fact_confidence CHECK (confidence >= 0 AND confidence <= 1)
);

CREATE TABLE IF NOT EXISTS fact_evidence (
    fact_id TEXT NOT NULL REFERENCES facts(id) ON DELETE CASCADE,
    evidence_id TEXT NOT NULL REFERENCES evidences(id) ON DELETE CASCADE,
    PRIMARY KEY (fact_id, evidence_id)
);

CREATE INDEX IF NOT EXISTS idx_evidences_snapshot ON evidences(snapshot_key);
CREATE INDEX IF NOT EXISTS idx_evidences_file ON evidences(file_path);
CREATE INDEX IF NOT EXISTS idx_facts_snapshot ON facts(snapshot_key);
CREATE INDEX IF NOT EXISTS idx_facts_type ON facts(fact_type);
CREATE INDEX IF NOT EXISTS idx_facts_attributes_gin ON facts USING GIN (attributes);
CREATE INDEX IF NOT EXISTS idx_fact_evidence_evidence ON fact_evidence(evidence_id);
