import { useEffect, useMemo, useState } from 'preact/hooks'
import { getFileGraph, getRepoFile, reviewRepoFile, applyRepoFile } from '../lib/api.js'
import { Button, Card, Badge, EmptyState, useToast } from './ui/index.js'
import { ArchGraph } from './ArchGraph.jsx'
import { FactEditor } from './FactEditor.jsx'
import { CoverageBar } from './CoverageBar.jsx'

const EXPOSURE_TYPES = ['http_route', 'webhook', 'rpc_endpoint', 'queue_consumer', 'scheduled_job', 'cli_command', 'stream_consume']
const DEPENDENCY_TYPES = ['db_operation', 'cache_operation', 'outbound_http', 'outbound_rpc', 'queue_publish', 'command_exec']

// RepoGraph is the repository's architecture workspace: an interactive graph of
// the resolved diffmind.yaml, an inspector for the selected fact (accept / edit
// / reject), an "+ Add" toolbar, connect-by-click, and a raw YAML drawer.
export function RepoGraph({ repo, onGenerate, onSaved }) {
  const toast = useToast()
  const path = repo.file_path || `${repo.path}/diffmind.yaml`
  const [graph, setGraph] = useState(null)
  const [file, setFile] = useState(null)
  const [selected, setSelected] = useState(null) // {kind, id}
  const [editing, setEditing] = useState(null)    // {kind, fact, create}
  const [drawerOpen, setDrawerOpen] = useState(false)
  const [yamlDraft, setYamlDraft] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const load = async () => {
    try {
      const [g, f] = await Promise.all([getFileGraph(path), getRepoFile(path)])
      setGraph(g)
      setFile(f)
      setYamlDraft(f.content || '')
      setError('')
    } catch (e) {
      setError(e.message || String(e))
    }
  }

  useEffect(() => { load() }, [repo.id, repo.file_path])

  const factOf = useMemo(() => indexFacts(graph), [graph])
  const selectedFact = selected ? factOf(selected.kind, selected.id) : null

  const review = async (edits, msg) => {
    if (!file?.sha256) { toast.error('File not loaded.'); return }
    setBusy(true)
    try {
      const res = await reviewRepoFile(path, file.sha256, edits)
      setGraph(res.graph)
      setFile((f) => ({ ...f, sha256: res.sha256, content: res.yaml }))
      setYamlDraft(res.yaml)
      setEditing(null)
      toast.success(msg)
      onSaved?.()
    } catch (e) {
      toast.error(e.message || String(e))
      await load()
    } finally {
      setBusy(false)
    }
  }

  const accept = () => review(acceptEdit(selected.kind, selectedFact), 'Verified.')
  const reject = () => { review(rejectEdit(selected.kind, selectedFact), 'Rejected.'); setSelected(null) }
  const saveEdit = (edit, verify) => review({ [plural(editing.kind)]: [edit] }, verify ? 'Saved & verified.' : 'Saved.')
  const connect = (fromId, toId) => review({ connections: [{ from_id: fromId, to_id: toId, status: 'verified', source: 'manual' }] }, 'Connected.')

  const applyYaml = async () => {
    setBusy(true)
    try {
      await applyRepoFile(path, file?.sha256 || '', yamlDraft)
      toast.success('diffmind.yaml updated.')
      setDrawerOpen(false)
      await load()
      onSaved?.()
    } catch (e) {
      toast.error(e.message || String(e))
    } finally {
      setBusy(false)
    }
  }

  if (error) return <div class="catalog-loading error">{error}</div>
  if (!file) return <div class="catalog-loading">Loading graph…</div>
  if (!file.exists) {
    return (
      <EmptyState
        title="No diffmind.yaml yet"
        hint="Generate one from a completed run in the Overview tab, then come back to shape the graph."
        action={<Button onClick={onGenerate}>Generate from a run</Button>}
      />
    )
  }

  return (
    <div class="repo-graph-workspace">
      <div class="repo-graph-bar">
        <CoverageBar coverage={graph?.coverage} />
        <div class="fw-action-row">
          <Button variant="secondary" size="tiny" onClick={() => setEditing({ kind: 'exposure', fact: {}, create: true })}>+ Exposure</Button>
          <Button variant="secondary" size="tiny" onClick={() => setEditing({ kind: 'dependency', fact: {}, create: true })}>+ Dependency</Button>
          <Button variant="secondary" size="tiny" onClick={() => setEditing({ kind: 'resource', fact: {}, create: true })}>+ Resource</Button>
          <Button variant="secondary" size="tiny" onClick={() => setDrawerOpen(true)}>Edit YAML</Button>
        </div>
      </div>

      <div class="repo-graph-body">
        {graph && <ArchGraph graph={graph} selected={selected} onSelect={setSelected} onConnect={connect} />}
        {selectedFact && (
          <Inspector
            kind={selected.kind}
            fact={selectedFact}
            busy={busy}
            onEdit={() => setEditing({ kind: selected.kind, fact: selectedFact })}
            onAccept={accept}
            onReject={reject}
            onClose={() => setSelected(null)}
          />
        )}
      </div>

      {editing && (
        <FactEditor
          kind={editing.kind}
          fact={editing.fact}
          create={editing.create}
          types={editing.kind === 'exposure' ? EXPOSURE_TYPES : DEPENDENCY_TYPES}
          resources={graph?.resources || []}
          verifyLabel={editing.create ? 'Add' : 'Save & verify'}
          onClose={() => setEditing(null)}
          onSave={saveEdit}
        />
      )}

      {drawerOpen && (
        <div class="rg-yaml-drawer">
          <div class="rg-yaml-panel">
            <div class="rg-editor-head">
              <div><div class="repo-section-kicker">Raw YAML</div><h2>Edit diffmind.yaml</h2></div>
              <Button variant="secondary" size="tiny" onClick={() => setDrawerOpen(false)}>Close</Button>
            </div>
            <textarea class="fw-editor rg-yaml-editor" spellcheck={false} value={yamlDraft} onInput={(e) => setYamlDraft(e.target.value)} />
            <div class="rg-editor-actions">
              <Button onClick={applyYaml} disabled={busy}>{busy ? 'Applying…' : 'Apply YAML'}</Button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

function Inspector({ kind, fact, busy, onEdit, onAccept, onReject, onClose }) {
  const pending = fact.status && fact.status !== 'verified'
  return (
    <Card class="rg-inspector">
      <div class="rg-inspector-head">
        <div>
          <div class="repo-section-kicker">{kind}</div>
          <h3>{fact.name || `${fact.from_exposure_id} → ${fact.to_dependency_id}`}</h3>
        </div>
        <Button variant="secondary" size="tiny" onClick={onClose}>✕</Button>
      </div>
      <div class="rg-inspector-meta">
        <Badge tone={pending ? 'warn' : 'success'}>{fact.status || 'verified'}</Badge>
        {fact.type && <span class="review-row-type">{fact.type}</span>}
        {fact.source && <span class="review-row-src">from {fact.source}</span>}
      </div>
      {fact.summary && <p class="rg-inspector-summary">{fact.summary}</p>}
      <div class="fw-action-row">
        {pending && <Button size="tiny" disabled={busy} onClick={onAccept}>Accept</Button>}
        <Button size="tiny" variant="secondary" disabled={busy} onClick={onEdit}>Edit</Button>
        <Button size="tiny" variant="danger" disabled={busy} onClick={onReject}>Reject</Button>
      </div>
    </Card>
  )
}

function acceptEdit(kind, fact) {
  if (kind === 'connection') return { connections: [{ from_id: fact.from_exposure_id, to_id: fact.to_dependency_id, status: 'verified', source: 'manual' }] }
  return { [plural(kind)]: [{ id: fact.id, status: 'verified', source: 'manual' }] }
}

function rejectEdit(kind, fact) {
  if (kind === 'connection') return { delete: [{ kind: 'connection', from_id: fact.from_exposure_id, to_id: fact.to_dependency_id }] }
  return { delete: [{ kind, id: fact.id }] }
}

function plural(kind) {
  return kind === 'exposure' ? 'exposures' : kind === 'dependency' ? 'dependencies' : kind === 'resource' ? 'resources' : 'connections'
}

function indexFacts(graph) {
  const exp = new Map((graph?.exposures || []).map((x) => [x.id, x]))
  const dep = new Map((graph?.dependencies || []).map((x) => [x.id, x]))
  const res = new Map((graph?.resources || []).map((x) => [x.id, x]))
  return (kind, id) => {
    if (kind === 'exposure') return exp.get(id)
    if (kind === 'dependency') return dep.get(id)
    if (kind === 'resource') return res.get(id)
    return null
  }
}
