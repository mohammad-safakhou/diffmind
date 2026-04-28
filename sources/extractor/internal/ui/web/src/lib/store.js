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

const STAGES = ['repo_facts', 'discovery', 'reexamination', 'detail', 'connections', 'reconcile']

export const runMeta = signal(null)
export const stages = signal(initialStages())
export const jobs = signal(new Map())
export const timeline = signal([]) // newest events appended at the end
export const llmCalls = signal(new Map()) // by jobID
export const watchdogActions = signal([])
export const selection = signal(null) // { type: 'job'|'stage', id }
export const counts = computed(() => deriveCounts(jobs.value))

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
    case 'run_started':
      runMeta.value = {
        id: e.run_id,
        startedAt: e.ts,
        status: 'running',
        repo: e.payload?.repo,
        snapshot: e.payload?.snapshot,
        config: e.payload || {},
      }
      stages.value = initialStages()
      jobs.value = new Map()
      llmCalls.value = new Map()
      watchdogActions.value = []
      break

    case 'run_completed':
    case 'run_failed':
    case 'run_cancelled':
      if (runMeta.value) {
        runMeta.value = {
          ...runMeta.value,
          finishedAt: e.ts,
          status: e.kind === 'run_completed' ? 'completed' : (e.kind === 'run_failed' ? 'failed' : 'cancelled'),
          summary: e.payload || {},
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
        })
      })
      break

    case 'job_pending':
    case 'job_started':
    case 'job_completed':
    case 'job_failed':
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

    default:
      // session_created, session_aborted, log, subscriber_dropped — purely informational.
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
