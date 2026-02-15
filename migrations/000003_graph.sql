CREATE TABLE IF NOT EXISTS graph_runs (
    id BIGSERIAL PRIMARY KEY,
    status TEXT NOT NULL,
    mode TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS graphs (
    graph_id TEXT PRIMARY KEY,
    graph_run_id BIGINT REFERENCES graph_runs(id) ON DELETE SET NULL,
    mode TEXT NOT NULL,
    generated_at TIMESTAMPTZ NOT NULL,
    artifact_path TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS graph_nodes (
    id BIGSERIAL PRIMARY KEY,
    graph_id TEXT NOT NULL REFERENCES graphs(graph_id) ON DELETE CASCADE,
    node_id TEXT NOT NULL,
    node_type TEXT NOT NULL,
    label TEXT NOT NULL,
    service_id TEXT,
    attributes JSONB NOT NULL DEFAULT '{}'::jsonb,
    confidence DOUBLE PRECISION NOT NULL,
    inferred BOOLEAN NOT NULL,
    UNIQUE (graph_id, node_id)
);

CREATE TABLE IF NOT EXISTS graph_edges (
    id BIGSERIAL PRIMARY KEY,
    graph_id TEXT NOT NULL REFERENCES graphs(graph_id) ON DELETE CASCADE,
    edge_id TEXT NOT NULL,
    edge_type TEXT NOT NULL,
    source_id TEXT NOT NULL,
    target_id TEXT NOT NULL,
    attributes JSONB NOT NULL DEFAULT '{}'::jsonb,
    confidence DOUBLE PRECISION NOT NULL,
    inferred BOOLEAN NOT NULL,
    UNIQUE (graph_id, edge_id)
);

CREATE TABLE IF NOT EXISTS graph_edge_evidence (
    id BIGSERIAL PRIMARY KEY,
    graph_id TEXT NOT NULL REFERENCES graphs(graph_id) ON DELETE CASCADE,
    edge_id TEXT NOT NULL,
    snapshot_key TEXT,
    file_path TEXT,
    start_line INTEGER NOT NULL DEFAULT 0,
    start_col INTEGER NOT NULL DEFAULT 0,
    end_line INTEGER NOT NULL DEFAULT 0,
    end_col INTEGER NOT NULL DEFAULT 0,
    fact_id TEXT,
    evidence_id TEXT
);

CREATE INDEX IF NOT EXISTS idx_graphs_generated ON graphs(generated_at DESC);
CREATE INDEX IF NOT EXISTS idx_graph_nodes_graph_type ON graph_nodes(graph_id, node_type);
CREATE INDEX IF NOT EXISTS idx_graph_edges_graph_type ON graph_edges(graph_id, edge_type);
CREATE INDEX IF NOT EXISTS idx_graph_edges_graph_source ON graph_edges(graph_id, source_id);
CREATE INDEX IF NOT EXISTS idx_graph_edges_graph_target ON graph_edges(graph_id, target_id);
CREATE INDEX IF NOT EXISTS idx_graph_edges_graph_inferred ON graph_edges(graph_id, inferred);
CREATE INDEX IF NOT EXISTS idx_graph_edge_evidence_graph_edge ON graph_edge_evidence(graph_id, edge_id);
