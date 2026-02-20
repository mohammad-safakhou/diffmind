#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GOCACHE_DIR="${GOCACHE:-${ROOT_DIR}/.gocache}"

SOURCE=""
REF="HEAD"
OUT_ROOT="${ROOT_DIR}/.diffmind/e2e-m8"
SERVICE_ID=""
SERVICE_NAME=""
EXPECT_LINKS=""
GENERATE_EXPECT_LINKS="true"
RUN_CLEAN="false"
SKIP_EXTRACT="false"
EXTRACT_MODE="stages"
QUALITY_POLICY="${ROOT_DIR}/quality/policy.e2e.json"

usage() {
  cat <<USAGE
Usage: $(basename "$0") --source <repo_path> [options]

Required:
  --source <path>                 Repository path to analyze.

Options:
  --ref <git-ref>                 Git ref for extractor run (default: HEAD).
  --out <path>                    Output root for this E2E run (default: ${OUT_ROOT}).
  --service-id <id>               Service ID for single-graph build (default: source basename).
  --service-name <name>           Service display name (default: service-id).
  --expect-links <path>           Expected-links JSON for benchmark mode.
  --generate-expect-links <bool>  Auto-generate expected links from built graph when missing (default: true).
  --skip-extract <bool>           Skip extractor run stage (default: false).
  --extract-mode <run|stages>     Extraction mode (default: stages; use run when DB-backed pipeline is required).
  --quality-policy <path>         Quality policy used by this E2E harness (default: quality/policy.e2e.json).
  --clean <bool>                  Delete output root before run (default: false).

Examples:
  $(basename "$0") --source "${ROOT_DIR}/checkout-service" --clean true
  $(basename "$0") --source "${ROOT_DIR}/checkout-service" --expect-links "${ROOT_DIR}/.diffmind/graph/expected_links.json"
USAGE
}

log() {
  printf '[m8-e2e] %s\n' "$*"
}

fail() {
  printf '[m8-e2e][error] %s\n' "$*" >&2
  exit 1
}

bool_normalize() {
  case "$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')" in
    true|1|yes|y) printf 'true' ;;
    false|0|no|n) printf 'false' ;;
    *) fail "invalid boolean value: $1" ;;
  esac
}

extract_mode_normalize() {
  case "$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')" in
    run|stages) printf '%s' "$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')" ;;
    *) fail "invalid --extract-mode value: $1 (expected run|stages)" ;;
  esac
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --source)
      SOURCE="$2"
      shift 2
      ;;
    --ref)
      REF="$2"
      shift 2
      ;;
    --out)
      OUT_ROOT="$2"
      shift 2
      ;;
    --service-id)
      SERVICE_ID="$2"
      shift 2
      ;;
    --service-name)
      SERVICE_NAME="$2"
      shift 2
      ;;
    --expect-links)
      EXPECT_LINKS="$2"
      shift 2
      ;;
    --generate-expect-links)
      GENERATE_EXPECT_LINKS="$(bool_normalize "$2")"
      shift 2
      ;;
    --skip-extract)
      SKIP_EXTRACT="$(bool_normalize "$2")"
      shift 2
      ;;
    --extract-mode)
      EXTRACT_MODE="$(extract_mode_normalize "$2")"
      shift 2
      ;;
    --quality-policy)
      QUALITY_POLICY="$2"
      shift 2
      ;;
    --clean)
      RUN_CLEAN="$(bool_normalize "$2")"
      shift 2
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      fail "unknown argument: $1"
      ;;
  esac
done

[[ -n "${SOURCE}" ]] || { usage; fail "--source is required"; }
[[ -d "${SOURCE}" ]] || fail "source directory does not exist: ${SOURCE}"
[[ -f "${QUALITY_POLICY}" ]] || fail "quality policy does not exist: ${QUALITY_POLICY}"

SOURCE="$(cd "${SOURCE}" && pwd)"
OUT_ROOT="$(mkdir -p "${OUT_ROOT}" && cd "${OUT_ROOT}" && pwd)"

if [[ "${RUN_CLEAN}" == "true" ]]; then
  log "Cleaning output root ${OUT_ROOT}"
  rm -rf "${OUT_ROOT}"
  mkdir -p "${OUT_ROOT}"
fi

if [[ -z "${SERVICE_ID}" ]]; then
  SERVICE_ID="$(basename "${SOURCE}")"
fi
if [[ -z "${SERVICE_NAME}" ]]; then
  SERVICE_NAME="${SERVICE_ID}"
fi

