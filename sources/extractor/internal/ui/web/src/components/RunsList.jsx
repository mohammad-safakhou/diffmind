import { useEffect, useMemo, useState } from 'preact/hooks'
import { listRuns, cancelRun, deleteRun, ssePath } from '../lib/api.js'
import { navigate } from '../lib/router.js'
import { Button, StatusBadge, ConfirmDialog, EmptyState, useToast } from './ui/index.js'

// RunsList is the sortable/filterable runs table with live SSE refresh, extracted
// from the old Home view so both the global Runs page and a repository's Runs tab
// reuse it. Pass `lockedRepo` (a repo_path) to scope it to one repository and hide
// the repo controls.
export function RunsList({ lockedRepo = null }) {
  const toast = useToast()
  const [runs, setRuns] = useState([])
  const [activeIDs, setActiveIDs] = useState([])
  const [loading, setLoading] = useState(false)
  const [repoFilter, setRepoFilter] = useState('')
  const [statusFilter, setStatusFilter] = useState('all')
  const [sortKey, setSortKey] = useState('time')
  const [sortDir, setSortDir] = useState('desc')
  const [confirmDelete, setConfirmDelete] = useState(null)

  const refresh = async () => {
    setLoading(true)
    try {
      const r = await listRuns()
      setRuns(Array.isArray(r?.runs) ? r.runs : [])
      setActiveIDs(Array.isArray(r?.active_ids) ? r.active_ids : [])
    } catch (e) {
      toast.error(e.message || String(e))
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { refresh() }, [])

  useEffect(() => {
    let es = null, poll = null, stopped = false
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
    if (lockedRepo) list = list.filter((r) => (r.repo_path || '') === lockedRepo)
    const rf = repoFilter.trim().toLowerCase()
    if (rf) list = list.filter((r) => (r.repo_path || '').toLowerCase().includes(rf))
    if (statusFilter !== 'all') list = list.filter((r) => (r.status || 'unknown') === statusFilter)
    const dir = sortDir === 'asc' ? 1 : -1
    list.sort((a, b) => {
      let av, bv
      if (sortKey === 'repo') { av = a.repo_path || ''; bv = b.repo_path || '' }
      else if (sortKey === 'status') { av = a.status || ''; bv = b.status || '' }
      else { av = a.run_id || ''; bv = b.run_id || '' }
      if (av < bv) return -1 * dir
      if (av > bv) return 1 * dir
      return 0
    })
    return list
  }, [runs, repoFilter, statusFilter, sortKey, sortDir, lockedRepo])

  const toggleSort = (key) => {
    if (sortKey === key) setSortDir((d) => (d === 'asc' ? 'desc' : 'asc'))
    else { setSortKey(key); setSortDir(key === 'repo' ? 'asc' : 'desc') }
  }

  const doCancel = async (runID) => {
    try { await cancelRun(runID); refresh() } catch (e) { toast.error(e.message || String(e)) }
  }
  const doDelete = async (runID) => {
    try { await deleteRun(runID); toast.success('Run deleted.') } catch (e) { toast.error(e.message || String(e)) }
    setConfirmDelete(null)
    refresh()
  }

  return (
    <div>
      <div class="home-toolbar">
        {!lockedRepo && (
          <input
            class="filter-input"
            placeholder="Filter by repository…"
            value={repoFilter}
            onInput={(e) => setRepoFilter(e.target.value)}
          />
        )}
        <select class="filter-select" value={statusFilter} onChange={(e) => setStatusFilter(e.target.value)}>
          <option value="all">All statuses</option>
          <option value="running">Running</option>
          <option value="cancelling">Cancelling</option>
          <option value="completed">Completed</option>
          <option value="failed">Failed</option>
          <option value="cancelled">Cancelled</option>
          <option value="unknown">Unknown</option>
        </select>
        <Button variant="secondary" onClick={refresh} disabled={loading}>{loading ? '…' : 'Refresh'}</Button>
        <span class="home-count">{visible.length} of {runs.length} runs{activeIDs.length ? ` · ${activeIDs.length} active` : ''}</span>
      </div>

      <div class="runs-table-wrap">
        <table class="runs-table">
          <thead>
            <tr>
              <th class="sortable" onClick={() => toggleSort('time')}>Run {sortIndicator(sortKey, sortDir, 'time')}</th>
              {!lockedRepo && <th class="sortable" onClick={() => toggleSort('repo')}>Repository {sortIndicator(sortKey, sortDir, 'repo')}</th>}
              <th class="sortable" onClick={() => toggleSort('status')}>Status {sortIndicator(sortKey, sortDir, 'status')}</th>
              <th>Summary</th>
              <th>Duration</th>
              <th class="actions-col">Actions</th>
            </tr>
          </thead>
          <tbody>
            {visible.length === 0 && (
              <tr><td colspan={lockedRepo ? 5 : 6} class="empty-row">No runs yet.</td></tr>
            )}
            {visible.map((r) => {
              const isActive = activeSet.has(r.run_id) || r.status === 'running' || r.status === 'cancelling'
              return (
                <tr key={r.run_id} class="run-row" onClick={() => navigate(`/runs/${encodeURIComponent(r.run_id)}`)}>
                  <td class="mono">{r.run_id}</td>
                  {!lockedRepo && <td class="repo-cell" title={r.repo_path || ''}>{r.repo_path || '—'}</td>}
                  <td><StatusBadge status={r.status} /></td>
                  <td class="muted">{summary(r.counts || {})}</td>
                  <td class="muted">{r.duration_ms ? humanDuration(r.duration_ms) : (isActive ? '…' : '—')}</td>
                  <td class="actions-col" onClick={(e) => e.stopPropagation()}>
                    <Button variant="secondary" size="tiny" onClick={() => navigate(`/runs/${encodeURIComponent(r.run_id)}`)}>Open</Button>
                    {isActive && <Button variant="secondary" size="tiny" onClick={() => doCancel(r.run_id)}>Cancel</Button>}
                    <Button variant="danger" size="tiny" onClick={() => setConfirmDelete(r.run_id)}>Delete</Button>
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>

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

function sortIndicator(key, dir, col) {
  if (key !== col) return ''
  return dir === 'asc' ? '▲' : '▼'
}

export function summary(c) {
  const parts = []
  if (c.exposures) parts.push(`${c.exposures} exp`)
  if (c.dependencies) parts.push(`${c.dependencies} dep`)
  if (c.connections) parts.push(`${c.connections} conn`)
  if (c.unresolved) parts.push(`${c.unresolved} unresolved`)
  return parts.length ? parts.join(' · ') : '—'
}

export function humanDuration(ms) {
  if (!Number.isFinite(ms) || ms < 0) return ''
  const s = Math.round(ms / 1000)
  if (s < 60) return s + 's'
  const m = Math.floor(s / 60)
  return m + 'm ' + (s % 60) + 's'
}
