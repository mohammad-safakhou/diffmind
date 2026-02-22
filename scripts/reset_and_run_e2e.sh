#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

SOURCE_PATH="${1:-$ROOT_DIR/.codebases/checkout-service}"
OUT_DIR="${2:-$ROOT_DIR/.diffmind}"
REF="${REF:-HEAD}"
SERVE="${SERVE:-false}"

if [[ ! -d "$SOURCE_PATH" ]]; then
  echo "error: source path not found: $SOURCE_PATH" >&2
  exit 1
fi

echo "[1/5] Reset output: $OUT_DIR"
rm -rf "$OUT_DIR"

echo "[2/5] Run extractor pipeline"
go run ./cmd/extractor run \
  --source "$SOURCE_PATH" \
  --ref "$REF" \
  --out "$OUT_DIR" \
  --no-resume

echo "[3/5] Build graph"
go run ./cmd/extractor graph build --sources "$OUT_DIR" --out "$OUT_DIR"

INDEX_PATH="$OUT_DIR/graph/index.json"
if [[ ! -f "$INDEX_PATH" ]]; then
  echo "error: graph index not found: $INDEX_PATH" >&2
  exit 1
fi

GRAPH_PATH="$(jq -r '.graphs[0].path // empty' "$INDEX_PATH")"
if [[ -z "$GRAPH_PATH" || ! -f "$GRAPH_PATH" ]]; then
  echo "error: graph file not found from index: $GRAPH_PATH" >&2
  exit 1
fi

echo "[4/5] Validate key outputs"
jq '{status,stages:[.stages[]|{name,status,attempts}]}' "$OUT_DIR/run/report.json"
jq '{
  node_count:.stats.node_count,
  edge_count:.stats.edge_count,
  by_node_type:.stats.by_node_type,
  by_edge_type:.stats.by_edge_type
}' "$GRAPH_PATH"
echo "graph_path=$GRAPH_PATH"

echo "[5/5] Surface checks"
echo "- endpoint methods:"
jq -r '.nodes[] | select(.type=="endpoint") | (.attributes.method // "UNKNOWN")' "$GRAPH_PATH" | sort | uniq -c
echo "- queue nodes:"
jq -r '.nodes[] | select(.type=="queue") | .label' "$GRAPH_PATH" | sort
echo "- key dependency operations:"
jq -r '.nodes[] | select(.type=="dependency_operation") | .label' "$GRAPH_PATH" \
  | rg 'PUBLISH|CONSUME|static-brochure-import|notifications|publisher-api|sales-force|order-info|content-qa|content-store|advendio' \
  | sort || true

if [[ "$SERVE" == "true" ]]; then
  echo "Starting API server on :8080"
  exec go run ./cmd/extractor serve --addr :8080 --bundle "$OUT_DIR/bundle/intelligence_bundle.json" --graph-root "$OUT_DIR/graph"
fi

echo "Done."