RUN_DIR="${OUT_ROOT}/service"
GRAPH_DIR="${RUN_DIR}/graph"
MERGE_REPORT="${GRAPH_DIR}/merge_quality_report.json"
GRAPH_INDEX="${GRAPH_DIR}/index.json"
QUALITY_DIR="${OUT_ROOT}/quality"
QUALITY_REPORT="${QUALITY_DIR}/report.json"
QUALITY_GATE="${QUALITY_DIR}/gate_result.json"
QUALITY_DASHBOARD="${QUALITY_DIR}/dashboard.md"
QUALITY_TRIAGE="${QUALITY_DIR}/triage.md"
CORPUS_DIR="${OUT_ROOT}/corpus-fixtures"
CORPUS_REPORT="${CORPUS_DIR}/report.json"
CORPUS_MANIFEST="${ROOT_DIR}/corpus/manifest.e2e.json"
SLO_DIR="${OUT_ROOT}/ops"
SLO_REPORT="${SLO_DIR}/slo_report.json"
FINAL_DIR="${OUT_ROOT}/final"
FINAL_REPORT="${FINAL_DIR}/readiness_report.json"
FINAL_DECISION="${FINAL_DIR}/gate_decision.md"

mkdir -p "${RUN_DIR}" "${QUALITY_DIR}" "${SLO_DIR}" "${FINAL_DIR}"

cmd() {
  log "RUN: $*"
  GOCACHE="${GOCACHE_DIR}" "$@"
}

if [[ "${SKIP_EXTRACT}" == "false" ]]; then
  if [[ "${EXTRACT_MODE}" == "run" ]]; then
    log "Stage 1/8: extractor run"
    cmd go run ./cmd/extractor run --source "${SOURCE}" --ref "${REF}" --out "${RUN_DIR}" --persist
  else
    log "Stage 1/8: extractor staged pipeline (scan -> parse -> analyze -> bundle)"
    cmd go run ./cmd/extractor scan --source "${SOURCE}" --out "${RUN_DIR}"
    cmd go run ./cmd/extractor parse --source "${SOURCE}" --out "${RUN_DIR}"
    cmd go run ./cmd/extractor analyze --source "${SOURCE}" --out "${RUN_DIR}"
    cmd go run ./cmd/extractor bundle --in "${RUN_DIR}/analyzers/bundle.json" --out "${RUN_DIR}"
  fi
else
  log "Stage 1/8: extractor run skipped"
fi

log "Stage 2/8: graph build"
cmd go run ./cmd/extractor graph build \
  --mode single \
  --service-id "${SERVICE_ID}" \
  --service-name "${SERVICE_NAME}" \
  --bundle "${RUN_DIR}/bundle/intelligence_bundle.json" \
  --analyzer-bundle "${RUN_DIR}/analyzers/bundle.json" \
  --out "${RUN_DIR}"

[[ -f "${GRAPH_INDEX}" ]] || fail "missing graph index: ${GRAPH_INDEX}"

GRAPH_PATH="$(jq -r '.graphs[0].path // empty' "${GRAPH_INDEX}")"
[[ -n "${GRAPH_PATH}" ]] || fail "graph index has no graph path: ${GRAPH_INDEX}"

