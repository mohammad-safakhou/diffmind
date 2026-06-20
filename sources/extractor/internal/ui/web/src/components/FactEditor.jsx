import { useState } from 'preact/hooks'
import { Modal, Button } from './ui/index.js'

// FactEditor is the shared structured editor for one fact (exposure,
// dependency, resource, or connection). It is used by the Review inbox and the
// graph's inline editing. onSave receives an edit object shaped for the archfile
// EditSet slice of the given kind (id / from_id / to_id already filled), with
// status+source set when "Save & verify" is used.
export function FactEditor({ kind, fact, resources = [], onSave, onClose, create = false, types = [], verifyLabel = 'Save & verify' }) {
  const [name, setName] = useState(fact.name || '')
  const [type, setType] = useState(fact.type || (types[0] || ''))
  const [summary, setSummary] = useState(fact.summary || '')
  const [platform, setPlatform] = useState(fact.platform || '')
  const [resource, setResource] = useState(fact.resource_id || fact.resource || '')
  const [condition, setCondition] = useState(conditionText(fact))
  const [detailsText, setDetailsText] = useState(prettyDetails(fact.details))
  const [error, setError] = useState('')

  const build = (verify) => {
    const edit = {}
    if (kind === 'connection') {
      edit.from_id = fact.from_exposure_id
      edit.to_id = fact.to_dependency_id
      edit.condition = condition
      edit.summary = summary
    } else {
      if (!create) edit.id = fact.id
      edit.name = name
      edit.summary = summary
      if (create && (kind === 'exposure' || kind === 'dependency')) {
        if (!type.trim()) { setError('Type is required.'); return null }
        edit.type = type.trim()
      }
      if (kind === 'resource') {
        edit.platform = platform
      }
      if (kind === 'dependency') {
        edit.platform = platform
        edit.resource = resource
      }
      if (kind === 'exposure' || kind === 'dependency') {
        if (detailsText.trim()) {
          let parsed
          try { parsed = JSON.parse(detailsText) } catch { setError('Details must be valid JSON.'); return null }
          edit.details = parsed
        }
      }
    }
    if (verify) {
      edit.status = 'verified'
      edit.source = 'manual'
    }
    return edit
  }

  const save = (verify) => {
    const edit = build(verify)
    if (!edit) return
    onSave(edit, verify)
  }

  return (
    <Modal title={`${create ? 'Add' : 'Edit'} ${kind}`} onClose={onClose}>
      {error && <div class="banner error">{error}</div>}
      <div class="fact-editor">
        {create && (kind === 'exposure' || kind === 'dependency') && (
          <label class="ui-field">
            <span class="ui-field-label">Type</span>
            {types.length
              ? <select value={type} onChange={(e) => setType(e.target.value)}>{types.map((t) => <option key={t} value={t}>{t}</option>)}</select>
              : <input value={type} placeholder="http_route, db_operation…" onInput={(e) => setType(e.target.value)} />}
          </label>
        )}
        {kind !== 'connection' && (
          <label class="ui-field">
            <span class="ui-field-label">Name</span>
            <input value={name} onInput={(e) => setName(e.target.value)} />
          </label>
        )}
        {kind === 'connection' && (
          <label class="ui-field">
            <span class="ui-field-label">Condition</span>
            <input value={condition} placeholder="unconditional" onInput={(e) => setCondition(e.target.value)} />
            <span class="ui-field-hint">Leave blank for an unconditional call.</span>
          </label>
        )}
        {(kind === 'resource' || kind === 'dependency') && (
          <label class="ui-field">
            <span class="ui-field-label">Platform</span>
            <input value={platform} placeholder="postgres, redis, sqs…" onInput={(e) => setPlatform(e.target.value)} />
          </label>
        )}
        {kind === 'dependency' && (
          <label class="ui-field">
            <span class="ui-field-label">Resource</span>
            <select value={resource} onChange={(e) => setResource(e.target.value)}>
              <option value="">(derive automatically)</option>
              {resources.map((r) => <option key={r.id} value={r.id}>{r.name || r.id}</option>)}
            </select>
          </label>
        )}
        <label class="ui-field">
          <span class="ui-field-label">Summary</span>
          <textarea value={summary} onInput={(e) => setSummary(e.target.value)} />
        </label>
        {(kind === 'exposure' || kind === 'dependency') && (
          <label class="ui-field">
            <span class="ui-field-label">Details (JSON)</span>
            <textarea class="mono" value={detailsText} onInput={(e) => setDetailsText(e.target.value)} />
          </label>
        )}
      </div>
      <div class="actions">
        <Button onClick={() => save(true)}>{verifyLabel}</Button>
        <Button variant="secondary" onClick={() => save(false)}>Save</Button>
        <Button variant="secondary" onClick={onClose}>Cancel</Button>
      </div>
    </Modal>
  )
}

function conditionText(fact) {
  const c = fact.condition
  if (!c || c.kind === 'unconditional') return ''
  return c.expression && c.expression !== 'true' ? c.expression : ''
}

function prettyDetails(details) {
  if (!details || typeof details !== 'object') return ''
  const clean = {}
  for (const [k, v] of Object.entries(details)) {
    if (['operation_normalized', 'operation_kind', 'instance', 'resource'].includes(k)) continue
    clean[k] = v
  }
  if (!Object.keys(clean).length) return ''
  return JSON.stringify(clean, null, 2)
}
