import { useEffect, useState } from 'preact/hooks'
import { listRuns, createRun, deleteRun, cancelRun, listRepos, diffmindRuns, runEventsURL } from '../../lib/api.js'
import { navigate } from '../../lib/router.js'
import { Modal, ConfirmDialog } from '../../components/Modal.jsx'

// RunsTab lists graph runs and hosts the New Graph Run flow. It refreshes the
// list on a slow poll plus whenever a run's SSE stream reports a terminal
// event for any active run.
export function RunsTab({ pid }) {
  const [runs, setRuns] = useState([])
  const [error, setError] = useState('')
  const [showNew, setShowNew] = useState(false)
  const [confirmDel, setConfirmDel] = useState(null)

  const refresh = async () => {
    try { setRuns((await listRuns(pid)).runs || []); setError('') }
    catch (e) { setError(e.message) }
  }
  useEffect(() => { refresh() }, [pid])

  // Poll while any run is active.
  useEffect(() => {
    const active = runs.some((r) => r.status === 'running' || r.status === 'cancelling')
    if (!active) return
    const t = setInterval(refresh, 3000)
    return () => clearInterval(t)
  }, [runs])

  const doDelete = async (rid) => {
    try { await deleteRun(pid, rid) } catch (e) { setError(e.message) }
    setConfirmDel(null); refresh()
  }
  const doCancel = async (rid) => {
    try { await cancelRun(pid, rid); refresh() } catch (e) { setError(e.message) }
  }

  return (
    <div>
      <div class="toolbar">
        <h2>Graph Runs</h2>
        <button class="btn" onClick={() => setShowNew(true)}>+ New Graph Run</button>
      </div>
      {error && <div class="banner error">{error}</div>}
      {runs.length === 0 && <p class="muted">No graph runs yet.</p>}
      <table class="data-table">
        <thead><tr><th>Run</th><th>Status</th><th>Services</th><th>Edges</th><th></th></tr></thead>
        <tbody>
          {runs.map((r) => {
            const active = r.status === 'running' || r.status === 'cancelling'
            return (
              <tr key={r.id} class="row-click" onClick={() => navigate(`/projects/${pid}/runs/${r.id}`)}>
                <td class="mono">{r.id}</td>
                <td><StatusBadge status={r.status} /></td>
                <td>{r.service_count || 0}</td>
                <td>{r.edge_count || 0}</td>
                <td class="actions-col" onClick={(e) => e.stopPropagation()}>
                  <button class="btn ghost tiny" onClick={() => navigate(`/projects/${pid}/runs/${r.id}`)}>Open</button>
                  {active && <button class="btn ghost tiny" onClick={() => doCancel(r.id)}>Cancel</button>}
                  <button class="btn danger tiny" onClick={() => setConfirmDel(r)}>Delete</button>
                </td>
              </tr>
            )
          })}
        </tbody>
      </table>

      {showNew && <NewRun pid={pid} onClose={() => setShowNew(false)} onCreated={(run) => { setShowNew(false); navigate(`/projects/${pid}/runs/${run.id}`) }} />}
      {confirmDel && (
        <ConfirmDialog
          title="Delete graph run?"
          message={`This permanently removes run ${confirmDel.id} and its graph artifacts from disk.`}
          onConfirm={() => doDelete(confirmDel.id)}
          onCancel={() => setConfirmDel(null)}
        />
      )}
    </div>
  )
}

function NewRun({ pid, onClose, onCreated }) {
  const [repos, setRepos] = useState([])
  const [choices, setChoices] = useState({}) // repoID → { runs:[], selected, enabled }
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    (async () => {
      try {
        const rs = (await listRepos(pid)).repos || []
        setRepos(rs)
        const next = {}
        for (const r of rs) {
          let runs = []
          try { runs = (await diffmindRuns(r.path)).runs || [] } catch { runs = [] }
          next[r.id] = { runs, selected: runs[0]?.run_id || '', enabled: runs.length > 0 }
        }
        setChoices(next)
      } catch (e) { setError(e.message) }
    })()
  }, [pid])

  const setSel = (rid, runID) => setChoices((c) => ({ ...c, [rid]: { ...c[rid], selected: runID } }))
  const setEnabled = (rid, on) => setChoices((c) => ({ ...c, [rid]: { ...c[rid], enabled: on } }))

  const submit = async () => {
    setError('')
    const refs = repos
      .filter((r) => choices[r.id]?.enabled && choices[r.id]?.selected)
      .map((r) => ({ repo_id: r.id, diffmind_run_id: choices[r.id].selected }))
    if (refs.length === 0) { setError('Select at least one repo with DiffMind data.'); return }
    setBusy(true)
    try { onCreated(await createRun(pid, { repos: refs })) }
    catch (e) { setError(e.message) }
    finally { setBusy(false) }
  }

  return (
    <Modal title="New Graph Run" onClose={onClose} wide>
      {repos.length === 0 && <p class="muted">No repositories in this project. Add some in the Repos tab first.</p>}
      <table class="data-table">
        <thead><tr><th>Include</th><th>Repository</th><th>DiffMind source</th></tr></thead>
        <tbody>
          {repos.map((r) => {
            const c = choices[r.id] || { runs: [], selected: '', enabled: false }
            return (
              <tr key={r.id}>
                <td><input type="checkbox" checked={c.enabled} disabled={c.runs.length === 0} onChange={(e) => setEnabled(r.id, e.target.checked)} /></td>
                <td>{r.name}<div class="muted mono small">{r.path}</div></td>
                <td>
                  {c.runs.length === 0
                    ? <span class="muted small">no DiffMind data found</span>
                    : (
                      <select value={c.selected} onChange={(e) => setSel(r.id, e.target.value)}>
                        {c.runs.map((run, i) => (
                          <option key={run.run_id} value={run.run_id}>{sourceLabel(run)}{i === 0 ? ' (default)' : ''}</option>
                        ))}
                      </select>
                    )}
                </td>
              </tr>
            )
          })}
        </tbody>
      </table>
      {error && <div class="banner error">{error}</div>}
      <div class="actions">
        <button class="btn" disabled={busy} onClick={submit}>{busy ? 'Starting…' : 'Start run'}</button>
        <button class="btn ghost" onClick={onClose}>Cancel</button>
      </div>
    </Modal>
  )
}

function sourceLabel(run) {
  if (run.source === 'archfile' || run.run_id === 'repo:diffmind.yaml') return 'diffmind.yaml'
  return run.run_id
}

export function StatusBadge({ status }) {
  if (!status) return <span class="muted">—</span>
  const colours = {
    completed: ['#062b13', '#22c55e'],
    failed: ['#3a0e11', '#ef4444'],
    cancelled: ['#3a2306', '#f59e0b'],
    cancelling: ['#3a2306', '#f59e0b'],
    running: ['#0e2240', '#4f8cff'],
  }
  const [bg, fg] = colours[status] || ['#1a2238', '#9aa6c0']
  return <span class="badge" style={`background:${bg};color:${fg};border:1px solid ${fg}44`}>{status}</span>
}

export { runEventsURL }
