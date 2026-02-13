CREATE TABLE IF NOT EXISTS runs (
    id BIGSERIAL PRIMARY KEY,
    run_type TEXT NOT NULL,
    status TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS snapshots (
    id BIGSERIAL PRIMARY KEY,
    run_id BIGINT REFERENCES runs(id),
    repo_locator TEXT NOT NULL,
    git_ref TEXT NOT NULL,
    commit_sha TEXT,
    snapshot_key TEXT UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS file_inventory (
    id BIGSERIAL PRIMARY KEY,
    snapshot_id BIGINT NOT NULL REFERENCES snapshots(id) ON DELETE CASCADE,
    path TEXT NOT NULL,
    size_bytes BIGINT NOT NULL,
    sha256 TEXT NOT NULL,
    file_type TEXT,
    classification TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (snapshot_id, path)
);

CREATE INDEX IF NOT EXISTS idx_snapshots_repo_ref ON snapshots(repo_locator, git_ref);
CREATE INDEX IF NOT EXISTS idx_file_inventory_snapshot ON file_inventory(snapshot_id);
CREATE INDEX IF NOT EXISTS idx_file_inventory_sha256 ON file_inventory(sha256);
