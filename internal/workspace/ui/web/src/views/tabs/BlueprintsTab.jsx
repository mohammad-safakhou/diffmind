import { useEffect, useState } from 'preact/hooks'
import { listBlueprints, getBlueprint, createBlueprint, putBlueprint, deleteBlueprint } from '../../lib/api.js'
import { Modal, ConfirmDialog } from '../../components/Modal.jsx'

const TEMPLATE = JSON.stringify({
  name: 'new-blueprint',
  description: '',
  version: '1',
  applies_to: { kind: 'service_repo', match: { has_file: '' } },
  extractions: [
    { name: 'identity', source: { glob: '.example/config/production/values.yaml' }, strategy: 'field_path', extract: [{ field: 'iamRole', maps_to: 'iam_role' }] },
  ],
}, null, 2)

// BlueprintsTab: list + textarea JSON editor with server-side validation.
export function BlueprintsTab({ pid }) {
  const [blueprints, setBlueprints] = useState([])
  const [error, setError] = useState('')
  const [editor, setEditor] = useState(null) // { id|null, body }
  const [confirmDel, setConfirmDel] = useState(null)

  const refresh = async () => {
    try { setBlueprints((await listBlueprints(pid)).blueprints || []); setError('') }
    catch (e) { setError(e.message) }
  }
  useEffect(() => { refresh() }, [pid])

  const openNew = () => setEditor({ id: null, body: TEMPLATE })
  const openEdit = async (id) => {
    try {
      const raw = await getBlueprint(pid, id)
      setEditor({ id, body: JSON.stringify(raw, null, 2) })
    } catch (e) { setError(e.message) }
  }

  const doDelete = async (id) => {
    try { await deleteBlueprint(pid, id) } catch (e) { setError(e.message) }
    setConfirmDel(null); refresh()
  }

  return (
    <div>
      <div class="toolbar">
        <h2>Blueprints</h2>
        <button class="btn" onClick={openNew}>+ New Blueprint</button>
      </div>
      {error && <div class="banner error">{error}</div>}
      {blueprints.length === 0 && <p class="muted">No blueprints yet.</p>}
      <table class="data-table">
        <thead><tr><th>Name</th><th>ID</th><th></th></tr></thead>
        <tbody>
          {blueprints.map((b) => (
            <tr key={b.id}>
              <td>{b.name}</td>
              <td class="mono muted small">{b.id}</td>
              <td class="actions-col">
                <button class="btn ghost tiny" onClick={() => openEdit(b.id)}>Edit</button>
                <button class="btn danger tiny" onClick={() => setConfirmDel(b)}>Delete</button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>

      {editor && <Editor pid={pid} editor={editor} onClose={() => setEditor(null)} onSaved={() => { setEditor(null); refresh() }} />}
      {confirmDel && (
        <ConfirmDialog
          title="Delete blueprint?"
          message={`This permanently removes blueprint “${confirmDel.name}” from the project.`}
          onConfirm={() => doDelete(confirmDel.id)}
          onCancel={() => setConfirmDel(null)}
        />
      )}
    </div>
  )
}

function Editor({ pid, editor, onClose, onSaved }) {
  const [body, setBody] = useState(editor.body)
  const [error, setError] = useState('')
  const [verrs, setVerrs] = useState([])
  const [busy, setBusy] = useState(false)

  const save = async () => {
    setError(''); setVerrs([]); setBusy(true)
    // Client-side JSON parse first for an instant message.
    try { JSON.parse(body) } catch (e) { setError('Invalid JSON: ' + e.message); setBusy(false); return }
    try {
      if (editor.id) await putBlueprint(pid, editor.id, body)
      else await createBlueprint(pid, body)
      onSaved()
    } catch (e) {
      setError(e.message)
      if (e.validation) setVerrs(e.validation)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal title={editor.id ? `Edit blueprint: ${editor.id}` : 'New blueprint'} onClose={onClose} wide>
      <textarea class="code-editor" rows="20" value={body} onInput={(e) => setBody(e.target.value)} spellcheck={false} />
      {error && <div class="banner error">{error}</div>}
      {verrs.length > 0 && (
        <ul class="validation-list">
          {verrs.map((v, i) => (<li key={i}><code>{v.field || '(root)'}</code>: {v.message}</li>))}
        </ul>
      )}
      <div class="actions">
        <button class="btn" disabled={busy} onClick={save}>{busy ? 'Saving…' : 'Save'}</button>
        <button class="btn ghost" onClick={onClose}>Cancel</button>
      </div>
    </Modal>
  )
}
