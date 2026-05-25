// Reactive state store for the dashboard. Built on @preact/signals for
// fine-grained reactivity without React-style hooks gymnastics.
//
// The reducer applies one event at a time and updates four signals:
//   - runMeta:   id, repo, status, started/finished
//   - stages:    map of stage name -> { status, total, done, percent, ... }
//   - jobs:      map of job id -> { stage, parent, status, payload[], duration }
//   - timeline:  append-only list of events (latest last)
//
// The graph adapter and the timeline both read from these signals.

import { computed, signal } from '@preact/signals'

// STAGES_MAIN is the main pipeline row in event-emission order.
const STAGES_MAIN = ['repo_facts', 'discovery', 'reexamination', 'detail', 'index', 'connections', 'reconcile']
// STAGES_PARALLEL are stages that run in parallel WITH the main row
// rather than sequentially. They each get their own graph node above
// the main pipeline row. Today this is just the indexer image build.
const STAGES_PARALLEL = ['index.build']
const STAGES = [...STAGES_MAIN, ...STAGES_PARALLEL]

export const runMeta = signal(null)
export const stages = signal(initialStages())
export const jobs = signal(new Map())
export const timeline = signal([]) // newest events appended at the end
export const llmCalls = signal(new Map()) // by jobID
export const watchdogActions = signal([])
export const selection = signal(null) // { type: 'job'|'stage', id }
export const counts = computed(() => deriveCounts(jobs.value))

// preflight is the latest /api/preflight Report (or null while we
// have not polled yet). The SystemStatus component subscribes to
// this; the RunForm reads it to gate the Run button.
export const preflight = signal(null)

// buildLogs is a ring buffer of recent docker-build log lines for
// the parallel `index.build` stage. The DetailDrawer subscribes
// to this when the user selects the index.build node so they see
// live tail of the build output.
//
// We cap at 200 entries to keep memory bounded across long builds.
export const buildLogs = signal([])

function initialStages() {
  const m = new Map()
  for (const s of STAGES) {
    m.set(s, { name: s, status: 'pending', total: 0, done: 0, percent: 0, tip: '', startedAt: null, finishedAt: null })
  }
  return m
}

export function resetStore() {
  runMeta.value = null
  stages.value = initialStages()
  jobs.value = new Map()
  timeline.value = []
  llmCalls.value = new Map()
  watchdogActions.value = []
  selection.value = null
}

