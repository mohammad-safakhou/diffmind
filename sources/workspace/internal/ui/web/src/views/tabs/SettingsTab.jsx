import { useState } from 'preact/hooks'
import { patchProject, deleteProject } from '../../lib/api.js'
import { navigate } from '../../lib/router.js'
import { ConfirmDialog } from '../../components/Modal.jsx'

// SettingsTab: rename, search roots, OpenCode override, and delete project.
export function SettingsTab({ project, onChanged }) {
  const [name, setName] = useState(project.name)
  const [roots, setRoots] = useState((project.search_roots || []).join('\n'))
  const [instruction, setInstruction] = useState(project.instruction || '')
  const oc = project.opencode || {}
  const [baseURL, setBaseURL] = useState(oc.base_url || '')
  const [providerID, setProviderID] = useState(oc.provider_id || '')
  const [modelID, setModelID] = useState(oc.model_id || '')
  const [status, setStatus] = useState('')
  const [error, setError] = useState('')
  const [confirmDel, setConfirmDel] = useState(false)

  const save = async () => {
    setError(''); setStatus('')
    const patch = {
      name: name.trim(),
      search_roots: roots.split('\n').map((s) => s.trim()).filter(Boolean),
      instruction,
    }
    if (baseURL || providerID || modelID) {
      patch.opencode = { base_url: baseURL, provider_id: providerID, model_id: modelID }
    }
    try { await patchProject(project.id, patch); setStatus('Saved.'); onChanged && onChanged() }
    catch (e) { setError(e.message) }
  }

  const doDelete = async () => {
    try { await deleteProject(project.id); navigate('/') }
    catch (e) { setError(e.message); setConfirmDel(false) }
  }

  return (
    <div class="settings">
      <div class="field"><label>Name</label><input value={name} onInput={(e) => setName(e.target.value)} /></div>
      <div class="field">
        <label>Repository search roots (one per line)</label>
        <textarea rows="3" value={roots} onInput={(e) => setRoots(e.target.value)} />
      </div>
      <div class="field">
        <label>Default extraction instruction</label>
        <textarea rows="2" value={instruction} onInput={(e) => setInstruction(e.target.value)} />
      </div>

      <h3>OpenCode override (optional)</h3>
      <p class="muted small">Leave blank to use the global ~/.diffmind/config.json defaults.</p>
      <div class="row-3">
        <div class="field"><label>Base URL</label><input value={baseURL} onInput={(e) => setBaseURL(e.target.value)} /></div>
        <div class="field"><label>Provider</label><input value={providerID} onInput={(e) => setProviderID(e.target.value)} /></div>
        <div class="field"><label>Model</label><input value={modelID} onInput={(e) => setModelID(e.target.value)} /></div>
      </div>

      {error && <div class="banner error">{error}</div>}
      {status && <div class="banner ok">{status}</div>}
      <div class="actions">
        <button class="btn" onClick={save}>Save settings</button>
      </div>

      <div class="danger-zone">
        <h3>Danger zone</h3>
        <button class="btn danger" onClick={() => setConfirmDel(true)}>Delete project</button>
      </div>

      {confirmDel && (
        <ConfirmDialog
          title="Delete project?"
          message={`This permanently removes project “${project.name}” and all its repos, blueprints, and graph runs from disk. This cannot be undone.`}
          onConfirm={doDelete}
          onCancel={() => setConfirmDel(false)}
        />
      )}
    </div>
  )
}
