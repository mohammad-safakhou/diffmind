// Thin fetch wrapper: throws on non-2xx (surfacing the server's `error` and
// `validation` payloads) and parses JSON. Every API call goes through here.

export class ApiError extends Error {
  constructor(message, status, validation) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.validation = validation
  }
}

async function api(path, opts = {}) {
  const headers = { ...(opts.headers || {}) }
  if (opts.body && !(opts.rawBody)) headers['Content-Type'] = 'application/json'
  const r = await fetch(path, { ...opts, headers })
  const text = await r.text()
  let body = null
  if (text) {
    try { body = JSON.parse(text) } catch { body = text }
  }
  if (!r.ok) {
    const msg = (body && body.error) || `HTTP ${r.status}`
    throw new ApiError(msg, r.status, body && body.validation)
  }
  return body
}

const j = (v) => JSON.stringify(v)

// Projects
export const listProjects = () => api('/api/projects')
export const createProject = (p) => api('/api/projects', { method: 'POST', body: j(p) })
export const getProject = (id) => api(`/api/projects/${id}`)
export const patchProject = (id, p) => api(`/api/projects/${id}`, { method: 'PATCH', body: j(p) })
export const deleteProject = (id) => api(`/api/projects/${id}`, { method: 'DELETE' })

// Repos
export const listRepos = (pid) => api(`/api/projects/${pid}/repos`)
export const createRepo = (pid, r) => api(`/api/projects/${pid}/repos`, { method: 'POST', body: j(r) })
export const patchRepo = (pid, rid, r) => api(`/api/projects/${pid}/repos/${rid}`, { method: 'PATCH', body: j(r) })
export const deleteRepo = (pid, rid) => api(`/api/projects/${pid}/repos/${rid}`, { method: 'DELETE' })
export const repoSuggestions = (pid) => api(`/api/projects/${pid}/repo-suggestions`)

// Blueprints
export const listBlueprints = (pid) => api(`/api/projects/${pid}/blueprints`)
export const getBlueprint = (pid, bid) => api(`/api/projects/${pid}/blueprints/${bid}`)
export const createBlueprint = (pid, body) => api(`/api/projects/${pid}/blueprints`, { method: 'POST', body, rawBody: true, headers: { 'Content-Type': 'application/json' } })
export const putBlueprint = (pid, bid, body) => api(`/api/projects/${pid}/blueprints/${bid}`, { method: 'PUT', body, rawBody: true, headers: { 'Content-Type': 'application/json' } })
export const deleteBlueprint = (pid, bid) => api(`/api/projects/${pid}/blueprints/${bid}`, { method: 'DELETE' })

// DiffMind run discovery
export const diffmindRuns = (repoPath) => api('/api/diffmind-runs' + (repoPath ? `?repo_path=${encodeURIComponent(repoPath)}` : ''))

// Graph runs
export const listRuns = (pid) => api(`/api/projects/${pid}/runs`)
export const createRun = (pid, body) => api(`/api/projects/${pid}/runs`, { method: 'POST', body: j(body) })
export const getRun = (pid, rid) => api(`/api/projects/${pid}/runs/${rid}`)
export const cancelRun = (pid, rid) => api(`/api/projects/${pid}/runs/${rid}/cancel`, { method: 'POST' })
export const deleteRun = (pid, rid) => api(`/api/projects/${pid}/runs/${rid}`, { method: 'DELETE' })
export const getRunGraph = (pid, rid) => api(`/api/projects/${pid}/runs/${rid}/graph`)
export const runEventsURL = (pid, rid) => `/api/projects/${pid}/runs/${rid}/events`