if [[ "${GRAPH_PATH}" != /* ]]; then
  GRAPH_PATH="${ROOT_DIR}/${GRAPH_PATH}"
fi
[[ -f "${GRAPH_PATH}" ]] || fail "graph file does not exist: ${GRAPH_PATH}"

if [[ -z "${EXPECT_LINKS}" && "${GENERATE_EXPECT_LINKS}" == "true" ]]; then
  EXPECT_LINKS="${GRAPH_DIR}/expected_links.generated.json"
  log "Stage 3/8: generating expected links from built graph (${EXPECT_LINKS})"
  jq '{
    service_calls_service: [
      .edges[]
      | select(.type == "service_calls_service")
      | {
          source_service_id: ((.attributes.source_service_id // .source_id // "") | tostring),
          source_repo_path: ((.attributes.source_repo_path // "") | tostring),
          target_service_id: ((.attributes.target_service_id // .target_id // "") | tostring),
          target_repo_path: ((.attributes.target_repo_path // "") | tostring)
        }
      | select(.source_service_id != "" and .target_service_id != "")
    ] | unique,
    canonical_service_aliases: [
      .edges[]
      | select(.type == "service_alias_of_canonical_service")
      | {
          source_service_id: ((.attributes.source_service_id // .source_id // "") | tostring),
          source_repo_path: ((.attributes.source_repo_path // "") | tostring),
          canonical_key: ((.attributes.canonical_key // "") | tostring),
          env_scope: ((.attributes.env_scope // "") | tostring)
        }
      | select(.source_service_id != "" and .canonical_key != "")
    ] | unique
  }' "${GRAPH_PATH}" > "${EXPECT_LINKS}"
fi

ASSESS_ARGS=(go run ./cmd/extractor graph assess --index "${GRAPH_INDEX}" --out "${MERGE_REPORT}" --fail-on-gate)

TOTAL_EXPECTED=0
if [[ -n "${EXPECT_LINKS}" ]]; then
  [[ -f "${EXPECT_LINKS}" ]] || fail "expected-links file does not exist: ${EXPECT_LINKS}"
  TOTAL_EXPECTED="$(jq '(.service_calls_service // [] | length) + (.canonical_service_aliases // [] | length)' "${EXPECT_LINKS}")"
  if [[ "${TOTAL_EXPECTED}" -gt 0 ]]; then
    ASSESS_ARGS+=(--expect-links "${EXPECT_LINKS}")
    log "Stage 4/8: graph assess with benchmark (records=${TOTAL_EXPECTED})"
  else
    log "Stage 4/8: graph assess without benchmark (expected-links file has zero records)"
  fi
else
  log "Stage 4/8: graph assess without benchmark (no expected-links configured)"
fi
cmd "${ASSESS_ARGS[@]}"

[[ -f "${MERGE_REPORT}" ]] || fail "missing merge quality report: ${MERGE_REPORT}"

log "Stage 5/8: corpus fixtures evaluation input"
[[ -f "${CORPUS_MANIFEST}" ]] || fail "missing corpus manifest: ${CORPUS_MANIFEST}"
cmd go run ./cmd/extractor corpus --manifest "${CORPUS_MANIFEST}" --out "${CORPUS_DIR}"
[[ -f "${CORPUS_REPORT}" ]] || fail "missing corpus report: ${CORPUS_REPORT}"

log "Stage 6/8: quality evaluate + gate"
QUALITY_ARGS=(
  go run ./cmd/extractor quality evaluate
  --corpus "${CORPUS_REPORT}"
  --golden "${ROOT_DIR}/corpus/golden/fixtures_summary.json"
  --merge-quality "${MERGE_REPORT}"
  --graph-index "${GRAPH_INDEX}"
  --merge-quality-auto
  --out "${QUALITY_REPORT}"
  --dashboard "${QUALITY_DASHBOARD}"
  --triage "${QUALITY_TRIAGE}"
)
if [[ -n "${EXPECT_LINKS}" && "${TOTAL_EXPECTED}" -gt 0 ]]; then
  QUALITY_ARGS+=(--merge-quality-expect-links "${EXPECT_LINKS}")
fi
cmd "${QUALITY_ARGS[@]}"

cmd go run ./cmd/extractor quality gate \
  --report "${QUALITY_REPORT}" \
  --policy "${QUALITY_POLICY}" \
  --out "${QUALITY_GATE}"

[[ -f "${QUALITY_GATE}" ]] || fail "missing quality gate result: ${QUALITY_GATE}"

log "Stage 7/8: ops slo"
cmd go run ./cmd/extractor ops slo \
  --audit-root "${RUN_DIR}" \
  --quality "${QUALITY_REPORT}" \
  --out "${SLO_REPORT}"

[[ -f "${SLO_REPORT}" ]] || fail "missing slo report: ${SLO_REPORT}"

log "Stage 8/8: finalgate attest"
FINAL_ARGS=(
  go run ./cmd/extractor finalgate attest
  --quality-gate "${QUALITY_GATE}"
  --merge-quality "${MERGE_REPORT}"
  --slo "${SLO_REPORT}"
  --templates "${ROOT_DIR}/docs/m15_query_templates.json"
  --catalog "${ROOT_DIR}/docs/m17_question_catalog.json"
  --graph-index "${GRAPH_INDEX}"
  --out-report "${FINAL_REPORT}"
  --out-decision "${FINAL_DECISION}"
  --signers "engineering,platform,security"
)
if [[ -n "${EXPECT_LINKS}" && "${TOTAL_EXPECTED}" -gt 0 ]]; then
  FINAL_ARGS+=(--merge-quality-expect-links "${EXPECT_LINKS}")
fi
cmd "${FINAL_ARGS[@]}"

[[ -f "${FINAL_REPORT}" ]] || fail "missing final readiness report: ${FINAL_REPORT}"
[[ -f "${FINAL_DECISION}" ]] || fail "missing final decision: ${FINAL_DECISION}"

OVERALL_PASSED="$(jq -r '.overall_passed // false' "${FINAL_REPORT}")"
[[ "${OVERALL_PASSED}" == "true" ]] || fail "final readiness report indicates failure"

log "E2E validation completed successfully"
log "Artifacts:"
log "  - ${MERGE_REPORT}"
log "  - ${QUALITY_REPORT}"
log "  - ${QUALITY_GATE}"
log "  - ${SLO_REPORT}"
log "  - ${FINAL_REPORT}"
log "  - ${FINAL_DECISION}"
