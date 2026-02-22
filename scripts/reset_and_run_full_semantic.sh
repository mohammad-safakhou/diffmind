#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

SOURCE_PATH="${1:-$ROOT_DIR/.codebases/checkout-service}"
OUT_DIR="${2:-$ROOT_DIR/.diffmind}"
REF="${REF:-HEAD}"

# Run with mandatory LSP adapters by default. Non-applicable adapters are skipped by analyzer policy.
ADAPTERS="${ADAPTERS:-builtin,gopls,tsserver,pyright,jdtls}"
EXTRACTORS="${EXTRACTORS:-runtime,endpoint,external_http,queue_db,config,ci_iac,dependency,semantic_model}"
ALLOW_MISSING_ADAPTERS="${ALLOW_MISSING_ADAPTERS:-false}"
OFFLINE="${OFFLINE:-true}"
LLM_AUGMENT="${LLM_AUGMENT:-false}"
SERVE="${SERVE:-false}"

run_step() {
  local step="$1"
  local label="$2"
  shift 2
  local started_at epoch_start epoch_end elapsed
  started_at="$(date -Iseconds)"
  epoch_start="$(date +%s)"
  echo "[$step/8] $label"
  echo "    started_at=$started_at"
  "$@"
  epoch_end="$(date +%s)"
  elapsed="$((epoch_end - epoch_start))"
  echo "    completed_in=${elapsed}s"
}

if [[ ! -d "$SOURCE_PATH" ]]; then
  echo "error: source path not found: $SOURCE_PATH" >&2
  exit 1
fi

run_step "1" "Reset output: $OUT_DIR" rm -rf "$OUT_DIR"

run_step "2" "Snapshot" go run ./cmd/extractor snapshot --source "$SOURCE_PATH" --ref "$REF" --out "$OUT_DIR"

run_step "3" "Scan" go run ./cmd/extractor scan --source "$SOURCE_PATH" --out "$OUT_DIR"

run_step "4" "Parse" go run ./cmd/extractor parse --source "$SOURCE_PATH" --out "$OUT_DIR"

echo "[5/8] Analyze (adapters: $ADAPTERS)"
echo "    started_at=$(date -Iseconds)"
analyze_started="$(date +%s)"
ANALYZE_ARGS=(
  --source "$SOURCE_PATH"
  --out "$OUT_DIR"
  --adapters "$ADAPTERS"
  --extractors "$EXTRACTORS"
  --offline="$OFFLINE"
)
if [[ "$ALLOW_MISSING_ADAPTERS" == "true" ]]; then
  ANALYZE_ARGS+=(--allow-missing-adapters)
fi
if [[ "$LLM_AUGMENT" == "true" ]]; then
  ANALYZE_ARGS+=(--llm-augment)
fi
go run ./cmd/extractor analyze "${ANALYZE_ARGS[@]}"
echo "    completed_in=$(( $(date +%s) - analyze_started ))s"

echo "[6/8] Bundle + Verify"
echo "    started_at=$(date -Iseconds)"
bundle_started="$(date +%s)"
go run ./cmd/extractor bundle --in "$OUT_DIR/analyzers/bundle.json" --out "$OUT_DIR"
go run ./cmd/extractor verify \
  --in "$OUT_DIR/bundle/intelligence_bundle.json" \
  --out "$OUT_DIR" \
  --out-bundle "$OUT_DIR/bundle/intelligence_bundle.json"
echo "    completed_in=$(( $(date +%s) - bundle_started ))s"

run_step "7" "Build graph" go run ./cmd/extractor graph build --sources "$OUT_DIR" --out "$OUT_DIR"

echo "[8/8] Validate outputs"
echo "    started_at=$(date -Iseconds)"
validate_started="$(date +%s)"
INDEX_PATH="$OUT_DIR/graph/index.json"
GRAPH_PATH="$(jq -r '.graphs[0].path // empty' "$INDEX_PATH")"
jq '{facts_count,evidence_count,adapters,adapter_plan}' "$OUT_DIR/analyzers/report.json"
jq '{
  node_count:.stats.node_count,
  edge_count:.stats.edge_count,
  by_node_type:.stats.by_node_type,
  by_edge_type:.stats.by_edge_type
}' "$GRAPH_PATH"
echo "    completed_in=$(( $(date +%s) - validate_started ))s"

if [[ "$SERVE" == "true" ]]; then
  echo "Starting API server on :8080"
  exec go run ./cmd/extractor serve --addr :8080 --bundle "$OUT_DIR/bundle/intelligence_bundle.json" --graph-root "$OUT_DIR/graph"
fi

echo "Done. graph_path=$GRAPH_PATH"
