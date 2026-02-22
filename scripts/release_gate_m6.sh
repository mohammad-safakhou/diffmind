#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GOCACHE_DIR="${GOCACHE:-${ROOT_DIR}/.gocache}"

OUT_ROOT="${ROOT_DIR}/.diffmind/release-gate-m6"
QUALITY_POLICY="${ROOT_DIR}/quality/policy.e2e.json"
RUN_CLEAN="false"
STRICT_MODE="true"
RUN_API_CONTRACT_TESTS="true"
EXPECTED_CONTRACT=""
REQUIRE_REAL_SUITE="true"
MIN_REAL_SOURCES=3
SOURCE_BASELINES="${ROOT_DIR}/quality/source_baselines.e2e.json"
declare -a SOURCES=()

usage() {
  cat <<USAGE
Usage: $(basename "$0") [options]

Options:
  --source <path>              Source repository path (repeatable).
  --sources <csv>              Comma-separated source paths.
  --out <path>                 Output root (default: ${OUT_ROOT}).
  --quality-policy <path>      Quality policy for per-source E2E runs.
  --clean <bool>               Delete output root before run (default: false).
  --strict <bool>              Fail when aggregated scorecard gate fails (default: true).
  --api-contract-tests <bool>  Run API contract tests before E2E runs (default: true).
  --expected-contract <path>   Optional graph contract JSON path.
  --require-real-suite <bool>  Require real-repo suite pass/fail gate (default: true).
  --min-real-sources <count>   Minimum real repos required (default: 3).
  --source-baselines <path>    JSON policy for per-source minimum thresholds (default: ${SOURCE_BASELINES}).
  -h|--help                    Show this message.

Default sources (if none specified):
  Real repos:
  1) ${ROOT_DIR}/.codebases/checkout-service
  2) ${ROOT_DIR}/../sample-service
  3) ${ROOT_DIR}/../inventory-service
  Fixture smoke:
  4) ${ROOT_DIR}/corpus/fixtures/18-mixed-monorepo
  5) ${ROOT_DIR}/corpus/fixtures/01-go-gin-service
  6) ${ROOT_DIR}/corpus/fixtures/03-node-express-api
USAGE
}

log() {
  printf '[m6-release-gate] %s\n' "$*"
}

fail() {
  printf '[m6-release-gate][error] %s\n' "$*" >&2
  exit 1
}

bool_normalize() {
  case "$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')" in
    true|1|yes|y) printf 'true' ;;
    false|0|no|n) printf 'false' ;;
    *) fail "invalid boolean value: $1" ;;
  esac
}

append_sources_csv() {
  local csv="$1"
  local entry=""
  IFS=',' read -r -a arr <<< "$csv"
  for entry in "${arr[@]}"; do
    entry="$(printf '%s' "$entry" | xargs)"
    if [[ -n "$entry" ]]; then
      SOURCES+=("$entry")
    fi
  done
}

append_source_if_dir() {
  local path="$1"
  if [[ -d "${path}" ]]; then
    SOURCES+=("${path}")
  fi
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --source)
      SOURCES+=("$2")
      shift 2
      ;;
    --sources)
      append_sources_csv "$2"
      shift 2
      ;;
    --out)
      OUT_ROOT="$2"
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
    --strict)
      STRICT_MODE="$(bool_normalize "$2")"
      shift 2
      ;;
    --api-contract-tests)
      RUN_API_CONTRACT_TESTS="$(bool_normalize "$2")"
      shift 2
      ;;
    --expected-contract)
      EXPECTED_CONTRACT="$2"
      shift 2
      ;;
    --require-real-suite)
      REQUIRE_REAL_SUITE="$(bool_normalize "$2")"
      shift 2
      ;;
    --min-real-sources)
      MIN_REAL_SOURCES="$2"
      shift 2
      ;;
    --source-baselines)
      SOURCE_BASELINES="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      fail "unknown argument: $1"
      ;;
  esac
done

