import { useEffect, useMemo, useState } from 'preact/hooks'
import {
  getArchitecture,
  importArchitectureRun,
  listRuns,
  saveArchitecture,
} from '../lib/api.js'
import { navigate } from '../lib/router.js'
import { OutcomeGraph } from '../components/OutcomeGraph.jsx'

export function Architecture() {
  const [doc, setDoc] = useState(null)
  const [runs, setRuns] = useState([])
  const [selectedRun, setSelectedRun] = useState('')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [editor, setEditor] = useState(null)
  const [showGraph, setShowGraph] = useState(false)

  const load = async () => {
    setLoading(true)
    try {
      const [architecture, runData] = await Promise.all([getArchitecture(), listRuns()])
      setDoc(architecture)
      const available = Array.isArray(runData?.runs)
        ? runData.runs.filter((run) => run.status === 'completed')
        : []
      setRuns(available)
      setSelectedRun((current) => current || available[0]?.run_id || '')
      setError('')
    } catch (e) {
      setError(e.message || String(e))
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { load() }, [])

  const persist = async (next, message) => {
    try {
      const saved = await saveArchitecture(next)
      setDoc(saved)
      setNotice(message)
      setError('')
      return true
    } catch (e) {
      setError(e.message || String(e))
      if ((e.message || '').includes('revision conflict')) {
        await load()
      }
      return false
    }
  }

  const importRun = async () => {
    if (!selectedRun) return
    try {
      const result = await importArchitectureRun(selectedRun)
      setDoc(result.document)
      const s = result.summary || {}
      setNotice(`Imported ${selectedRun}: ${s.added || 0} added, ${s.updated || 0} updated, ${s.skipped_manual || 0} manual records protected.`)
      setError('')
    } catch (e) {
      setError(e.message || String(e))
    }
  }

  const removeNode = async (kind, id) => {
    const key = kind === 'exposure' ? 'exposures' : 'dependencies'
    const next = {
      ...doc,
      [key]: doc[key].filter((item) => item.id !== id),
      connections: doc.connections.filter((c) =>
        c.from_exposure_id !== id && c.to_dependency_id !== id
      ),
    }
    await persist(next, 'Node removed.')
  }

  const removeConnection = async (id) => {
    await persist({
      ...doc,
      connections: doc.connections.filter((c) => c.id !== id),
    }, 'Connection removed.')
  }

  if (loading && !doc) return <div class="catalog-loading">Loading architecture catalog…</div>
  if (!doc) return <div class="catalog-loading error">{error || 'Architecture catalog unavailable.'}</div>

  return (
    <div class="app catalog-app">
      <header class="catalog-header">
        <div>
          <div class="catalog-eyebrow">Architecture source of truth</div>
          <h1>{doc.name || 'DiffMind Architecture'}</h1>
          <p>{doc.description || 'Build and curate the graph directly. Completed automation runs can be imported.'}</p>
        </div>
        <div class="catalog-header-actions">
          <button class="btn secondary" onClick={() => navigate('/runs')}>Automation runs</button>
          <button class="btn secondary" onClick={() => setShowGraph(true)}>Visual graph</button>
        </div>
      </header>

      <section class="catalog-toolbar">
        <div class="catalog-import">
          <select value={selectedRun} onChange={(e) => setSelectedRun(e.target.value)}>
            <option value="">Select completed run…</option>
            {runs.map((r) => <option value={r.run_id} key={r.run_id}>{r.run_id} · {shortRepo(r.repo_path)}</option>)}
          </select>
          <button class="btn" disabled={!selectedRun} onClick={importRun}>Import run</button>
        </div>
        <div class="catalog-revision">revision {doc.revision} · {doc.exposures.length + doc.dependencies.length} nodes · {doc.connections.length} connections</div>
      </section>

      {error && <div class="banner error">{error}</div>}
      {notice && <div class="banner success">{notice}</div>}

      <main class="catalog-grid">
        <CatalogColumn
          title="Exposures"
          subtitle="How systems and users enter"
          items={doc.exposures}
          records={doc.records}
          onAdd={() => setEditor({ mode: 'node', kind: 'exposure', item: emptyNode('exposure') })}
          onEdit={(item) => setEditor({ mode: 'node', kind: 'exposure', item })}
          onDelete={(id) => removeNode('exposure', id)}
        />
        <CatalogColumn
          title="Dependencies"
          subtitle="External systems and operations"
          items={doc.dependencies}
          records={doc.records}
          onAdd={() => setEditor({ mode: 'node', kind: 'dependency', item: emptyNode('dependency') })}
          onEdit={(item) => setEditor({ mode: 'node', kind: 'dependency', item })}
          onDelete={(id) => removeNode('dependency', id)}
        />
      </main>

      <section class="catalog-connections">
        <div class="catalog-section-head">
          <div>
            <h2>Connections</h2>
            <p>Define which exposure reaches which dependency and under what condition.</p>
          </div>
          <button
            class="btn"
            disabled={!doc.exposures.length || !doc.dependencies.length}
            onClick={() => setEditor({ mode: 'connection', item: emptyConnection(doc) })}
          >
            + Connection
          </button>
        </div>
        <ConnectionTable
          doc={doc}
          onEdit={(item) => setEditor({ mode: 'connection', item })}
          onDelete={removeConnection}
        />
      </section>

      {editor?.mode === 'node' && (
        <NodeEditor
          kind={editor.kind}
          item={editor.item}
          onClose={() => setEditor(null)}
          onSave={async (item) => {
            const key = editor.kind === 'exposure' ? 'exposures' : 'dependencies'
            const exists = doc[key].some((v) => v.id === item.id)
            const nextItems = exists
              ? doc[key].map((v) => v.id === item.id ? item : v)
              : [...doc[key], item]
            if (await persist({ ...doc, [key]: nextItems }, `${editor.kind} saved.`)) setEditor(null)
          }}
        />
      )}

      {editor?.mode === 'connection' && (
        <ConnectionEditor
          doc={doc}
          item={editor.item}
          onClose={() => setEditor(null)}
          onSave={async (item) => {
            const exists = doc.connections.some((v) => v.id === item.id)
            const connections = exists
              ? doc.connections.map((v) => v.id === item.id ? item : v)
              : [...doc.connections, item]
            if (await persist({ ...doc, connections }, 'Connection saved.')) setEditor(null)
          }}
        />
      )}

      {showGraph && <OutcomeGraph graphData={doc} onClose={() => setShowGraph(false)} />}
    </div>
  )
}

function CatalogColumn({ title, subtitle, items, records, onAdd, onEdit, onDelete }) {
  return (
    <section class="catalog-column">
      <div class="catalog-section-head">
        <div><h2>{title}</h2><p>{subtitle}</p></div>
        <button class="btn" onClick={onAdd}>+ Add</button>
      </div>
      <div class="catalog-list">
        {items.map((item) => (
          <article class="catalog-card" key={item.id}>
            <div class="catalog-card-main">
              <div class="catalog-card-type">{item.type?.replaceAll('_', ' ')}</div>
              <h3>{item.name}</h3>
              <p>{item.summary || 'No summary yet.'}</p>
              <div class="catalog-card-meta">
                {item.service && <span>{item.service}</span>}
                {item.platform && <span>{item.platform}</span>}
                <OwnerBadge meta={records?.[item.id]} />
              </div>
            </div>
            <div class="catalog-card-actions">
              <button class="btn secondary tiny" onClick={() => onEdit(item)}>Edit</button>
              <button class="btn danger tiny" onClick={() => onDelete(item.id)}>Delete</button>
            </div>
          </article>
        ))}
        {!items.length && <div class="catalog-empty">No {title.toLowerCase()} yet. Add one manually or import an automation run.</div>}
      </div>
    </section>
  )
}

function ConnectionTable({ doc, onEdit, onDelete }) {
  const exposures = useMemo(() => new Map(doc.exposures.map((v) => [v.id, v])), [doc.exposures])
  const dependencies = useMemo(() => new Map(doc.dependencies.map((v) => [v.id, v])), [doc.dependencies])
  if (!doc.connections.length) return <div class="catalog-empty">No connections yet.</div>
  return (
    <div class="catalog-connection-list">
      {doc.connections.map((c) => (
        <div class="catalog-connection-row" key={c.id}>
          <span>{exposures.get(c.from_exposure_id)?.name || c.from_exposure_id}</span>
          <b>→</b>
          <span>{dependencies.get(c.to_dependency_id)?.name || c.to_dependency_id}</span>
          <em>{c.condition?.kind || 'unconditional'}</em>
          <OwnerBadge meta={doc.records?.[c.id]} />
          <button class="btn secondary tiny" onClick={() => onEdit(c)}>Edit</button>
          <button class="btn danger tiny" onClick={() => onDelete(c.id)}>Delete</button>
        </div>
      ))}
    </div>
  )
}

function OwnerBadge({ meta }) {
  const owner = meta?.owner || 'manual'
  return <span class={'catalog-owner ' + owner} title={meta?.run_id ? `Imported from ${meta.run_id}` : ''}>{owner}</span>
}

function NodeEditor({ kind, item, onSave, onClose }) {
  const [draft, setDraft] = useState(() => ({ ...item }))
  const set = (key, value) => setDraft((d) => ({ ...d, [key]: value }))
  return (
    <EditorModal title={`${item.id ? 'Edit' : 'Add'} ${kind}`} onClose={onClose}>
      <label>Type<input value={draft.type || ''} onInput={(e) => set('type', e.target.value)} placeholder={kind === 'exposure' ? 'http_route' : 'db_operation'} /></label>
      <label>Name<input value={draft.name || ''} onInput={(e) => set('name', e.target.value)} placeholder="Human-readable architectural fact" /></label>
      <label>Service<input value={draft.service || ''} onInput={(e) => set('service', e.target.value)} placeholder="orders-service" /></label>
      <label>Platform<input value={draft.platform || ''} onInput={(e) => set('platform', e.target.value)} placeholder="http, postgres, sqs…" /></label>
      <label>Instance<input value={draft.instance || ''} onInput={(e) => set('instance', e.target.value)} placeholder="orders-db or billing-api" /></label>
      <label>Summary<textarea value={draft.summary || ''} onInput={(e) => set('summary', e.target.value)} /></label>
      <div class="actions">
        <button class="btn" disabled={!draft.type?.trim() || !draft.name?.trim()} onClick={() => onSave({
          ...draft,
          id: draft.id || newID(kind),
          confidence: draft.confidence || 1,
          source_locations: draft.source_locations || [],
          evidence: draft.evidence || [],
          details: draft.details || {},
        })}>Save</button>
        <button class="btn secondary" onClick={onClose}>Cancel</button>
      </div>
    </EditorModal>
  )
}

function ConnectionEditor({ doc, item, onSave, onClose }) {
  const [draft, setDraft] = useState(() => ({ ...item, condition: { ...(item.condition || {}) } }))
  const set = (key, value) => setDraft((d) => ({ ...d, [key]: value }))
  const setCondition = (key, value) => setDraft((d) => ({ ...d, condition: { ...d.condition, [key]: value } }))
  const from = doc.exposures.find((e) => e.id === draft.from_exposure_id)
  const to = doc.dependencies.find((d) => d.id === draft.to_dependency_id)
  return (
    <EditorModal title={`${item.id ? 'Edit' : 'Add'} connection`} onClose={onClose}>
      <label>Exposure<select value={draft.from_exposure_id} onChange={(e) => set('from_exposure_id', e.target.value)}>
        {doc.exposures.map((e) => <option value={e.id} key={e.id}>{e.name}</option>)}
      </select></label>
      <label>Dependency<select value={draft.to_dependency_id} onChange={(e) => set('to_dependency_id', e.target.value)}>
        {doc.dependencies.map((d) => <option value={d.id} key={d.id}>{d.name}</option>)}
      </select></label>
      <label>Condition<select value={draft.condition?.kind || 'unconditional'} onChange={(e) => setCondition('kind', e.target.value)}>
        <option value="unconditional">Unconditional</option>
        <option value="if_guard">Conditional</option>
        <option value="loop">Loop / batch</option>
        <option value="catch_block">Error only</option>
      </select></label>
      <label>Expression<input value={draft.condition?.expression || ''} onInput={(e) => setCondition('expression', e.target.value)} placeholder="true or featureEnabled" /></label>
      <label>Summary<textarea value={draft.summary || ''} onInput={(e) => set('summary', e.target.value)} /></label>
      <div class="actions">
        <button class="btn" onClick={() => onSave({
          ...draft,
          id: draft.id || newID('connection'),
          source: 'manual',
          confidence: draft.confidence || 1,
          from_type: from?.type || '',
          to_type: to?.type || '',
          path_signature: draft.path_signature || `manual:${draft.from_exposure_id}->${draft.to_dependency_id}`,
          source_locations: draft.source_locations || [],
          evidence: draft.evidence || [],
          paths: draft.paths || [],
          condition: {
            kind: draft.condition?.kind || 'unconditional',
            expression: draft.condition?.expression || 'true',
            explanation: draft.condition?.explanation || 'Manually defined',
          },
        })}>Save</button>
        <button class="btn secondary" onClick={onClose}>Cancel</button>
      </div>
    </EditorModal>
  )
}

function EditorModal({ title, children, onClose }) {
  return (
    <div class="modal-backdrop" onClick={onClose}>
      <div class="modal catalog-editor" onClick={(e) => e.stopPropagation()}>
        <div class="modal-head"><h2>{title}</h2><button class="btn secondary tiny" onClick={onClose}>x</button></div>
        <div class="modal-body catalog-editor-fields">{children}</div>
      </div>
    </div>
  )
}

function emptyNode() {
  return { id: '', type: '', name: '', service: '', platform: '', instance: '', summary: '', details: {} }
}

function emptyConnection(doc) {
  return {
    id: '',
    from_exposure_id: doc.exposures[0]?.id || '',
    to_dependency_id: doc.dependencies[0]?.id || '',
    condition: { kind: 'unconditional', expression: 'true', explanation: 'Manually defined' },
    summary: '',
  }
}

function newID(prefix) {
  const suffix = globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random().toString(16).slice(2)}`
  return `manual-${prefix}-${suffix}`
}

function shortRepo(path) {
  if (!path) return 'repository'
  const parts = path.split('/').filter(Boolean)
  return parts.slice(-2).join('/')
}