export function applyEvent(e) {
  // Append to timeline first; it's used everywhere downstream.
  const tl = timeline.value
  if (tl.length > 5000) {
    timeline.value = [...tl.slice(-4500), e]
  } else {
    timeline.value = [...tl, e]
  }

  switch (e.kind) {
    case 'run_started': {
      // CRITICAL: run_started is the FIRST event in events.jsonl, so
      // it is also the first event we see when REPLAYING a finished
      // run via SSE. If we naively wrote `status: 'running'` here we
      // would clobber the terminal status the sidebar's onReplay()
      // already set ("failed" / "cancelled" / "completed"), and the
      // user would see a "running" pill until the run_failed event
      // at the very END of replay flipped it back — minutes later.
      //
      // The rule: preserve a known-terminal status. A new (live)
      // run has no prior runMeta (or has a non-terminal one), so
      // we still set "running" in the live case.
      const prior = runMeta.value
      const isTerminal = prior && (prior.status === 'failed' || prior.status === 'completed' || prior.status === 'cancelled')
      runMeta.value = {
        ...(prior || {}),
        id: e.run_id,
        // Preserve startedAt from the live event when prior has no
        // value; never overwrite a more precise startedAt that the
        // sidebar already filled in.
        startedAt: prior?.startedAt || e.ts,
        // Keep terminal status sticky; otherwise transition to
        // running (the live-run case).
        status: isTerminal ? prior.status : 'running',
        repo: e.payload?.repo || prior?.repo,
        snapshot: e.payload?.snapshot || prior?.snapshot,
        config: e.payload || prior?.config || {},
        // Preserve any terminal-only fields the sidebar set.
        finishedAt: prior?.finishedAt,
        error: prior?.error,
        errorClass: prior?.errorClass,
      }
      stages.value = initialStages()
      jobs.value = new Map()
      llmCalls.value = new Map()
      watchdogActions.value = []
      break
    }

    case 'run_completed':
    case 'run_failed':
    case 'run_cancelled':
      {
        const status = e.kind === 'run_completed' ? 'completed' : (e.kind === 'run_failed' ? 'failed' : 'cancelled')
        const prev = runMeta.value || { id: e.run_id, startedAt: e.ts }
        runMeta.value = {
          ...prev,
          id: prev.id || e.run_id,
          finishedAt: e.ts,
          status,
          summary: e.payload || {},
          // Hoist the most actionable bits to the top so banners / TopBar
          // don't have to dig into payload every render.
          empty: !!e.payload?.empty,
          error: e.message || e.payload?.sample_error || prev.error,
          // errorClass lets the failure banner pick a class-specific
          // remediation surface (e.g. show the "fresh credentials"
          // panel by default when the failure was auth or quota).
          errorClass: e.payload?.error_class || prev.errorClass || '',
          // tokensTotal is the run-wide total; the full per-stage
          // breakdown is in payload.tokens for callers that need it
          // (DetailDrawer renders a table).
          tokens: e.payload?.tokens || prev.tokens,
          tokensTotal: e.payload?.tokens?.total?.total ?? prev.tokensTotal,
          tokensCost: e.payload?.tokens?.total?.cost ?? prev.tokensCost,
        }
      }
      // Mark any stages still showing running as either completed or
      // cancelled so the UI doesn't keep pulsing forever.
      mutateStages((m) => {
        for (const [k, st] of m) {
          if (st.status === 'running') {
            m.set(k, { ...st, status: e.kind === 'run_completed' ? 'success' : (e.kind === 'run_cancelled' ? 'cancelled' : 'failed'), finishedAt: e.ts, percent: 100 })
          }
        }
      })
      break

    case 'stage_started':
      mutateStages((m) => {
        const prev = m.get(e.stage) || {}
        m.set(e.stage, {
          ...prev,
          name: e.stage,
          status: 'running',
          total: e.payload?.total || 0,
          tip: e.payload?.tip || '',
          startedAt: e.ts,
          done: 0,
          percent: 0,
          // Batch counts (currently only emitted for the detail
          // stage). PipelineStrip renders "X/N entities · X/B
          // batches" so the user sees the LLM-cost-relevant
          // number alongside the entity tally.
          batchesTotal: e.payload?.batches_total ?? prev.batchesTotal ?? 0,
          batchesDone: 0,
          pendingEntities: e.payload?.pending ?? prev.pendingEntities ?? 0,
        })
      })
      break

    case 'stage_progress':
      mutateStages((m) => {
        const prev = m.get(e.stage) || {}
        m.set(e.stage, {
          ...prev,
          name: e.stage,
          done: e.payload?.done ?? prev.done ?? 0,
          total: e.payload?.total ?? prev.total ?? 0,
          percent: e.payload?.percent ?? prev.percent ?? 0,
          tip: e.message || prev.tip,
        })
      })
      break

    case 'stage_completed':
      mutateStages((m) => {
        const prev = m.get(e.stage) || {}
        m.set(e.stage, {
          ...prev,
          name: e.stage,
          status: e.status || 'success',
          finishedAt: e.ts,
          percent: 100,
          // Token totals get attached to the stage record so the
          // PipelineStrip can render a small "12.3k tokens" line
          // under the stage's progress bar without touching every
          // llm_call_completed event.
          tokens: e.payload?.tokens || prev.tokens,
        })
      })
      break

    case 'job_pending':
    case 'job_started':
    case 'job_completed':
    case 'job_failed':
      // Track batches separately from per-entity jobs so the
      // pipeline strip can render the dual counter ("X/N entities ·
      // X/B batches"). A batch-level job event carries
      // payload.batch === true and represents one LLM call covering
      // multiple entities.
      if (e.payload?.batch === true && (e.kind === 'job_completed' || e.kind === 'job_failed') && e.stage) {
        mutateStages((m) => {
          const prev = m.get(e.stage) || {}
          m.set(e.stage, {
            ...prev,
            batchesDone: (prev.batchesDone || 0) + 1,
          })
        })
      }
      mutateJobs((m) => {
        const id = e.job_id
        const prev = m.get(id) || {
          id,
          stage: e.stage,
          parentId: e.parent_id || null,
          status: 'pending',
          history: [],
          payload: e.payload || {},
        }
        const updated = {
          ...prev,
          stage: e.stage || prev.stage,
          parentId: e.parent_id || prev.parentId,
          status: e.status || mapJobKindToStatus(e.kind),
          message: e.message || prev.message,
          startedAt: prev.startedAt || (e.kind === 'job_started' ? e.ts : prev.startedAt),
          finishedAt: (e.kind === 'job_completed' || e.kind === 'job_failed') ? e.ts : prev.finishedAt,
          payload: { ...(prev.payload || {}), ...(e.payload || {}) },
          history: [...prev.history, { kind: e.kind, ts: e.ts, payload: e.payload, message: e.message, status: e.status }],
        }
        m.set(id, updated)
      })
      break

    case 'llm_call_started':
    case 'llm_call_completed':
      {
        const map = new Map(llmCalls.value)
        const prev = map.get(e.job_id) || { id: e.job_id, history: [] }
        map.set(e.job_id, {
          ...prev,
          status: e.status || prev.status,
          duration_ms: e.payload?.duration_ms ?? prev.duration_ms,
          session_id: e.payload?.session_id ?? prev.session_id,
          history: [...prev.history, { kind: e.kind, ts: e.ts, payload: e.payload, status: e.status, message: e.message }],
        })
        llmCalls.value = map
      }
      break

    case 'watchdog_action':
      watchdogActions.value = [...watchdogActions.value, e]
      break

    case 'log':
      // Capture image-build log lines so DetailDrawer can render
      // a live tail when the user inspects the index.build node.
      // Other 'log' events (rare today) just hit the timeline.
      if (e.stage === 'index.build' || e.job_id === 'index.build') {
        const next = [...buildLogs.value, {
          ts: e.ts,
          message: e.message || '',
          tail: e.payload?.tail || '',
        }]
        if (next.length > 200) next.splice(0, next.length - 200)
        buildLogs.value = next
      }
      break

    default:
      // session_created, session_aborted, subscriber_dropped — purely informational.
      break
  }
}

function mapJobKindToStatus(kind) {
  switch (kind) {
    case 'job_pending': return 'pending'
    case 'job_started': return 'running'
    case 'job_completed': return 'success'
    case 'job_failed': return 'failed'
    default: return 'pending'
  }
}

function mutateStages(fn) {
  const next = new Map(stages.value)
  fn(next)
  stages.value = next
}

function mutateJobs(fn) {
  const next = new Map(jobs.value)
  fn(next)
  jobs.value = next
}

function deriveCounts(j) {
  const out = { total: j.size, pending: 0, running: 0, success: 0, failed: 0 }
  for (const job of j.values()) {
    if (job.status === 'pending') out.pending++
    else if (job.status === 'running') out.running++
    else if (job.status === 'failed') out.failed++
    else out.success++
  }
  return out
}