if [[ ${#SOURCES[@]} -eq 0 ]]; then
  append_source_if_dir "${ROOT_DIR}/.codebases/checkout-service"
  append_source_if_dir "${ROOT_DIR}/../sample-service"
  append_source_if_dir "${ROOT_DIR}/../inventory-service"
  append_source_if_dir "${ROOT_DIR}/corpus/fixtures/18-mixed-monorepo"
  append_source_if_dir "${ROOT_DIR}/corpus/fixtures/01-go-gin-service"
  append_source_if_dir "${ROOT_DIR}/corpus/fixtures/03-node-express-api"
fi

if ! [[ "${MIN_REAL_SOURCES}" =~ ^[0-9]+$ ]]; then
  fail "--min-real-sources must be integer, got: ${MIN_REAL_SOURCES}"
fi

if [[ ! -f "${QUALITY_POLICY}" ]]; then
  fail "quality policy not found: ${QUALITY_POLICY}"
fi
if [[ -n "${SOURCE_BASELINES}" && ! -f "${SOURCE_BASELINES}" ]]; then
  fail "source baselines policy not found: ${SOURCE_BASELINES}"
fi

mkdir -p "${OUT_ROOT}"
OUT_ROOT="$(cd "${OUT_ROOT}" && pwd)"
if [[ "${RUN_CLEAN}" == "true" ]]; then
  log "Cleaning output root ${OUT_ROOT}"
  rm -rf "${OUT_ROOT}"
  mkdir -p "${OUT_ROOT}"
fi

cmd() {
  log "RUN: $*"
  GOCACHE="${GOCACHE_DIR}" "$@"
}

if [[ "${RUN_API_CONTRACT_TESTS}" == "true" ]]; then
  log "Stage 1/4: API contract regression tests"
  cmd go test ./internal/httpapi -run 'TestProductEndpoints|TestMergeQualityEndpoints|TestQualityEndpoints' -count=1
else
  log "Stage 1/4: API contract regression tests skipped"
fi

RUNS_DIR="${OUT_ROOT}/runs"
SCORECARDS_DIR="${OUT_ROOT}/scorecards"
mkdir -p "${RUNS_DIR}" "${SCORECARDS_DIR}"

log "Stage 2/4: Per-source E2E harness runs"
declare -a RUN_SCORECARD_PATHS=()
real_sources_total=0
real_sources_passed=0
fixtures_total=0
catalogue_present="false"
for source in "${SOURCES[@]}"; do
  if [[ ! -d "${source}" ]]; then
    fail "source directory not found: ${source}"
  fi
  source_abs="$(cd "${source}" && pwd)"
  source_id="$(basename "${source_abs}")"
  source_type="real"
  if [[ "${source_abs}" == *"/corpus/fixtures/"* ]]; then
    source_type="fixture"
  fi
  if [[ "${source_id}" == "checkout-service" ]]; then
    catalogue_present="true"
  fi
  if [[ "${source_type}" == "real" ]]; then
    real_sources_total=$((real_sources_total + 1))
  else
    fixtures_total=$((fixtures_total + 1))
  fi
  run_out="${RUNS_DIR}/${source_id}"
  log "Running E2E for source=${source_abs} out=${run_out}"
  "${ROOT_DIR}/scripts/e2e_m8_validation.sh" \
    --source "${source_abs}" \
    --out "${run_out}" \
    --quality-policy "${QUALITY_POLICY}" \
    --clean true

  graph_index="${run_out}/service/graph/index.json"
  readiness_report="${run_out}/final/readiness_report.json"
  quality_report="${run_out}/quality/report.json"
  quality_gate="${run_out}/quality/gate_result.json"

  [[ -f "${graph_index}" ]] || fail "missing graph index for ${source_id}: ${graph_index}"
  [[ -f "${readiness_report}" ]] || fail "missing readiness report for ${source_id}: ${readiness_report}"
  [[ -f "${quality_report}" ]] || fail "missing quality report for ${source_id}: ${quality_report}"
  [[ -f "${quality_gate}" ]] || fail "missing quality gate for ${source_id}: ${quality_gate}"

  graph_path="$(jq -r '.graphs[0].path // empty' "${graph_index}")"
  [[ -n "${graph_path}" ]] || fail "graph index has no graph path for ${source_id}: ${graph_index}"
  if [[ "${graph_path}" != /* ]]; then
    graph_path="${ROOT_DIR}/${graph_path}"
  fi
  [[ -f "${graph_path}" ]] || fail "graph file not found for ${source_id}: ${graph_path}"

  task_dir="${run_out}/tasks"
  mkdir -p "${task_dir}"
  task_report="${task_dir}/architecture_tasks.json"
  focus_subgraph="${task_dir}/focused_subgraph.json"
  focus_node="$(
    jq -r '
      [
        ((.nodes // []) | map(select((.section // "") == "exposure" and (.type // "") == "endpoint") | .id) | .[0] // ""),
        ((.nodes // []) | map(select((.section // "") == "exposure") | .id) | .[0] // ""),
        ((.nodes // []) | map(select((.type // "") == "service") | .id) | .[0] // "")
      ]
      | map(select(length > 0))
      | .[0] // ""
    ' "${graph_path}"
  )"
  if [[ -n "${focus_node}" ]]; then
    jq --arg focus "${focus_node}" '
      . as $g
      | [($g.edges // [])[] | select(.source_id == $focus or .target_id == $focus)] as $neighbor_edges
      | ([$focus] + ([$neighbor_edges[]?.source_id, $neighbor_edges[]?.target_id])) as $ids
      | ($ids | map(select(length > 0)) | unique) as $keep
      | {
          graph_id: ($g.graph_id // ""),
          focus_node_id: $focus,
          nodes: [($g.nodes // [])[] | select((.id as $id | $keep | index($id)) != null)],
          edges: [($g.edges // [])[] | select((.source_id as $sid | $keep | index($sid)) != null and (.target_id as $tid | $keep | index($tid)) != null)]
        }
      | .meta = {
          node_count: ((.nodes // []) | length),
          edge_count: ((.edges // []) | length),
          exported_at_utc: now | todateiso8601
        }
    ' "${graph_path}" > "${focus_subgraph}"
  else
    jq -n --arg graph_id "$(jq -r '.graph_id // ""' "${graph_path}")" '
      {
        graph_id: $graph_id,
        focus_node_id: "",
        nodes: [],
        edges: [],
        meta: { node_count: 0, edge_count: 0, exported_at_utc: (now | todateiso8601) }
      }
    ' > "${focus_subgraph}"
  fi

  jq -n \
    --arg graph_path "${graph_path}" \
    --arg focus_node "${focus_node}" \
    --arg focus_subgraph "${focus_subgraph}" \
    --slurpfile graph "${graph_path}" '
    def any_true($arr): (($arr | map(select(. == true)) | length) > 0);
    def edge_connected($ids; $types):
      any_true(
        [($graph[0].edges // [])[] | . as $e | select(
          (($types | index($e.type)) != null)
          and (
            (($ids | index($e.source_id)) != null) or
            (($ids | index($e.target_id)) != null)
          )
        ) | true]
      );
    def service_nodes_for($node_ids):
      [($graph[0].edges // [])[] | . as $e
        | select((($node_ids | index($e.source_id)) != null and ($e.target_id | startswith("svc:")))
              or (($node_ids | index($e.target_id)) != null and ($e.source_id | startswith("svc:"))))
        | if (($node_ids | index($e.source_id)) != null) then $e.target_id else $e.source_id end
      ] | unique;
    def dependencies_ids:
      [($graph[0].nodes // [])[] | select((.section // "") == "dependencies") | .id];
    def exposure_endpoint_ids:
      [($graph[0].nodes // [])[] | select((.section // "") == "exposure" and (.type // "") == "endpoint") | .id];
    def exposure_scheduler_ids:
      [($graph[0].nodes // [])[] | select(
        (.section // "") == "exposure" and (
          ((.class // "") | ascii_downcase | contains("scheduler"))
          or ((.label // "") | ascii_downcase | contains("cron"))
          or ((.label // "") | ascii_downcase | contains("schedule"))
        )
      ) | .id];
    def queue_ids:
      [($graph[0].nodes // [])[] | select((.type // "") == "queue" or (.type // "") == "topic") | .id];
    def exposure_count: ([($graph[0].nodes // [])[] | select((.section // "") == "exposure")] | length);
    def focus_subgraph_meta:
      (
        try (
          (input_filename | .)
        ) catch null
      );
    (
      exposure_endpoint_ids as $endpoint_ids
      | exposure_scheduler_ids as $scheduler_ids
      | dependencies_ids as $dependency_ids
      | queue_ids as $queue_ids
      | service_nodes_for($endpoint_ids) as $endpoint_svcs
      | service_nodes_for($scheduler_ids) as $scheduler_svcs
      | {
          generated_at_utc: (now | todateiso8601),
          graph_path: $graph_path,
          focus_node_id: $focus_node,
          focus_subgraph_path: $focus_subgraph,
          tasks: {
            find_exposures: {
              applicable: true,
              passed: (exposure_count > 0),
              expected: "at least one exposure node exists",
              observed: { exposure_count: exposure_count }
            },
            trace_endpoint_to_dependencies: {
              applicable: (($endpoint_ids | length) > 0 and ($dependency_ids | length) > 0),
              passed: (
                (($endpoint_ids | length) == 0 or ($dependency_ids | length) == 0)
                or edge_connected(
                  $endpoint_svcs;
                  ["service_calls_service","service_publishes_queue","service_reads_db","service_writes_db","service_calls_endpoint"]
                )
              ),
              expected: "endpoint-exposing service can be traced to dependency-bearing edges",
              observed: {
                endpoint_count: ($endpoint_ids | length),
                dependency_count: ($dependency_ids | length),
                endpoint_service_count: ($endpoint_svcs | length)
              }
            },
            identify_queue_consumers_publishers: {
              applicable: (($queue_ids | length) > 0),
              passed: (
                (($queue_ids | length) == 0) or
                edge_connected($queue_ids; ["service_publishes_queue","queue_delivers_to_service"])
              ),
              expected: "queue/topic nodes have producer or consumer edges",
              observed: { queue_node_count: ($queue_ids | length) }
            },
            trace_scheduler_trigger_paths: {
              applicable: (($scheduler_ids | length) > 0 and ($dependency_ids | length) > 0),
              passed: (
                (($scheduler_ids | length) == 0 or ($dependency_ids | length) == 0)
                or edge_connected(
                  $scheduler_svcs;
                  ["service_calls_service","service_publishes_queue","service_reads_db","service_writes_db","service_calls_endpoint"]
                )
              ),
              expected: "scheduler-exposing service can be traced to dependency-bearing edges",
              observed: {
                scheduler_count: ($scheduler_ids | length),
                dependency_count: ($dependency_ids | length),
                scheduler_service_count: ($scheduler_svcs | length)
              }
            },
            export_focused_subgraph: {
              applicable: (($focus_node | length) > 0),
              passed: (
                ($focus_node | length) > 0
                and (
                  ([($graph[0].nodes // [])[] | select(.id == $focus_node)] | length) > 0
                )
              ),
              expected: "focused subgraph export target node exists and artifact is produced",
              observed: {
                focus_node_id: $focus_node
              }
            }
          }
        }
      | .summary = (
          (.tasks | to_entries) as $entries
          | {
              total_tasks: ($entries | length),
              applicable_tasks: (($entries | map(select(.value.applicable == true)) | length)),
              passed_tasks: (($entries | map(select(.value.passed == true)) | length)),
              pass_rate: (
                ($entries | map(select(.value.applicable == true)) | length) as $app
                | if $app == 0 then 1 else (($entries | map(select(.value.applicable == true and .value.passed == true)) | length) / $app) end
              )
            }
        )
    )
    ' "${graph_path}" > "${task_report}"

  # Strengthen export task with actual artifact validation.
  export_ok="$(
    jq -r '
      ((.meta.node_count // 0) > 0 and (.focus_node_id // "") != "")
    ' "${focus_subgraph}" 2>/dev/null || printf 'false'
  )"
  if [[ "${export_ok}" == "true" ]]; then
    jq '.tasks.export_focused_subgraph.passed = true' "${task_report}" > "${task_report}.tmp"
  else
    jq '.tasks.export_focused_subgraph.passed = false' "${task_report}" > "${task_report}.tmp"
  fi
  mv "${task_report}.tmp" "${task_report}"
  jq '
    .summary = (
      (.tasks | to_entries) as $entries
      | {
          total_tasks: ($entries | length),
          applicable_tasks: (($entries | map(select(.value.applicable == true)) | length)),
          passed_tasks: (($entries | map(select(.value.applicable == true and .value.passed == true)) | length)),
          pass_rate: (
            ($entries | map(select(.value.applicable == true)) | length) as $app
            | if $app == 0 then 1 else (($entries | map(select(.value.applicable == true and .value.passed == true)) | length) / $app) end
          )
        }
    )
  ' "${task_report}" > "${task_report}.tmp"
  mv "${task_report}.tmp" "${task_report}"

  contract_report="${task_dir}/graph_contract_report.json"
  contract_gate_applicable="false"
  contract_gate_passed="true"
  contract_gate_reason="not_applicable"
  contract_path=""
  if [[ -n "${EXPECTED_CONTRACT}" ]]; then
    contract_path="${EXPECTED_CONTRACT}"
    if [[ "${contract_path}" != /* ]]; then
      contract_path="${ROOT_DIR}/${contract_path}"
    fi
  elif [[ "${source_id}" == "checkout-service" ]]; then
    contract_path="${ROOT_DIR}/docs/contracts/checkout-service.expected_graph_contract.json"
  fi

  if [[ -n "${contract_path}" ]]; then
    contract_gate_applicable="true"
    if [[ ! -f "${contract_path}" ]]; then
      contract_gate_passed="false"
      contract_gate_reason="missing_contract_file"
    else
      contract_gate_reason="evaluated"
      if ! cmd go run ./cmd/extractor graph contract --graph "${graph_path}" --contract "${contract_path}" --out "${contract_report}" --fail-on-gate; then
        contract_gate_passed="false"
      fi
    fi
  fi

  scorecard_path="${SCORECARDS_DIR}/${source_id}.json"
  prev_scorecard_path=""
  if [[ -f "${scorecard_path}" ]]; then
    prev_scorecard_path="${scorecard_path}"
  fi
  jq -n \
    --arg source_id "${source_id}" \
    --arg source_path "${source_abs}" \
    --arg run_out "${run_out}" \
    --arg graph_path "${graph_path}" \
    --arg readiness_path "${readiness_report}" \
    --arg quality_report_path "${quality_report}" \
    --arg quality_gate_path "${quality_gate}" \
    --arg task_report_path "${task_report}" \
    --arg focus_subgraph_path "${focus_subgraph}" \
    --arg contract_report_path "${contract_report}" \
    --arg contract_gate_applicable "${contract_gate_applicable}" \
    --arg contract_gate_passed "${contract_gate_passed}" \
    --arg contract_gate_reason "${contract_gate_reason}" \
    --arg contract_path "${contract_path}" \
    --arg source_type "${source_type}" \
    --arg prev_scorecard_path "${prev_scorecard_path}" \
    --slurpfile graph "${graph_path}" \
    --slurpfile readiness "${readiness_report}" \
    --slurpfile quality "${quality_report}" \
    --slurpfile gate "${quality_gate}" \
    --slurpfile task "${task_report}" \
    --slurpfile prev "$(if [[ -n "${prev_scorecard_path}" ]]; then printf '%s' "${prev_scorecard_path}"; else printf '%s' '/dev/null'; fi)" \
    '
    def readiness_pass($name):
      [($readiness[0].checks // [])[] | select(.name == $name) | .passed][0] // false;
    def section_count($section):
      [($graph[0].nodes // [])[] | select((.section // "") == $section)] | length;
    def section_coverage_ratio:
      (
        [
          (section_count("exposure") > 0),
          (section_count("logic") > 0),
          (section_count("dependencies") > 0)
        ]
        | map(select(. == true))
        | length
      ) / 3;
    {
      source_id: $source_id,
      source_path: $source_path,
      run_out: $run_out,
      artifacts: {
        graph_path: $graph_path,
        readiness_report: $readiness_path,
        quality_report: $quality_report_path,
        quality_gate: $quality_gate_path,
        task_report: $task_report_path,
        focused_subgraph: $focus_subgraph_path,
        contract_report: (if $contract_gate_applicable == "true" then $contract_report_path else "" end),
        contract_path: (if $contract_gate_applicable == "true" then $contract_path else "" end)
      },
      gates: {
        readiness_passed: ($readiness[0].overall_passed // false),
        quality_gate_passed: (($gate[0].overall_passed // $gate[0].passed) // false),
        explainability_traceability_100: readiness_pass("explainability_traceability_100"),
        graph_traceability_coverage_100: readiness_pass("graph_traceability_coverage_100"),
        architecture_task_suite_passed: (($task[0].summary.pass_rate // 0) == 1),
        contract_gate_applicable: ($contract_gate_applicable == "true"),
        contract_gate_passed: ($contract_gate_passed == "true"),
        contract_gate_reason: $contract_gate_reason
      },
      source_type: $source_type,
      scorecard: {
        accuracy: {
          pass_rate: ($quality[0].metrics.pass_rate // 0),
          precision: ($quality[0].metrics.precision // 0),
          recall: ($quality[0].metrics.recall // 0),
          f1: ($quality[0].metrics.f1 // 0)
        },
        completeness: {
          node_count: (($graph[0].nodes // []) | length),
          edge_count: (($graph[0].edges // []) | length),
          exposures_nodes: section_count("exposure"),
          logic_nodes: section_count("logic"),
          dependencies_nodes: section_count("dependencies"),
          all_sections_present: ((section_count("exposure") > 0) and (section_count("logic") > 0) and (section_count("dependencies") > 0)),
          section_coverage_ratio: section_coverage_ratio
        },
        explainability: {
          explainability_traceability_100: readiness_pass("explainability_traceability_100"),
          graph_traceability_coverage_100: readiness_pass("graph_traceability_coverage_100")
        },
        architecture_tasks: ($task[0] // {}),
        readiness_task_pass_rate: (
          (
            [
              readiness_pass("question_catalog_coverage_100"),
              readiness_pass("question_catalog_api_coverage_100"),
              readiness_pass("explainability_traceability_100"),
              readiness_pass("graph_traceability_coverage_100")
            ] | map(select(. == true)) | length
          ) / 4
        ),
        task_pass_rate: (
          ($task[0].summary.pass_rate // 0)
        )
      },
      drift: {
        has_previous: (($prev | length) > 0),
        precision_delta: (
          if ($prev | length) == 0 then null
          else (($quality[0].metrics.precision // 0) - ($prev[0].scorecard.accuracy.precision // 0))
          end
        ),
        recall_delta: (
          if ($prev | length) == 0 then null
          else (($quality[0].metrics.recall // 0) - ($prev[0].scorecard.accuracy.recall // 0))
          end
        ),
        f1_delta: (
          if ($prev | length) == 0 then null
          else (($quality[0].metrics.f1 // 0) - ($prev[0].scorecard.accuracy.f1 // 0))
          end
        ),
        node_count_delta: (
          if ($prev | length) == 0 then null
          else (((($graph[0].nodes // []) | length)) - ($prev[0].scorecard.completeness.node_count // 0))
          end
        ),
        edge_count_delta: (
          if ($prev | length) == 0 then null
          else (((($graph[0].edges // []) | length)) - ($prev[0].scorecard.completeness.edge_count // 0))
          end
        )
      }
    }' > "${scorecard_path}"

  baseline_eval_path="${task_dir}/source_baseline_eval.json"
  if [[ -n "${SOURCE_BASELINES}" ]]; then
    jq -n \
      --arg source_id "${source_id}" \
      --slurpfile sc "${scorecard_path}" \
      --slurpfile policy "${SOURCE_BASELINES}" '
      def check_min($name; $actual; $min):
        if ($min == null) then [] else
          if ($actual >= $min) then [] else
            [($name + " below minimum: actual=" + (($actual|tostring)) + ", min=" + (($min|tostring)))]
          end
        end;
      ($policy[0] // {}) as $p
      | (($p.sources // {})[$source_id] // ($p.default // {})) as $r
      | ($sc[0] // {}) as $s
      | {
          source_id: $source_id,
          applicable: (($r | length) > 0),
          resolved: $r,
          failures: (
            []
            + check_min("accuracy.precision"; ($s.scorecard.accuracy.precision // 0); ($r.min_precision // null))
            + check_min("accuracy.recall"; ($s.scorecard.accuracy.recall // 0); ($r.min_recall // null))
            + check_min("accuracy.f1"; ($s.scorecard.accuracy.f1 // 0); ($r.min_f1 // null))
            + check_min("accuracy.pass_rate"; ($s.scorecard.accuracy.pass_rate // 0); ($r.min_pass_rate // null))
            + check_min("task.pass_rate"; ($s.scorecard.task_pass_rate // 0); ($r.min_task_pass_rate // null))
            + check_min("completeness.section_coverage_ratio"; ($s.scorecard.completeness.section_coverage_ratio // 0); ($r.min_section_coverage_ratio // null))
            + (
              if (($r.require_contract_gate // false) == true and ($s.gates.contract_gate_passed // false) != true)
              then ["contract gate required by source baseline but not passed"]
              else []
              end
            )
          )
        }
      | .passed = ((.failures | length) == 0)
      ' > "${baseline_eval_path}"

    jq --slurpfile base "${baseline_eval_path}" '
      .gates.source_baseline_applicable = ($base[0].applicable // false)
      | .gates.source_baseline_passed = (
          if (($base[0].applicable // false) == true) then ($base[0].passed // false) else true end
        )
      | .gates.source_baseline_failures = ($base[0].failures // [])
      | .artifacts.source_baseline_eval = $base[0]
    ' "${scorecard_path}" > "${scorecard_path}.tmp"
    mv "${scorecard_path}.tmp" "${scorecard_path}"
  else
    jq '
      .gates.source_baseline_applicable = false
      | .gates.source_baseline_passed = true
      | .gates.source_baseline_failures = []
    ' "${scorecard_path}" > "${scorecard_path}.tmp"
    mv "${scorecard_path}.tmp" "${scorecard_path}"
  fi

  RUN_SCORECARD_PATHS+=("${scorecard_path}")
  if jq -e '.gates.readiness_passed == true and .gates.quality_gate_passed == true and .gates.architecture_task_suite_passed == true and .gates.source_baseline_passed == true and ((.gates.contract_gate_applicable|not) or .gates.contract_gate_passed == true)' "${scorecard_path}" >/dev/null; then
    if [[ "${source_type}" == "real" ]]; then
      real_sources_passed=$((real_sources_passed + 1))
    fi
  fi
  log "Scorecard generated: ${scorecard_path}"
done

log "Stage 3/4: Aggregated release gate scorecard"
ALL_SCORECARDS_JSON="${SCORECARDS_DIR}/all_scorecards.json"
printf '%s\n' "${RUN_SCORECARD_PATHS[@]}" | jq -Rsc '
  split("\n")
  | map(select(length > 0))
' > "${SCORECARDS_DIR}/scorecard_paths.json"

jq -n --slurpfile paths "${SCORECARDS_DIR}/scorecard_paths.json" '
  $paths[0]
' > /dev/null

jq -n --slurpfile pathList "${SCORECARDS_DIR}/scorecard_paths.json" '
  [($pathList[0] // [])[]]
' > /dev/null

scorecards_tmp="${SCORECARDS_DIR}/_tmp_aggregate.jsonl"
: > "${scorecards_tmp}"
for p in "${RUN_SCORECARD_PATHS[@]}"; do
  cat "${p}" >> "${scorecards_tmp}"
  printf '\n' >> "${scorecards_tmp}"
done
jq -s '.' "${scorecards_tmp}" > "${ALL_SCORECARDS_JSON}"
rm -f "${scorecards_tmp}"

SUMMARY_JSON="${OUT_ROOT}/summary.json"
jq -n \
  --arg generated_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --arg policy "${QUALITY_POLICY}" \
  --arg source_baselines "${SOURCE_BASELINES}" \
  --argjson api_contract_tests "${RUN_API_CONTRACT_TESTS}" \
  --argjson real_sources_total "${real_sources_total}" \
  --argjson real_sources_passed "${real_sources_passed}" \
  --argjson fixtures_total "${fixtures_total}" \
  --argjson min_real_sources "${MIN_REAL_SOURCES}" \
  --argjson require_real_suite "${REQUIRE_REAL_SUITE}" \
  --argjson catalogue_present "$(if [[ "${catalogue_present}" == "true" ]]; then echo true; else echo false; fi)" \
  --slurpfile runs "${ALL_SCORECARDS_JSON}" '
  def avg($arr): if ($arr | length) == 0 then 0 else (($arr | add) / ($arr | length)) end;
  {
    generated_at_utc: $generated_at,
    quality_policy: $policy,
    source_baselines_policy: $source_baselines,
    suite: {
      require_real_suite: $require_real_suite,
      min_real_sources: $min_real_sources,
      real_sources_total: $real_sources_total,
      real_sources_passed: $real_sources_passed,
      fixtures_total: $fixtures_total,
      catalogue_present: $catalogue_present
    },
    runs: ($runs[0] // []),
    rollup: {
      total_runs: (($runs[0] // []) | length),
      readiness_passed: ((($runs[0] // []) | map(.gates.readiness_passed == true) | all)),
      quality_gate_passed: ((($runs[0] // []) | map(.gates.quality_gate_passed == true) | all)),
      architecture_tasks_passed: ((($runs[0] // []) | map(.gates.architecture_task_suite_passed == true) | all)),
      source_baselines_passed: ((($runs[0] // []) | map((if (.gates.source_baseline_passed == null) then true else .gates.source_baseline_passed end) == true) | all)),
      contract_gate_passed: ((($runs[0] // []) | map(((.gates.contract_gate_applicable == true) | not) or (.gates.contract_gate_passed == true)) | all)),
      explainability_passed: ((($runs[0] // []) | map(.scorecard.explainability.explainability_traceability_100 == true and .scorecard.explainability.graph_traceability_coverage_100 == true) | all)),
      completeness_sections_present: ((($runs[0] // []) | map(.scorecard.completeness.all_sections_present == true) | all)),
      completeness_min_coverage_ok: ((($runs[0] // []) | map((.scorecard.completeness.section_coverage_ratio // 0) >= 0.66) | all)),
      avg_precision: avg((($runs[0] // []) | map(.scorecard.accuracy.precision // 0))),
      avg_recall: avg((($runs[0] // []) | map(.scorecard.accuracy.recall // 0))),
      avg_f1: avg((($runs[0] // []) | map(.scorecard.accuracy.f1 // 0))),
      avg_task_pass_rate: avg((($runs[0] // []) | map(.scorecard.task_pass_rate // 0))),
      real_suite_gate_passed: (
        if $require_real_suite then
          ($catalogue_present == true)
          and ($real_sources_total >= $min_real_sources)
        else true end
      )
    }
  }
  | .overall_passed = (
      .rollup.total_runs > 0
      and .rollup.readiness_passed
      and .rollup.quality_gate_passed
      and .rollup.architecture_tasks_passed
      and .rollup.source_baselines_passed
      and .rollup.contract_gate_passed
      and .rollup.explainability_passed
      and .rollup.completeness_min_coverage_ok
      and .rollup.real_suite_gate_passed
      and .rollup.avg_task_pass_rate == 1
    )
' > "${SUMMARY_JSON}"

SUMMARY_MD="${OUT_ROOT}/summary.md"
jq -r '
  [
    "# M6 Release Gate Summary",
    "",
    ("Generated: " + .generated_at_utc),
    ("Quality policy: `" + .quality_policy + "`"),
    ("Source baselines policy: `" + (.source_baselines_policy // "") + "`"),
    "",
    "## Rollup",
    ("- Overall passed: `" + (.overall_passed|tostring) + "`"),
    ("- Total runs: `" + (.rollup.total_runs|tostring) + "`"),
    ("- Readiness passed (all): `" + (.rollup.readiness_passed|tostring) + "`"),
    ("- Quality gate passed (all): `" + (.rollup.quality_gate_passed|tostring) + "`"),
    ("- Architecture task suite passed (all): `" + (.rollup.architecture_tasks_passed|tostring) + "`"),
    ("- Source baseline gate passed (all): `" + (.rollup.source_baselines_passed|tostring) + "`"),
    ("- Contract gate passed (all applicable runs): `" + (.rollup.contract_gate_passed|tostring) + "`"),
    ("- Real suite gate passed: `" + (.rollup.real_suite_gate_passed|tostring) + "`"),
    ("- Real sources total/passed/min: `" + ((.suite.real_sources_total|tostring)) + "/" + ((.suite.real_sources_passed|tostring)) + "/" + ((.suite.min_real_sources|tostring)) + "`"),
    ("- Catalogue present in suite: `" + ((.suite.catalogue_present|tostring)) + "`"),
    ("- Explainability passed (all): `" + (.rollup.explainability_passed|tostring) + "`"),
    ("- Completeness sections present (all): `" + (.rollup.completeness_sections_present|tostring) + "`"),
    ("- Completeness min coverage >= 0.66 (all): `" + (.rollup.completeness_min_coverage_ok|tostring) + "`"),
    ("- Avg precision: `" + ((.rollup.avg_precision // 0)|tostring) + "`"),
    ("- Avg recall: `" + ((.rollup.avg_recall // 0)|tostring) + "`"),
    ("- Avg F1: `" + ((.rollup.avg_f1 // 0)|tostring) + "`"),
    ("- Avg task pass-rate: `" + ((.rollup.avg_task_pass_rate // 0)|tostring) + "`"),
    "",
    "## Runs",
    (
      (.runs // [])
      | map(
          "- `\(.source_id)`: readiness=`\(.gates.readiness_passed)`, quality=`\(.gates.quality_gate_passed)`, arch_tasks=`\(.gates.architecture_task_suite_passed)`, source_baseline=`\(.gates.source_baseline_passed // true)`, explain=`\(.scorecard.explainability.explainability_traceability_100 and .scorecard.explainability.graph_traceability_coverage_100)`, task_rate=`\(.scorecard.task_pass_rate)`, sections_ok=`\(.scorecard.completeness.all_sections_present)`"
          + ", contract_applicable=`\(.gates.contract_gate_applicable)`, contract_passed=`\(.gates.contract_gate_passed)`"
          + ", source_type=`\(.source_type // "unknown")`"
          + ", drift_f1_delta=`\((.drift.f1_delta // null)|tostring)`"
        )
      | .[]
    )
  ] | .[]
' "${SUMMARY_JSON}" > "${SUMMARY_MD}"

log "Stage 4/4: completion"
log "Summary JSON: ${SUMMARY_JSON}"
log "Summary MD: ${SUMMARY_MD}"
cat "${SUMMARY_JSON}"

if [[ "${STRICT_MODE}" == "true" ]]; then
  overall="$(jq -r '.overall_passed // false' "${SUMMARY_JSON}")"
  if [[ "${overall}" != "true" ]]; then
    fail "release gate summary did not pass"
  fi
fi
