import { useEffect, useState } from 'preact/hooks'
import { getCapabilities, getProjectAccess, putProjectAccess } from '../lib/api.js'
import { membersFromRows } from '../lib/access.js'
import { navigate } from '../lib/router.js'
import { ProjectTokens } from './ProjectTokens.jsx'
import './Operations.css'

export function ProjectAccess({ pid }) {
  const [policy, setPolicy] = useState(null)
  const [caps, setCaps] = useState(null)
  const [rows, setRows] = useState([])
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [busy, setBusy] = useState(false)
  const apply = (value) => { setPolicy(value); setRows(Object.entries(value.members).sort(([a], [b]) => a.localeCompare(b)).map(([subject, role]) => ({ subject, role }))) }
  const load = async () => {
    setBusy(true); setError(''); setNotice('')
    try { const [c, p] = await Promise.all([getCapabilities(pid), getProjectAccess(pid)]); setCaps(c); apply(p) }
    catch (e) { setError(e.message); setPolicy(null); setCaps(null) }
    finally { setBusy(false) }
  }
  useEffect(() => { load() }, [pid])
  const save = async () => {
    setBusy(true); setError(''); setNotice('')
    try { apply(await putProjectAccess(pid, { revision: policy.revision, members: membersFromRows(rows) })); setNotice('Access saved. New requests use these grants immediately.') }
    catch (e) { setError(e.message) }
    finally { setBusy(false) }
  }
  const update = (index, patch) => setRows(rows.map((row, i) => i === index ? { ...row, ...patch } : row))
  return <main class="operations-page">
    <header class="operations-header"><button class="btn ghost" onClick={() => navigate(`/projects/${encodeURIComponent(pid)}`)}>← {pid}</button><div><h1>Project access</h1><p class="muted">User memberships and project-scoped agent tokens. Global admins retain recovery access.</p></div></header>
    {error && <p class="banner error">{error}</p>}{notice && <p class="banner ok">{notice}</p>}
    {policy && <>
      {caps?.mode === 'legacy' && <p class="banner warn">Legacy mode: grants are saved but not enforced. Start with --project-access scoped to activate restrictions.</p>}
      <p>Viewers can query this project. Editors can also queue, retry, and cancel refresh work. Host paths, imports, packs, configuration, and access changes are admin-only in scoped mode.</p>
      <p class="muted">Use the exact stable X-DiffMind-User subject. The proxy role limits each grant. No members means no proxy-user access in scoped mode; agent tokens below are separate grants.</p>
      <table class="data-table"><thead><tr><th>Proxy subject</th><th>Project role</th><th></th></tr></thead><tbody>{rows.map((row, i) => <tr key={i}><td><input aria-label={`Member ${i + 1} subject`} value={row.subject} disabled={busy} onInput={(e) => update(i, { subject: e.target.value })} /></td><td><select aria-label={`Member ${i + 1} role`} value={row.role} disabled={busy} onChange={(e) => update(i, { role: e.target.value })}><option value="viewer">Viewer</option><option value="editor">Editor</option></select></td><td><button class="btn danger tiny" disabled={busy} onClick={() => setRows(rows.filter((_, index) => index !== i))}>Remove member</button></td></tr>)}</tbody></table>
      <div class="actions"><button class="btn ghost" disabled={busy || rows.length >= 1000} onClick={() => setRows([...rows, { subject: '', role: 'viewer' }])}>Add member</button><button class="btn" disabled={busy || !caps?.can_manage_access} onClick={save}>{busy ? 'Working…' : 'Save access'}</button><button class="btn ghost" disabled={busy} onClick={load}>Reload policy</button></div>
      <p class="muted">Revision {policy.revision}. Concurrent changes cause a conflict. Reload discards your unsaved edits; review before saving again.</p>
      {caps?.can_manage_access && <ProjectTokens key={pid} pid={pid} />}
    </>}
  </main>
}
