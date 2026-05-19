import { useEffect, useState } from 'preact/hooks'
import { listRuns } from '../lib/api.js'
import { runMeta } from '../lib/store.js'

// RunsSidebar lists past runs from disk and lets the user attach the live
// view to any of them. For a finished run, attaching simply replays the
// events.jsonl file via the SSE endpoint (the server transparently switches
// to JSONL replay when the run is not in memory).
export function RunsSidebar({ onPick }) {
  const [runs, setRuns] = useState([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const meta = runMeta.value

  const refresh = async () => {
    setLoading(true)
    try {
      const r = await listRuns()
      const all = Array.isArray(r?.runs) ? r.runs : []
      setRuns(all)
      setError('')
    } catch (e) {
      setError(e.message || String(e))
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    refresh()
    const t = setInterval(refresh, 8000)
    return () => clearInterval(t)
  }, [])

  // Refresh whenever the active run id changes (e.g. a fresh run finishes).
  useEffect(() => { refresh() }, [meta?.id, meta?.status])

  return (
    <div class="form" style="border-bottom: 1px solid var(--border); padding-bottom: 12px;">
      <h2 style="display:flex; justify-content: space-between; align-items: center;">
        <span>Recent runs</span>
        <button class="btn secondary" style="padding: 3px 8px; font-size: 11px;" onClick={refresh} disabled={loading}>
          {loading ? '\u2026' : 'Refresh'}
        </button>
      </h2>
      {error && <div class="banner error">{error}</div>}
      {runs.length === 0 && (
        <div class="muted" style="font-size: 12px; color: var(--text-muted);">
          No runs yet. Launch one below.
        </div>
      )}
      <div style="display:flex; flex-direction: column; gap: 4px; max-height: 220px; overflow: auto;">
        {runs.map((r) => {
          const isActive = meta?.id === r.run_id
          const counts = r.counts || {}
          return (
            <button
              key={r.run_id}
              onClick={() => onPick && onPick(r)}
              class="btn secondary"
              style={
                'text-align: left; padding: 8px 10px; line-height: 1.3; ' +
                'background: ' + (isActive ? 'var(--bg-3)' : 'var(--bg-2)') + ';' +
                'border: 1px solid ' + (isActive ? 'var(--accent)' : 'var(--border)') + ';'
              }
            >
              <div style="font-family: 'JetBrains Mono', monospace; font-size: 11px; color: var(--text); display:flex; align-items:center; gap:6px;">
                <span>{r.run_id}</span>
                <StatusBadge status={r.status} />
              </div>
              <div style="font-size: 11px; color: var(--text-muted); margin-top: 2px;">
                {summary(counts)}{r.duration_ms ? ` · ${humanDuration(r.duration_ms)}` : ''}{r.has_events ? '' : ' · (no events log)'}
              </div>
              {(r.status === 'failed' || r.status === 'cancelled') && r.failed_stage && (
                <div style="font-size: 10px; color: var(--error); margin-top: 2px;">
                  {r.status === 'cancelled' ? 'cancelled' : 'failed'} in {r.failed_stage}
                  {r.error_class && r.error_class !== 'cancelled' ? ` (${r.error_class})` : ''}
                </div>
              )}
              {r.repo_path && (
                <div style="font-size: 10px; color: var(--text-dim); margin-top: 2px; word-break: break-all;">
                  {r.repo_path}
                </div>
              )}
            </button>
          )
        })}
      </div>
    </div>
  )
}

function summary(c) {
  const parts = []
  if (c.exposures) parts.push(`${c.exposures} exp`)
  if (c.dependencies) parts.push(`${c.dependencies} dep`)
  if (c.connections) parts.push(`${c.connections} conn`)
  if (c.unresolved) parts.push(`${c.unresolved} unresolved`)
  return parts.length ? parts.join(' · ') : 'no counts'
}

// StatusBadge renders a small coloured pill next to the run id so
// the user can see at-a-glance whether a row is a successful run, a
// failed one, or a half-done unknown. Colours match the dashboard's
// status palette.
function StatusBadge({ status }) {
  if (!status || status === 'unknown') return null
  const colours = {
    completed: ['#062b13', '#22c55e'],
    failed: ['#3a0e11', '#ef4444'],
    cancelled: ['#3a2306', '#f59e0b'],
    running: ['#0e2240', '#4f8cff'],
    cancelling: ['#3a2306', '#f59e0b'],
  }
  const [bg, fg] = colours[status] || ['#1a2238', '#9aa6c0']
  return (
    <span style={`background:${bg};color:${fg};border:1px solid ${fg}44;border-radius:999px;padding:1px 6px;font-size:10px;font-weight:600;text-transform:uppercase;letter-spacing:0.04em`}>
      {status}
    </span>
  )
}

function humanDuration(ms) {
  if (!Number.isFinite(ms) || ms < 0) return ''
  const s = Math.round(ms / 1000)
  if (s < 60) return s + 's'
  const m = Math.floor(s / 60)
  return m + 'm ' + (s % 60) + 's'
}
