#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GOCACHE_DIR="${GOCACHE:-${ROOT_DIR}/.gocache}"

SUMMARY_GLOB="${ROOT_DIR}/.diffmind/release-gate-m6-history/*/summary.json"
OUT_PATH="${ROOT_DIR}/quality/source_baselines.e2e.json"
MIN_SAMPLES=2
INCLUDE_FIXTURES="false"

usage() {
  cat <<USAGE
Usage: $(basename "$0") [options]

Options:
  --summary-glob <glob>      Glob pattern for release-gate summary files.
  --out <path>               Output calibrated baseline policy path.
  --min-samples <count>      Minimum runs per source (default: 2).
  --include-fixtures <bool>  Include fixture runs in calibration (default: false).
  -h|--help                  Show this help.
USAGE
}

bool_normalize() {
  case "$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')" in
    true|1|yes|y) printf 'true' ;;
    false|0|no|n) printf 'false' ;;
    *) echo "invalid boolean: $1" >&2; exit 1 ;;
  esac
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --summary-glob)
      SUMMARY_GLOB="$2"
      shift 2
      ;;
    --out)
      OUT_PATH="$2"
      shift 2
      ;;
    --min-samples)
      MIN_SAMPLES="$2"
      shift 2
      ;;
    --include-fixtures)
      INCLUDE_FIXTURES="$(bool_normalize "$2")"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      exit 1
      ;;
  esac
done

if ! [[ "${MIN_SAMPLES}" =~ ^[0-9]+$ ]]; then
  echo "--min-samples must be an integer" >&2
  exit 1
fi

shopt -s nullglob
summary_paths=( ${SUMMARY_GLOB} )
shopt -u nullglob
if [[ ${#summary_paths[@]} -eq 0 ]]; then
  echo "no summary files matched: ${SUMMARY_GLOB}" >&2
  exit 1
fi

summary_csv="$(IFS=, ; echo "${summary_paths[*]}")"

args=(
  go run ./cmd/extractor quality calibrate-baselines
  --summary "${summary_paths[0]}"
  --summaries "${summary_csv}"
  --out "${OUT_PATH}"
  --min-samples "${MIN_SAMPLES}"
)
if [[ "${INCLUDE_FIXTURES}" == "true" ]]; then
  args+=(--include-fixtures)
fi

echo "[calibrate-baselines] summaries=${#summary_paths[@]} out=${OUT_PATH}"
(
  cd "${ROOT_DIR}"
  GOCACHE="${GOCACHE_DIR}" "${args[@]}"
)

