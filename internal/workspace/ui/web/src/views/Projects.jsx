import { useEffect, useState } from 'preact/hooks'
import { listProjects, createProject, deleteProject, getSession } from '../lib/api.js'
import { canCreateProject } from '../lib/access.js'
import { navigate } from '../lib/router.js'
import { Modal, ConfirmDialog } from '../components/Modal.jsx'

// Projects is the index. When no projects exist it forces the create-project
// flow (prefilled with DEFAULT); otherwise it lists projects with open/delete.
export function Projects() {
  const [projects, setProjects] = useState(null)
  const [error, setError] = useState('')
  const [showCreate, setShowCreate] = useState(false)
  const [confirmDel, setConfirmDel] = useState(null)
  const [session, setSession] = useState(null)
  const canCreate = canCreateProject(session)

  const refresh = async () => {
    try {
      const [r, identity] = await Promise.all([listProjects(), getSession()])
      setSession(identity)
      setProjects(r.projects || [])
      setError('')
    } catch (e) {
      setError(e.message)
      setProjects([])
    }
  }
  useEffect(() => { refresh() }, [])

  // Force creation when empty.
  useEffect(() => {
    if (canCreate && projects && projects.length === 0) setShowCreate(true)
  }, [projects, canCreate])

  const onCreated = (p) => {
    setShowCreate(false)
    navigate(`/projects/${p.id}`)
  }

  const doDelete = async (id) => {
    try { await deleteProject(id) } catch (e) { setError(e.message) }
    setConfirmDel(null)
    refresh()
  }

  return (
    <div class="page">
      <header class="topbar">
        <div>
          <h1>DiffMind</h1>
          <p class="sub">Cross-service dependency graphs</p>
        </div>
        {canCreate && <button class="btn" onClick={() => setShowCreate(true)}>+ New Project</button>}
      </header>

      {error && <div class="banner error">{error}</div>}

      <div class="content">
        {projects === null && <p class="muted">Loading…</p>}
        {projects && projects.length === 0 && !showCreate && (
          <p class="muted">{canCreate ? 'No projects yet.' : 'No accessible projects. Ask an administrator to grant your user access.'}</p>
        )}
        <div class="card-grid">
          {(projects || []).map((p) => (
            <div class="card" key={p.id}>
              <div class="card-body" onClick={() => navigate(`/projects/${p.id}`)}>
                <h3>{p.name}</h3>
                <code class="muted">{p.id}</code>
                {p.instruction && <p class="muted small">{p.instruction}</p>}
              </div>
              <div class="card-actions">
                <button class="btn ghost tiny" onClick={() => navigate(`/projects/${p.id}`)}>Open</button>
                {session?.role === 'admin' && <button class="btn danger tiny" onClick={() => setConfirmDel(p)}>Delete</button>}
              </div>
            </div>
          ))}
        </div>
      </div>

      {showCreate && (
        <CreateProject
          forced={projects && projects.length === 0}
          onClose={() => { if (!(projects && projects.length === 0)) setShowCreate(false) }}
          onCreated={onCreated}
        />
      )}

      {confirmDel && (
        <ConfirmDialog
          title="Delete project?"
          message={`This permanently removes project “${confirmDel.name}” and all its repos, packs, and graph runs from disk. This cannot be undone.`}
          onConfirm={() => doDelete(confirmDel.id)}
          onCancel={() => setConfirmDel(null)}
        />
      )}
    </div>
  )
}

function CreateProject({ onClose, onCreated, forced }) {
  const [name, setName] = useState('DEFAULT')
  const [searchRoots, setSearchRoots] = useState('')
  const [instruction, setInstruction] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const submit = async () => {
    setError('')
    if (!name.trim()) { setError('Name is required.'); return }
    setBusy(true)
    try {
      const roots = searchRoots.split('\n').map((s) => s.trim()).filter(Boolean)
      const p = await createProject({ name: name.trim(), search_roots: roots, instruction: instruction.trim() })
      onCreated(p)
    } catch (e) {
      setError(e.message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal title={forced ? 'Create your first project' : 'New Project'} onClose={onClose}>
      {forced && <p class="muted small">A project is required before anything else. We suggest <code>DEFAULT</code>.</p>}
      <div class="field">
        <label>Name</label>
        <input value={name} onInput={(e) => setName(e.target.value)} />
      </div>
      <div class="field">
        <label>Repository search roots (one per line, optional)</label>
        <textarea rows="3" value={searchRoots} onInput={(e) => setSearchRoots(e.target.value)} placeholder="/path/to/repos" />
      </div>
      <div class="field">
        <label>Default extraction instruction (optional)</label>
        <textarea rows="2" value={instruction} onInput={(e) => setInstruction(e.target.value)} />
      </div>
      {error && <div class="banner error">{error}</div>}
      <div class="actions">
        <button class="btn" disabled={busy} onClick={submit}>{busy ? 'Creating…' : 'Create project'}</button>
        {!forced && <button class="btn ghost" onClick={onClose}>Cancel</button>}
      </div>
    </Modal>
  )
}
