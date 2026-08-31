// Tiny fetch wrapper that throws on non-2xx and parses JSON. Every call in
// the dashboard goes through here so error handling stays uniform.

const TOKEN_KEY = 'diffmind:ui-token'

// Token storage helpers. The token is purely client-side; the server
// validates a header on every request when --ui-token is set.
export function getToken() {
  try { return localStorage.getItem(TOKEN_KEY) || '' } catch { return '' }
}
export function setToken(v) {
  try {
    if (v) localStorage.setItem(TOKEN_KEY, v)
    else localStorage.removeItem(TOKEN_KEY)
  } catch {}
}

// On boot: if a `?token=...` is present in the URL, persist it and clean
// up the URL so it doesn't end up in browser history.
;(function initToken() {
  if (typeof window === 'undefined') return
  const url = new URL(window.location.href)
  const t = url.searchParams.get('token')
  if (t) {
    setToken(t)
    url.searchParams.delete('token')
    window.history.replaceState({}, document.title, url.pathname + (url.search ? url.search : ''))
  }
})()

// A signal-style listener so components can react to auth failures.
let authFailureHandler = null
export function onAuthFailure(fn) { authFailureHandler = fn }

export class UnauthorizedError extends Error {
  constructor(msg) { super(msg); this.name = 'UnauthorizedError' }
}

export async function api(path, opts = {}) {
  const headers = { 'Content-Type': 'application/json', ...(opts.headers || {}) }
  const token = getToken()
  if (token) headers['X-DiffMind-Token'] = token
  const r = await fetch(path, { ...opts, headers })
  if (r.status === 401) {
    if (authFailureHandler) authFailureHandler()
    throw new UnauthorizedError('unauthorized')
  }
  const text = await r.text()
  let body = null
  if (text) {
    try { body = JSON.parse(text) } catch { body = text }
  }
  if (!r.ok) {
    const msg = (body && body.error) || `HTTP ${r.status}`
    throw new Error(msg)
  }
  return body
}

// SSE doesn't carry custom headers easily; we pass the token via query
// string. The server accepts either, so this works in both modes.
export function ssePath(path) {
  const t = getToken()
  if (!t) return path
  return path + (path.includes('?') ? '&' : '?') + 'token=' + encodeURIComponent(t)
}

export function startRun(payload) {
  return api('/api/runs', { method: 'POST', body: JSON.stringify(payload) })
}

export function getActive() {
  return api('/api/runs/active')
}

export function listRuns() {
  return api('/api/runs')
}

export function getRunState(runID) {
  return api(`/api/runs/${encodeURIComponent(runID)}/state`)
}

// cancelRun stops a single in-flight run. The backend treats this as
// idempotent (cancelling an unknown/finished run is a 200 no-op).
export function cancelRun(runID) {
  return api(`/api/runs/${encodeURIComponent(runID)}/cancel`, { method: 'POST' })
}

// deleteRun removes a run and its artifacts from disk. The caller MUST confirm
// with the user first — this is irreversible.
export function deleteRun(runID) {
  return api(`/api/runs/${encodeURIComponent(runID)}`, { method: 'DELETE' })
}

// getConfig returns the New Run form defaults sourced from ~/.diffmind/config.json.
export function getConfig() {
  return api('/api/config')
}

export function getJob(runID, jobID) {
  return api(`/api/runs/${encodeURIComponent(runID)}/job/${encodeURIComponent(jobID)}`)
}

export function getRunArtifact(runID) {
  return api(`/api/run/${encodeURIComponent(runID)}`)
}

// getRunArtifacts is the new alias served under /api/runs/{id}/artifacts.
// Functionally identical to getRunArtifact; we expose both so the SPA can
// stay on the unified /api/runs/* prefix.
export function getRunArtifacts(runID) {
  return api(`/api/runs/${encodeURIComponent(runID)}/artifacts`)
}

// getRunGraph returns the versioned graph.v1 export for downstream tools
// and the sequence-tree panel.
export function getRunGraph(runID) {
  return api(`/api/runs/${encodeURIComponent(runID)}/graph`)
}

// First-class repositories.
export function listRepos() {
  return api('/api/repos')
}

export function upsertRepo(body) {
  return api('/api/repos', { method: 'POST', body: JSON.stringify(body) })
}

export function deleteRepo(id) {
  return api('/api/repos/' + encodeURIComponent(id), { method: 'DELETE' })
}

// Preflight API. The dashboard's SystemStatus panel polls /api/preflight.
export function getPreflight() {
  return api('/api/preflight')
}
