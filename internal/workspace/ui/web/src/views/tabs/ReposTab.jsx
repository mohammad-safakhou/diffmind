import { useEffect, useState } from 'preact/hooks'
import { listRepos, createRepo, patchRepo, deleteRepo, repoSuggestions, listBlueprints } from '../../lib/api.js'
import { Modal, ConfirmDialog } from '../../components/Modal.jsx'

// ReposTab: add/remove repos, autosuggest from search roots, and per-repo
// blueprint/instruction overrides.
export function ReposTab({ pid }) {
  const [repos, setRepos] = useState([])
  const [error, setError] = useState('')
  const [showAdd, setShowAdd] = useState(false)
  const [edit, setEdit] = useState(null)
  const [confirmDel, setConfirmDel] = useState(null)

  const refresh = async () => {
    try { setRepos((await listRepos(pid)).repos || []); setError('') }
    catch (e) { setError(e.message) }
  }
  useEffect(() => { refresh() }, [pid])

  const doDelete = async (rid) => {
    try { await deleteRepo(pid, rid) } catch (e) { setError(e.message) }
    setConfirmDel(null); refresh()
  }

  return (
    <div>
      <div class="toolbar">
        <h2>Repositories</h2>
        <button class="btn" onClick={() => setShowAdd(true)}>+ Add Repo</button>
      </div>
      {error && <div class="banner error">{error}</div>}
      {repos.length === 0 && <p class="muted">No repositories yet.</p>}
      <table class="data-table">
        <thead><tr><th>Name</th><th>Path</th><th>Kind</th><th>Overrides</th><th></th></tr></thead>
        <tbody>
          {repos.map((r) => (
            <tr key={r.id}>
              <td>{r.name}</td>
              <td class="muted mono small" title={r.path}>{r.path}</td>
              <td>{r.kind}</td>
              <td class="muted small">
                {(r.blueprint_ids && r.blueprint_ids.length) ? `${r.blueprint_ids.length} blueprint(s)` : ''}
                {r.instruction ? ' · instruction' : ''}
              </td>
              <td class="actions-col">
                <button class="btn ghost tiny" onClick={() => setEdit(r)}>Edit</button>
                <button class="btn danger tiny" onClick={() => setConfirmDel(r)}>Remove</button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>

      {showAdd && <AddRepo pid={pid} onClose={() => setShowAdd(false)} onAdded={() => { setShowAdd(false); refresh() }} />}
      {edit && <EditRepo pid={pid} repo={edit} onClose={() => setEdit(null)} onSaved={() => { setEdit(null); refresh() }} />}
      {confirmDel && (
        <ConfirmDialog
          title="Remove repository?"
          message={`This removes DiffMind's metadata for “${confirmDel.name}”. The source repository on disk is NOT touched.`}
          confirmLabel="Remove"
          onConfirm={() => doDelete(confirmDel.id)}
          onCancel={() => setConfirmDel(null)}
        />
      )}
    </div>
  )
}

function AddRepo({ pid, onClose, onAdded }) {
  const [path, setPath] = useState('')
  const [name, setName] = useState('')
  const [kind, setKind] = useState('service_repo')
  const [suggestions, setSuggestions] = useState([])
  const [roots, setRoots] = useState([])
  const [error, setError] = useState('')

  useEffect(() => {
    repoSuggestions(pid).then((r) => { setSuggestions(r.suggestions || []); setRoots(r.roots || []) }).catch(() => {})
  }, [pid])

  const submit = async () => {
    setError('')
    if (!path.trim()) { setError('Path is required.'); return }
    try {
      await createRepo(pid, { path: path.trim(), name: name.trim(), kind })
      onAdded()
    } catch (e) { setError(e.message) }
  }

  return (
    <Modal title="Add Repository" onClose={onClose}>
      {suggestions.length > 0 && (
        <div class="field">
          <label>Suggestions {roots.length ? `(from ${roots.join(', ')})` : ''}</label>
          <div class="suggestion-list">
            {suggestions.map((s) => (
              <button key={s.path} class="btn ghost tiny" onClick={() => { setPath(s.path); setName(s.name) }}>{s.name}</button>
            ))}
          </div>
        </div>
      )}
      {suggestions.length === 0 && roots.length === 0 && (
        <p class="muted small">No search roots configured. Set them in the project Settings tab, or enter a path manually below.</p>
      )}
      <div class="field"><label>Path</label><input value={path} onInput={(e) => setPath(e.target.value)} placeholder="/abs/path/to/repo" /></div>
      <div class="field"><label>Name (optional)</label><input value={name} onInput={(e) => setName(e.target.value)} /></div>
      <div class="field">
        <label>Kind</label>
        <select value={kind} onChange={(e) => setKind(e.target.value)}>
          <option value="service_repo">service_repo</option>
          <option value="infra_repo">infra_repo</option>
        </select>
      </div>
      {error && <div class="banner error">{error}</div>}
      <div class="actions">
        <button class="btn" onClick={submit}>Add</button>
        <button class="btn ghost" onClick={onClose}>Cancel</button>
      </div>
    </Modal>
  )
}

function EditRepo({ pid, repo, onClose, onSaved }) {
  const [instruction, setInstruction] = useState(repo.instruction || '')
  const [selected, setSelected] = useState(new Set(repo.blueprint_ids || []))
  const [blueprints, setBlueprints] = useState([])
  const [error, setError] = useState('')

  useEffect(() => { listBlueprints(pid).then((r) => setBlueprints(r.blueprints || [])).catch(() => {}) }, [pid])

  const toggle = (id) => {
    const next = new Set(selected)
    next.has(id) ? next.delete(id) : next.add(id)
    setSelected(next)
  }

  const submit = async () => {
    try {
      await patchRepo(pid, repo.id, { instruction, blueprint_ids: [...selected] })
      onSaved()
    } catch (e) { setError(e.message) }
  }

  return (
    <Modal title={`Edit ${repo.name}`} onClose={onClose}>
      <div class="field">
        <label>Blueprint overrides (empty = use project matching)</label>
        <div class="check-list">
          {blueprints.length === 0 && <p class="muted small">No project blueprints defined.</p>}
          {blueprints.map((b) => (
            <label class="check" key={b.id}>
              <input type="checkbox" checked={selected.has(b.id)} onChange={() => toggle(b.id)} /> {b.name}
            </label>
          ))}
        </div>
      </div>
      <div class="field">
        <label>Instruction override (empty = use project default)</label>
        <textarea rows="3" value={instruction} onInput={(e) => setInstruction(e.target.value)} />
      </div>
      {error && <div class="banner error">{error}</div>}
      <div class="actions">
        <button class="btn" onClick={submit}>Save</button>
        <button class="btn ghost" onClick={onClose}>Cancel</button>
      </div>
    </Modal>
  )
}
