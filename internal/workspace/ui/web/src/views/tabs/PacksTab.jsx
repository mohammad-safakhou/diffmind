import { useEffect, useState } from 'preact/hooks'
import { listPacks, getPack, createPack, putPack, deletePack } from '../../lib/api.js'
import { Modal, ConfirmDialog } from '../../components/Modal.jsx'

const TEMPLATE = JSON.stringify({
  api_version: 'diffmind.dev/v1alpha1',
  kind: 'KnowledgePack',
  id: 'new-pack',
  name: 'new-pack',
  description: 'Describe the repository conventions this pack teaches DiffMind.',
  version: '0.1.0',
  license: 'Apache-2.0',
  compatibility: '>=0.1.0',
  applies_to: { kind: 'service_repo', match: { has_file: '' } },
  extractions: [
    { name: 'identity', source: { glob: 'service.yaml' }, strategy: 'field_path', extract: [{ field: 'name', maps_to: 'service_name' }] },
  ],
}, null, 2)

// PacksTab: list + textarea JSON editor with server-side validation.
export function PacksTab({ pid, capabilities }) {
  const [packs, setPacks] = useState([])
  const [error, setError] = useState('')
  const [editor, setEditor] = useState(null) // { id|null, body }
  const [confirmDel, setConfirmDel] = useState(null)

  const refresh = async () => {
    try { setPacks((await listPacks(pid)).packs || []); setError('') }
    catch (e) { setError(e.message) }
  }
  useEffect(() => { refresh() }, [pid])

  const openNew = () => setEditor({ id: null, body: TEMPLATE })
  const openEdit = async (id) => {
    try {
      const raw = await getPack(pid, id)
      setEditor({ id, body: JSON.stringify(raw, null, 2) })
    } catch (e) { setError(e.message) }
  }

  const doDelete = async (id) => {
    try { await deletePack(pid, id) } catch (e) { setError(e.message) }
    setConfirmDel(null); refresh()
  }

  return (
    <div>
      <div class="toolbar">
        <h2>Packs</h2>
        <button class="btn" disabled={!capabilities?.can_configure} onClick={openNew}>+ New Pack</button>
      </div>
      {error && <div class="banner error">{error}</div>}
      {packs.length === 0 && <p class="muted">No packs yet.</p>}
      <table class="data-table">
        <thead><tr><th>Name</th><th>ID</th><th>Version</th><th>Priority</th><th></th></tr></thead>
        <tbody>
          {packs.map((b) => (
            <tr key={b.id}>
              <td>{b.name}</td>
              <td class="mono muted small">{b.id}</td>
              <td class="mono">{b.version}</td>
              <td class="mono">{b.priority || 0}</td>
              <td class="actions-col">
                <button class="btn ghost tiny" onClick={() => openEdit(b.id)}>{capabilities?.can_configure ? 'Edit' : 'View'}</button>
                <button class="btn danger tiny" disabled={!capabilities?.can_delete} onClick={() => setConfirmDel(b)}>Delete</button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>

      {editor && <Editor pid={pid} editor={editor} readOnly={!capabilities?.can_configure} onClose={() => setEditor(null)} onSaved={() => { setEditor(null); refresh() }} />}
      {confirmDel && (
        <ConfirmDialog
          title="Delete pack?"
          message={`This permanently removes pack “${confirmDel.name}” from the project.`}
          onConfirm={() => doDelete(confirmDel.id)}
          onCancel={() => setConfirmDel(null)}
        />
      )}
    </div>
  )
}

function Editor({ pid, editor, readOnly, onClose, onSaved }) {
  const [body, setBody] = useState(editor.body)
  const [error, setError] = useState('')
  const [verrs, setVerrs] = useState([])
  const [busy, setBusy] = useState(false)

  const save = async () => {
    setError(''); setVerrs([]); setBusy(true)
    // Client-side JSON parse first for an instant message.
    try { JSON.parse(body) } catch (e) { setError('Invalid JSON: ' + e.message); setBusy(false); return }
    try {
      if (editor.id) await putPack(pid, editor.id, body)
      else await createPack(pid, body)
      onSaved()
    } catch (e) {
      setError(e.message)
      if (e.validation) setVerrs(e.validation)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal title={editor.id ? `Edit pack: ${editor.id}` : 'New pack'} onClose={onClose} wide>
      <textarea class="code-editor" rows="20" value={body} readOnly={readOnly} onInput={(e) => setBody(e.target.value)} spellcheck={false} />
      {error && <div class="banner error">{error}</div>}
      {verrs.length > 0 && (
        <ul class="validation-list">
          {verrs.map((v, i) => (<li key={i}><code>{v.field || '(root)'}</code>: {v.message}</li>))}
        </ul>
      )}
      <div class="actions">
        <button class="btn" disabled={busy || readOnly} onClick={save}>{busy ? 'Saving…' : 'Save'}</button>
        <button class="btn ghost" onClick={onClose}>Cancel</button>
      </div>
    </Modal>
  )
}
