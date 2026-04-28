import { useState, useMemo } from 'preact/hooks'
import { timeline, selection } from '../lib/store.js'

const STAGE_FILTERS = ['all', 'repo_facts', 'discovery', 'reexamination', 'detail', 'connections', 'reconcile']

// Activity timeline. Lives below the graph; click a row to inspect that
// event's source job in the detail drawer.
export function Timeline() {
  const [stageFilter, setStageFilter] = useState('all')
  const [errorsOnly, setErrorsOnly] = useState(false)
  const [search, setSearch] = useState('')

  const events = timeline.value
  const filtered = useMemo(() => {
    let out = events
    if (stageFilter !== 'all') out = out.filter((e) => e.stage === stageFilter)
    if (errorsOnly) out = out.filter((e) => e.kind === 'job_failed' || e.kind === 'run_failed' || e.status === 'failed' || e.kind === 'watchdog_action')
    if (search.trim()) {
      const q = search.toLowerCase()
      out = out.filter((e) => JSON.stringify(e).toLowerCase().includes(q))
    }
    return out.slice(-500) // viewport cap; older still in the buffer.
  }, [events, stageFilter, errorsOnly, search])

  return (
    <div class="timeline" style="height: 360px; border-top: 1px solid var(--border);">
      <div class="timeline-header">
        <h2>Activity</h2>
        <div class="timeline-filters">
          {STAGE_FILTERS.map((s) => (
            <button class={'chip ' + (stageFilter === s ? 'active' : '')} onClick={() => setStageFilter(s)} key={s}>{s}</button>
          ))}
          <button class={'chip ' + (errorsOnly ? 'active' : '')} onClick={() => setErrorsOnly((v) => !v)}>errors</button>
        </div>
      </div>
      <div style="padding: 8px 14px;" class="timeline-search">
        <input placeholder="Filter events…" value={search} onInput={(e) => setSearch(e.target.value)} />
      </div>
      <ul class="timeline-list">
        {filtered.map((e) => (
          <li
            key={`${e.run_id}:${e.seq}`}
            class={rowClass(e)}
            onClick={() => onSelect(e)}
            title={e.kind + ' (' + (e.status || '') + ')'}
          >
            <span class="ts">{formatTime(e.ts)}</span>
            <span class="icon">{rowIcon(e)}</span>
            <span>
              <strong>{e.kind}</strong>
              {e.stage ? ` · ${e.stage}` : ''}
              {e.job_id ? ` · ${shortJob(e.job_id)}` : ''}
              {e.message ? ` · ${e.message}` : ''}
            </span>
          </li>
        ))}
        {filtered.length === 0 && (
          <li style="color: var(--text-muted); cursor: default;"><span /> <span /> waiting for events…</li>
        )}
      </ul>
    </div>
  )
}

function onSelect(e) {
  if (e.job_id) selection.value = { type: 'job', id: 'job:' + e.job_id }
  else if (e.stage) selection.value = { type: 'stage', id: 'stage:' + e.stage }
}

function shortJob(id) {
  if (!id) return ''
  if (id.length <= 32) return id
  return id.slice(0, 16) + '\u2026' + id.slice(-12)
}

function formatTime(ts) {
  const d = new Date(ts)
  if (isNaN(d.getTime())) return ''
  return d.toISOString().slice(11, 23)
}

function rowIcon(e) {
  if (e.kind === 'job_failed' || e.kind === 'run_failed') return '\u2716'
  if (e.kind === 'job_completed' || e.kind === 'stage_completed' || e.kind === 'run_completed') return '\u2713'
  if (e.kind === 'job_started' || e.kind === 'stage_started' || e.kind === 'run_started') return '\u25B6'
  if (e.kind === 'watchdog_action') return '\u26A0'
  if (e.kind === 'session_aborted') return '\u26D4'
  if (e.kind === 'llm_call_started' || e.kind === 'llm_call_completed') return '\u25C9'
  return '\u00B7'
}

function rowClass(e) {
  if (e.kind === 'job_failed' || e.kind === 'run_failed' || e.status === 'failed') return 'failed'
  if (e.kind === 'watchdog_action' || e.kind === 'subscriber_dropped') return 'warn'
  if (e.kind === 'run_cancelled' || e.status === 'cancelled') return 'cancelled'
  return ''
}
