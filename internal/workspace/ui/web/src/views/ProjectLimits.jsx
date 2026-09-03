import { useEffect, useRef, useState } from 'preact/hooks'
import { getProjectLimits, putProjectLimits } from '../lib/api.js'
import { limitsRequest } from '../lib/limits.js'

export function ProjectLimits({ pid, canManage = false }) {
  const [data, setData] = useState(null)
  const [pending, setPending] = useState('0')
  const [workers, setWorkers] = useState('0')
  const [revision, setRevision] = useState(0)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [reloadRequired, setReloadRequired] = useState(false)
  const alive = useRef(false)
  const load = async () => {
    setBusy(true); setError(''); setNotice('')
    try {
      const next = await getProjectLimits(pid)
      if (!alive.current) return
      setData(next); setPending(String(next.limits.max_pending_jobs)); setWorkers(String(next.limits.repository_workers)); setRevision(next.limits.revision); setReloadRequired(false)
      return true
    } catch (e) { if (alive.current) { setData(null); setError(e.message) } }
    finally { if (alive.current) setBusy(false) }
  }
  useEffect(() => { alive.current = true; load(); return () => { alive.current = false } }, [pid])
  const save = async (event) => {
    event.preventDefault()
    if (busy || !canManage || reloadRequired || !data) return
    let body
    try { body = limitsRequest(revision, pending, workers) }
    catch (e) { setError(e.message); return }
    setBusy(true); setError(''); setNotice('')
    try {
      await putProjectLimits(pid, body)
      if (!alive.current) return
      // Reload the effective ceilings and usage, not just the submitted values.
      const loaded = await load()
      if (alive.current && loaded) setNotice('Project limits saved. Existing jobs and active work are retained.')
    } catch (e) {
      if (alive.current) { setReloadRequired(true); setError(`${e.message} Reload limits before saving again; the stored revision may have changed.`) }
    } finally { if (alive.current) setBusy(false) }
  }
  return <section class="operation-card" aria-label="Project resource limits">
    <h2>Project resource limits</h2>
    <p class="muted">Caps share the server’s global budget; they do not reserve capacity. Lower limits apply to new admissions while active work drains. History is never pruned.</p>
    {error && <p class="banner error" role="alert">{error}</p>}
    {notice && <p class="banner ok" role="status">{notice}</p>}
    {!data && !error && <p role="status">Loading limits…</p>}
    {data && <>
      <p>Usage snapshot: {data.pending_jobs} / {data.effective_pending_jobs} pending jobs (queued + running) · {data.active_repository_workers} / {data.effective_repository_workers} active repository operations (sync + analysis).</p>
      <p class="muted">Configured caps: {data.limits.max_pending_jobs || 'inherit'} pending jobs · {data.limits.repository_workers || 'inherit'} repository operations. Reload to update this snapshot.</p>
      {canManage ? <form onSubmit={save}>
        <div class="actions">
          <label>Pending jobs <input aria-label="Pending job limit" type="number" min="0" max="10000" step="1" value={pending} disabled={busy} onInput={(e) => setPending(e.target.value)} /></label>
          <label>Repository operations <input aria-label="Repository worker limit" type="number" min="0" max="32" step="1" value={workers} disabled={busy} onInput={(e) => setWorkers(e.target.value)} /></label>
          <button class="btn" disabled={busy || reloadRequired}>Save limits</button>
        </div>
        <p class="muted">Use 0 to inherit. Maximums: 10,000 pending jobs and 32 repository operations; the server ceiling always wins. Saving checks revision {revision}.</p>
      </form> : <p class="muted">Only a global administrator can change these limits.</p>}
    </>}
    <button class="btn ghost" disabled={busy} onClick={load}>Reload limits (discard edits)</button>
  </section>
}
