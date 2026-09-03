import { useEffect, useRef, useState } from 'preact/hooks'
import { listProjectTokens, issueProjectToken, revokeProjectToken } from '../lib/api.js'
import { tokenRequest, tokenStatus } from '../lib/tokens.js'

export function ProjectTokens({ pid }) {
  const [data, setData] = useState(null)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [busy, setBusy] = useState(false)
  const [issued, setIssued] = useState(null)
  const [pendingRevoke, setPendingRevoke] = useState(null)
  const [name, setName] = useState('')
  const [role, setRole] = useState('viewer')
  const [days, setDays] = useState(30)
  const [page, setPage] = useState(0)
  const [now, setNow] = useState(Date.now())
  const alive = useRef(false)
  const load = async () => {
    setBusy(true); setError('')
    try { const next = await listProjectTokens(pid); if (alive.current) setData(next) }
    catch (e) { if (alive.current) { setData(null); setError(e.message) } }
    finally { if (alive.current) setBusy(false) }
  }
  useEffect(() => {
    alive.current = true; load()
    const timer = setInterval(() => setNow(Date.now()), 1000)
    return () => { alive.current = false; clearInterval(timer) }
  }, [pid])
  const issue = async (event) => {
    event.preventDefault()
    setBusy(true); setError(''); setNotice('')
    try {
      const result = await issueProjectToken(pid, tokenRequest(name, role, Number(days)))
      if (!alive.current) return
      setIssued(result); setName(''); setPage(0)
      setData((previous) => ({ ...previous, tokens: [result.token, ...(previous?.tokens || [])] }))
    } catch (e) { if (alive.current) setError(`${e.message} If the response was lost, reload and revoke the new entry before issuing another token.`) }
    finally { if (alive.current) setBusy(false) }
  }
  const revoke = async (token) => {
    setBusy(true); setError(''); setNotice('')
    try {
      const result = await revokeProjectToken(pid, token.id)
      if (!alive.current) return
      setData((previous) => ({ ...previous, tokens: previous.tokens.map((t) => t.id === result.id ? result : t) }))
      if (issued?.token.id === token.id) setIssued(null)
      setPendingRevoke(null)
      setNotice('Token revoked. Already-downloaded data and admitted jobs are not withdrawn.')
    } catch (e) { if (alive.current) setError(e.message) }
    finally { if (alive.current) setBusy(false) }
  }
  return <section aria-label="Agent tokens">
    <h2>Agent tokens</h2>
    <p>Credentials for this project only. Use a viewer token for an agent; editor tokens can also queue, retry, and cancel refresh work. These service grants are independent of user memberships.</p>
    <p class="muted">Send the token as an Authorization: Bearer header to your server’s /mcp endpoint or HTTP API over HTTPS. Keep the admin recovery token private. Tokens expire and can be revoked here.</p>
    {error && <p class="banner error" role="alert">{error}</p>}{notice && <p class="banner ok" role="status">{notice}</p>}
    {pendingRevoke && <div class="banner warn" role="alert">
      <p>Revoke “{pendingRevoke.name}”? New requests will be denied. This cannot be undone.</p>
      <div class="actions"><button class="btn danger" disabled={busy} onClick={() => revoke(pendingRevoke)}>Confirm revocation</button><button class="btn ghost" disabled={busy} onClick={() => setPendingRevoke(null)}>Keep token</button></div>
    </div>}
    {issued && <div class="banner warn" role="status">
      <p>Save this secret now. It will not be shown again after you dismiss it or leave this page. Store it in your agent’s secret/environment configuration, never in a repository or URL.</p>
      <textarea aria-label="New agent token secret" readOnly rows={3} style={{ width: '100%', overflowWrap: 'anywhere' }} value={issued.secret} onFocus={(e) => e.target.select()} />
      <button class="btn ghost" onClick={() => setIssued(null)}>I saved it — hide secret</button>
    </div>}
    {data && <>
      {!data.enabled && <p class="banner warn">Token authentication and issuance are disabled in legacy mode. Enable --project-access scoped first. Existing tokens can still be revoked.</p>}
      <form onSubmit={issue}>
        <div class="actions">
          <label>Name <input aria-label="Token name" autoComplete="off" value={name} disabled={busy || !data.enabled} onInput={(e) => setName(e.target.value)} placeholder="My coding agent" /></label>
          <label>Access <select aria-label="Token role" value={role} disabled={busy || !data.enabled} onChange={(e) => setRole(e.target.value)}><option value="viewer">Viewer (recommended)</option><option value="editor">Editor</option></select></label>
          <label>Expires in <select aria-label="Token lifetime" value={days} disabled={busy || !data.enabled} onChange={(e) => setDays(Number(e.target.value))}>{[1, 7, 30, 90, 365].map((d) => <option key={d} value={d}>{d} days</option>)}</select></label>
          <button class="btn" disabled={busy || !data.enabled || !!issued}>Issue token</button>
        </div>
      </form>
      <p class="muted">{data.tokens.length} retained token records. Rotation: issue a replacement, update the client, then revoke the old token. Revoking a user membership does not revoke their separately issued tokens.</p>
      <table class="data-table"><thead><tr><th>Name / ID</th><th>Role</th><th>Expires (local time)</th><th>Status</th><th></th></tr></thead><tbody>
        {data.tokens.slice(page * 25, (page + 1) * 25).map((token) => <tr key={token.id}>
          <td>{token.name}<br /><small>{token.id}</small><br /><small>Issued by {token.created_by} · {new Date(token.created_at).toLocaleString()}</small></td>
          <td>{token.role}</td><td>{new Date(token.expires_at).toLocaleString()}</td>
          <td>{tokenStatus(token, now)}{token.revoked_at && <><br /><small>{new Date(token.revoked_at).toLocaleString()} by {token.revoked_by}</small></>}</td>
          <td><button class="btn danger tiny" disabled={busy || !!token.revoked_at} onClick={() => setPendingRevoke(token)}>Revoke</button></td>
        </tr>)}
      </tbody></table>
      {!data.tokens.length && <p>No agent tokens issued for this project.</p>}
      <div class="actions"><button class="btn ghost" disabled={busy || page === 0} onClick={() => setPage(page - 1)}>Previous tokens</button><button class="btn ghost" disabled={busy || (page + 1) * 25 >= data.tokens.length} onClick={() => setPage(page + 1)}>Next tokens</button></div>
    </>}
    <button class="btn ghost" disabled={busy} onClick={load}>Reload token status</button>
  </section>
}
