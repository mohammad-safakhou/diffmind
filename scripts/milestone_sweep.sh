#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GOCACHE_DIR="${GOCACHE:-${ROOT_DIR}/.gocache}"

SOURCE="${ROOT_DIR}/corpus/fixtures/18-mixed-monorepo"
OUT_ROOT="${ROOT_DIR}/.diffmind/milestone-sweep"
M4_REPORT="${ROOT_DIR}/.diffmind/m4-closeout/m4_closure_report.json"
SKIP_TESTS="false"
SKIP_M4_CHECK="false"

usage() {
  cat <<USAGE
Usage: $(basename "$0") [options]

Options:
  --source <path>           Source repo for runnable E2E chain (default: ${SOURCE})
  --out <path>              Output root (default: ${OUT_ROOT})
  --m4-report <path>        Existing M4 closure report path
  --skip-tests <bool>       Skip Go test stage (default: false)
  --skip-m4-check <bool>    Skip M4 report validation (default: false)
USAGE
}

log() {
  printf '[milestone-sweep] %s\n' "$*"
}

fail() {
  printf '[milestone-sweep][error] %s\n' "$*" >&2
  exit 1
}

bool_normalize() {
  case "$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')" in
    true|1|yes|y) printf 'true' ;;
    false|0|no|n) printf 'false' ;;
    *) fail "invalid boolean value: $1" ;;
  esac
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --source)
      SOURCE="$2"; shift 2 ;;
    --out)
      OUT_ROOT="$2"; shift 2 ;;
    --m4-report)
      M4_REPORT="$2"; shift 2 ;;
    --skip-tests)
      SKIP_TESTS="$(bool_normalize "$2")"; shift 2 ;;
    --skip-m4-check)
      SKIP_M4_CHECK="$(bool_normalize "$2")"; shift 2 ;;
    -h|--help)
      usage; exit 0 ;;
    *)
      fail "unknown argument: $1" ;;
  esac
done

[[ -d "$SOURCE" ]] || fail "source not found: $SOURCE"
mkdir -p "$OUT_ROOT"

cmd() {
  log "RUN: $*"
  GOCACHE="$GOCACHE_DIR" "$@"
}

if [[ "$SKIP_TESTS" == "false" ]]; then
  log "Stage 1/5: core tests"
  cmd go test ./internal/analyzers ./internal/graph ./internal/quality ./internal/finalgate
else
  log "Stage 1/5: core tests skipped"
fi

if [[ "$SKIP_M4_CHECK" == "false" ]]; then
  log "Stage 2/5: M4 closure evidence validation"
  [[ -f "$M4_REPORT" ]] || fail "missing M4 report: $M4_REPORT"
  M4_OK="$(jq -r '.overall_passed // false' "$M4_REPORT")"
  [[ "$M4_OK" == "true" ]] || fail "M4 report is not passing: $M4_REPORT"
else
  log "Stage 2/5: M4 closure check skipped"
  M4_OK="unknown"
fi

log "Stage 3/5: runnable E2E chain (M8/M14/M16/M17 readiness)"
E2E_OUT="$OUT_ROOT/e2e"
"$ROOT_DIR/scripts/e2e_m8_validation.sh" --source "$SOURCE" --out "$E2E_OUT" --clean true

log "Stage 4/5: full final closeout"
CLOSEOUT_OUT="$OUT_ROOT/closeout"
mkdir -p "$CLOSEOUT_OUT"
# Copy E2E outputs and remove audit to validate closeout self-healing path.
rm -rf "$CLOSEOUT_OUT/work"
cp -R "$E2E_OUT" "$CLOSEOUT_OUT/work"
rm -rf "$CLOSEOUT_OUT/work/audit"

cmd go run ./cmd/extractor finalgate closeout \
  --quality-gate "$CLOSEOUT_OUT/work/quality/gate_result.json" \
  --merge-quality "$CLOSEOUT_OUT/work/service/graph/merge_quality_report.json" \
  --slo "$CLOSEOUT_OUT/work/ops/slo_report.json" \
  --templates "$ROOT_DIR/docs/m15_query_templates.json" \
  --catalog "$ROOT_DIR/docs/m17_question_catalog.json" \
  --graph-index "$CLOSEOUT_OUT/work/service/graph/index.json" \
  --quality-report "$CLOSEOUT_OUT/work/quality/report.json" \
  --corpus-report "$CLOSEOUT_OUT/work/corpus-fixtures/report.json" \
  --performance-policy "$ROOT_DIR/docs/graph_performance_baseline.md" \
  --audit-root "$CLOSEOUT_OUT/work" \
  --drill-source "$CLOSEOUT_OUT/work" \
  --drill-out "$CLOSEOUT_OUT/work/final/drills" \
  --out-report "$CLOSEOUT_OUT/work/final/readiness_report.json" \
  --out-decision "$CLOSEOUT_OUT/work/final/gate_decision.md" \
  --out-milestones "$CLOSEOUT_OUT/work/final/milestone_closure_report.json" \
  --out-benchmark "$CLOSEOUT_OUT/work/final/benchmark_evidence_report.json" \
  --out-security "$CLOSEOUT_OUT/work/final/security_validation_report.json" \
  --out-ops "$CLOSEOUT_OUT/work/final/operations_drill_report.json" \
  --signers engineering,platform,security

log "Stage 5/5: summary generation"
SUMMARY="$OUT_ROOT/summary.json"
MILESTONE_PASS="$(jq -r '.overall_passed // false' "$CLOSEOUT_OUT/work/final/milestone_closure_report.json")"
READINESS_PASS="$(jq -r '.overall_passed // false' "$CLOSEOUT_OUT/work/final/readiness_report.json")"
FAILED_IDS="$(jq -c '[.milestones[] | select(.passed==false) | .id]' "$CLOSEOUT_OUT/work/final/milestone_closure_report.json")"
SEC_AUDIT_EVENTS="$(jq '.audit_events // 0' "$CLOSEOUT_OUT/work/final/security_validation_report.json")"

jq -n \
  --arg generated_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --arg source "$SOURCE" \
  --arg m4_check "$M4_OK" \
  --arg readiness "$READINESS_PASS" \
  --arg milestones "$MILESTONE_PASS" \
  --argjson failed_ids "$FAILED_IDS" \
  --argjson security_audit_events "$SEC_AUDIT_EVENTS" \
  --arg m4_report "$M4_REPORT" \
  --arg readiness_report "$CLOSEOUT_OUT/work/final/readiness_report.json" \
  --arg milestone_report "$CLOSEOUT_OUT/work/final/milestone_closure_report.json" \
  --arg security_report "$CLOSEOUT_OUT/work/final/security_validation_report.json" \
  '{
    generated_at_utc: $generated_at,
    source: $source,
    overall_passed: (($readiness == "true") and ($milestones == "true") and ($m4_check == "true" or $m4_check == "unknown")),
    checks: {
      m4_closure_passed: $m4_check,
      readiness_passed: $readiness,
      milestone_closeout_passed: $milestones,
      failed_milestones: $failed_ids,
      security_audit_events: $security_audit_events
    },
    artifacts: {
      m4_report: $m4_report,
      readiness_report: $readiness_report,
      milestone_report: $milestone_report,
      security_report: $security_report
    }
  }' > "$SUMMARY"

log "Milestone sweep completed"
log "Summary: $SUMMARY"
cat "$SUMMARY"
