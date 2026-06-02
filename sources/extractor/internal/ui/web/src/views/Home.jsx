import { useEffect, useMemo, useState } from 'preact/hooks'
import { listRuns, getConfig, cancelRun, deleteRun, ssePath } from '../lib/api.js'
import { navigate } from '../lib/router.js'
import { RunForm } from '../components/RunForm.jsx'

// Home is the runs dashboard: a sortable/filterable table of every run on
// disk, a New Run modal, and live refresh driven by the aggregate SSE stream.
export function Home() {
  const [runs, setRuns] = useState([])
  const [activeIDs, setActiveIDs] = useState([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [showForm, setShowForm] = useState(false)
  const [prefill, setPrefill] = useState(null)

  const [repoFilter, setRepoFilter] = useState('')
  const [statusFilter, setStatusFilter] = useState('all')
  const [sortKey, setSortKey] = useState('time')
  const [sortDir, setSortDir] = useState('desc')

  const [confirmDelete, setConfirmDelete] = useState(null) // run_id pending delete

  const refresh = async () => {
    setLoading(true)
    try {
      const r = await listRuns()
      setRuns(Array.isArray(r?.runs) ? r.runs : [])
      setActiveIDs(Array.isArray(r?.active_ids) ? r.active_ids : [])
      setError('')
    } catch (e) {
      setError(e.message || String(e))
    } finally {
      setLoading(false)
    }
  }

  // Initial load + load form prefill defaults once.
  useEffect(() => {
    refresh()
    getConfig().then(setPrefill).catch(() => setPrefill({}))
  }, [])

  // Aggregate lifecycle SSE: any created/started/finished event refreshes the
  // table. EventSource auto-reconnects; on hard failure we fall back to a slow
  // poll so the table never goes stale.
  useEffect(() => {
    let es = null
    let poll = null
    let stopped = false
    const open = () => {
      if (stopped) return
      es = new EventSource(ssePath('/api/events'))
      const onLifecycle = () => refresh()
      es.addEventListener('created', onLifecycle)
      es.addEventListener('started', onLifecycle)
      es.addEventListener('finished', onLifecycle)
      es.onerror = () => {
        try { es.close() } catch {}
        if (stopped) return
        if (!poll) poll = setInterval(refresh, 8000)
        setTimeout(() => { if (!stopped) { if (poll) { clearInterval(poll); poll = null } open() } }, 4000)
      }
    }
    open()
    return () => {
      stopped = true
      if (es) try { es.close() } catch {}
      if (poll) clearInterval(poll)
    }
  }, [])

  const activeSet = useMemo(() => new Set(activeIDs), [activeIDs])

  const visible = useMemo(() => {
    let list = runs.slice()
    const rf = repoFilter.trim().toLowerCase()
    if (rf) list = list.filter((r) => (r.repo_path || '').toLowerCase().includes(rf))
    if (statusFilter !== 'all') list = list.filter((r) => (r.status || 'unknown') === statusFilter)
    const dir = sortDir === 'asc' ? 1 : -1
    list.sort((a, b) => {
      let av, bv
      if (sortKey === 'repo') { av = a.repo_path || ''; bv = b.repo_path || '' }
      else if (sortKey === 'status') { av = a.status || ''; bv = b.status || '' }
      else { av = a.run_id || ''; bv = b.run_id || '' } // run_id is a sortable timestamp
      if (av < bv) return -1 * dir
      if (av > bv) return 1 * dir
      return 0
    })
    return list
  }, [runs, repoFilter, statusFilter, sortKey, sortDir])

  const toggleSort = (key) => {
    if (sortKey === key) setSortDir((d) => (d === 'asc' ? 'desc' : 'asc'))
    else { setSortKey(key); setSortDir(key === 'repo' ? 'asc' : 'desc') }
  }

  const onLaunched = (runID) => {
    setShowForm(false)
    refresh()
    if (runID) navigate(`/runs/${encodeURIComponent(runID)}`)
  }

  const doCancel = async (runID) => {
    try { await cancelRun(runID); refresh() } catch (e) { setError(e.message || String(e)) }
  }

  const doDelete = async (runID) => {
    try { await deleteRun(runID) } catch (e) { setError(e.message || String(e)) }
    setConfirmDelete(null)
    refresh()
  }

  return (
    <div class="app">
      <header class="home-header">
        <div>
          <h1>DiffMind</h1>
          <p class="home-sub">Repository artifact extraction runs</p>
        </div>
        <button class="btn" onClick={() => setShowForm(true)}>+ New Run</button>
      </header>

      <div class="home-toolbar">
        <input
          class="filter-input"
          placeholder="Filter by repository…"
          value={repoFilter}
          onInput={(e) => setRepoFilter(e.target.value)}
        />
        <select class="filter-select" value={statusFilter} onChange={(e) => setStatusFilter(e.target.value)}>
          <option value="all">All statuses</option>
          <option value="running">Running</option>
          <option value="cancelling">Cancelling</option>
          <option value="completed">Completed</option>
          <option value="failed">Failed</option>
          <option value="cancelled">Cancelled</option>
          <option value="unknown">Unknown</option>
        </select>
        <button class="btn secondary" onClick={refresh} disabled={loading}>{loading ? '…' : 'Refresh'}</button>
        <span class="home-count">{visible.length} of {runs.length} runs{activeIDs.length ? ` · ${activeIDs.length} active` : ''}</span>
      </div>

      {error && <div class="banner error">{error}</div>}

      <div class="runs-table-wrap">
        <table class="runs-table">
          <thead>
            <tr>
              <th class="sortable" onClick={() => toggleSort('time')}>Run {sortIndicator(sortKey, sortDir, 'time')}</th>
              <th class="sortable" onClick={() => toggleSort('repo')}>Repository {sortIndicator(sortKey, sortDir, 'repo')}</th>
              <th class="sortable" onClick={() => toggleSort('status')}>Status {sortIndicator(sortKey, sortDir, 'status')}</th>
              <th>Summary</th>
              <th>Duration</th>
              <th class="actions-col">Actions</th>
            </tr>
          </thead>
          <tbody>
            {visible.length === 0 && (
              <tr><td colspan="6" class="empty-row">No runs match. {runs.length === 0 ? 'Launch one with “New Run”.' : ''}</td></tr>
            )}
            {visible.map((r) => {
              const isActive = activeSet.has(r.run_id) || r.status === 'running' || r.status === 'cancelling'
              return (
                <tr key={r.run_id} class="run-row" onClick={() => navigate(`/runs/${encodeURIComponent(r.run_id)}`)}>
                  <td class="mono">{r.run_id}</td>
                  <td class="repo-cell" title={r.repo_path || ''}>{r.repo_path || '—'}</td>
                  <td><StatusBadge status={r.status} /></td>
                  <td class="muted">{summary(r.counts || {})}</td>
                  <td class="muted">{r.duration_ms ? humanDuration(r.duration_ms) : (isActive ? '…' : '—')}</td>
                  <td class="actions-col" onClick={(e) => e.stopPropagation()}>
                    <button class="btn secondary tiny" onClick={() => navigate(`/runs/${encodeURIComponent(r.run_id)}`)}>Open</button>
                    {isActive && <button class="btn secondary tiny" onClick={() => doCancel(r.run_id)}>Cancel</button>}
                    <button class="btn danger tiny" onClick={() => setConfirmDelete(r.run_id)}>Delete</button>
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>

      {showForm && (
        <Modal onClose={() => setShowForm(false)} title="New Run">
          <RunForm onLaunched={onLaunched} prefill={prefill || {}} gateOnActiveRun={false} />
        </Modal>
      )}

      {confirmDelete && (
        <ConfirmDialog
          title="Delete run?"
          message={`This permanently removes ${confirmDelete} and all its artifacts from disk. This cannot be undone.`}
          confirmLabel="Delete"
          onCancel={() => setConfirmDelete(null)}
          onConfirm={() => doDelete(confirmDelete)}
        />
      )}
    </div>
  )
}

function Modal({ title, onClose, children }) {
  return (
    <div class="modal-backdrop" onClick={onClose}>
      <div class="modal" onClick={(e) => e.stopPropagation()}>
        <div class="modal-head">
          <h2>{title}</h2>
          <button class="btn secondary tiny" onClick={onClose}>✕</button>
        </div>
        <div class="modal-body">{children}</div>
      </div>
    </div>
  )
}

function ConfirmDialog({ title, message, confirmLabel, onConfirm, onCancel }) {
  return (
    <div class="modal-backdrop" onClick={onCancel}>
      <div class="modal confirm" onClick={(e) => e.stopPropagation()}>
        <div class="modal-head"><h2>{title}</h2></div>
        <div class="modal-body">
          <p class="confirm-message">{message}</p>
          <div class="actions">
            <button class="btn danger" onClick={onConfirm}>{confirmLabel}</button>
            <button class="btn secondary" onClick={onCancel}>Cancel</button>
          </div>
        </div>
      </div>
    </div>
  )
}

function sortIndicator(key, dir, col) {
  if (key !== col) return ''
  return dir === 'asc' ? '▲' : '▼'
}

function summary(c) {
  const parts = []
  if (c.exposures) parts.push(`${c.exposures} exp`)
  if (c.dependencies) parts.push(`${c.dependencies} dep`)
  if (c.connections) parts.push(`${c.connections} conn`)
  if (c.unresolved) parts.push(`${c.unresolved} unresolved`)
  return parts.length ? parts.join(' · ') : '—'
}

function humanDuration(ms) {
  if (!Number.isFinite(ms) || ms < 0) return ''
  const s = Math.round(ms / 1000)
  if (s < 60) return s + 's'
  const m = Math.floor(s / 60)
  return m + 'm ' + (s % 60) + 's'
}

function StatusBadge({ status }) {
  if (!status) return <span class="muted">—</span>
  const colours = {
    completed: ['#062b13', '#22c55e'],
    failed: ['#3a0e11', '#ef4444'],
    cancelled: ['#3a2306', '#f59e0b'],
    running: ['#0e2240', '#4f8cff'],
    cancelling: ['#3a2306', '#f59e0b'],
  }
  const [bg, fg] = colours[status] || ['#1a2238', '#9aa6c0']
  return (
    <span style={`background:${bg};color:${fg};border:1px solid ${fg}44;border-radius:999px;padding:2px 8px;font-size:10px;font-weight:600;text-transform:uppercase;letter-spacing:0.04em`}>
      {status}
    </span>
  )
}
