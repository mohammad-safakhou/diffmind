import { useEffect, useMemo, useState } from 'preact/hooks'
import { getFileGraph, getRepoFile, reviewRepoFile } from '../lib/api.js'
import { Button, Card, Badge, EmptyState, useToast } from './ui/index.js'
import { FactEditor } from './FactEditor.jsx'
import { CoverageBar } from './CoverageBar.jsx'

// ReviewInbox is the curation loop: it surfaces every automation-proposed fact
// and lets the user Accept (verify), Edit-then-accept, or Reject (delete) — one
// fact at a time or in bulk. Accepted facts become verified in the file; the
// coverage bar tracks progress toward 100%.
export function ReviewInbox({ repo, onChanged }) {
  const toast = useToast()
  const path = repo.file_path || `${repo.path}/diffmind.yaml`
  const [graph, setGraph] = useState(null)
  const [sha, setSha] = useState('')
  const [busy, setBusy] = useState(false)
  const [editing, setEditing] = useState(null) // {kind, fact}
  const [error, setError] = useState('')

  const load = async () => {
    try {
      const [g, f] = await Promise.all([getFileGraph(path), getRepoFile(path)])
      setGraph(g)
      setSha(f.sha256 || '')
      setError('')
    } catch (e) {
      setError(e.message || String(e))
    }
  }

  useEffect(() => { load() }, [repo.id, repo.file_path])

  const expName = useMemo(() => idMap(graph?.exposures), [graph])
  const depName = useMemo(() => idMap(graph?.dependencies), [graph])

  const pending = useMemo(() => collectPending(graph), [graph])

  const apply = async (edits, successMsg) => {
    if (!sha) { toast.error('File not loaded yet.'); return }
    setBusy(true)
    try {
      const res = await reviewRepoFile(path, sha, edits)
      setGraph(res.graph)
      setSha(res.sha256 || '')
      setEditing(null)
      toast.success(successMsg)
      onChanged?.()
    } catch (e) {
      // A stale sha means the file changed under us — reload and let the user retry.
      toast.error(e.message || String(e))
      await load()
    } finally {
      setBusy(false)
    }
  }

  const accept = (kind, fact) => apply(acceptEdits(kind, fact), 'Verified.')
  const reject = (kind, fact) => apply(rejectEdits(kind, fact), 'Rejected.')
  const acceptAll = () => apply(acceptAllEdits(pending), `Verified ${pending.total} fact(s).`)

  const saveEdit = (edit, verify) => {
    const kind = editing.kind
    const edits = { [pluralKind(kind)]: [edit] }
    apply(edits, verify ? 'Verified.' : 'Saved.')
  }

  if (error) return <div class="catalog-loading error">{error}</div>
  if (!graph) return <div class="catalog-loading">Loading review queue…</div>

  return (
    <div class="review">
      <Card class="review-head">
        <div class="review-head-main">
          <h2>Review queue</h2>
          <p class="page-sub">Curate automation's proposed facts into a verified architecture. Accept what's right, fix what's close, reject what's wrong.</p>
        </div>
        <CoverageBar coverage={graph.coverage} />
      </Card>

      {pending.total === 0 ? (
        <EmptyState title="All facts verified ✓" hint="Nothing pending review. Run automation or add facts on the graph to discover more." />
      ) : (
        <div class="review-groups">
          <div class="review-bulk">
            <span>{pending.total} fact(s) awaiting review</span>
            <Button size="tiny" disabled={busy} onClick={acceptAll}>Accept all</Button>
          </div>
          <ReviewGroup title="Exposures" rows={pending.exposures} kind="exposure"
            label={(f) => f.name} sub={(f) => f.type}
            onAccept={accept} onReject={reject} onEdit={(f) => setEditing({ kind: 'exposure', fact: f })} busy={busy} />
          <ReviewGroup title="Dependencies" rows={pending.dependencies} kind="dependency"
            label={(f) => f.name} sub={(f) => f.type}
            onAccept={accept} onReject={reject} onEdit={(f) => setEditing({ kind: 'dependency', fact: f })} busy={busy} />
          <ReviewGroup title="Connections" rows={pending.connections} kind="connection"
            label={(f) => `${expName[f.from_exposure_id] || f.from_exposure_id} → ${depName[f.to_dependency_id] || f.to_dependency_id}`}
            sub={(f) => f.condition?.expression && f.condition.expression !== 'true' ? f.condition.expression : 'unconditional'}
            onAccept={accept} onReject={reject} onEdit={(f) => setEditing({ kind: 'connection', fact: f })} busy={busy} />
        </div>
      )}

      {editing && (
        <FactEditor
          kind={editing.kind}
          fact={editing.fact}
          resources={graph.resources || []}
          onClose={() => setEditing(null)}
          onSave={saveEdit}
        />
      )}
    </div>
  )
}

function ReviewGroup({ title, rows, kind, label, sub, onAccept, onReject, onEdit, busy }) {
  if (!rows.length) return null
  return (
    <Card class="review-group">
      <div class="review-group-head">{title} <span class="fw-diff-count">{rows.length}</span></div>
      {rows.map((f, i) => (
        <div class="review-row" key={kind + '-' + i}>
          <div class="review-row-main">
            <div class="review-row-name">{label(f)}</div>
            <div class="review-row-sub">
              <Badge tone="warn">{f.status || 'proposed'}</Badge>
              <span class="review-row-type">{sub(f)}</span>
              {f.source && <span class="review-row-src">from {f.source}</span>}
            </div>
          </div>
          <div class="review-row-actions">
            <Button size="tiny" disabled={busy} onClick={() => onAccept(kind, f)}>Accept</Button>
            <Button size="tiny" variant="secondary" disabled={busy} onClick={() => onEdit(f)}>Edit</Button>
            <Button size="tiny" variant="danger" disabled={busy} onClick={() => onReject(kind, f)}>Reject</Button>
          </div>
        </div>
      ))}
    </Card>
  )
}

// --- edit builders ---------------------------------------------------------

function acceptEdits(kind, fact) {
  if (kind === 'connection') {
    return { connections: [{ from_id: fact.from_exposure_id, to_id: fact.to_dependency_id, status: 'verified', source: 'manual' }] }
  }
  return { [pluralKind(kind)]: [{ id: fact.id, status: 'verified', source: 'manual' }] }
}

function rejectEdits(kind, fact) {
  if (kind === 'connection') {
    return { delete: [{ kind: 'connection', from_id: fact.from_exposure_id, to_id: fact.to_dependency_id }] }
  }
  return { delete: [{ kind, id: fact.id }] }
}

function acceptAllEdits(pending) {
  const edits = { exposures: [], dependencies: [], connections: [] }
  for (const f of pending.exposures) edits.exposures.push({ id: f.id, status: 'verified', source: 'manual' })
  for (const f of pending.dependencies) edits.dependencies.push({ id: f.id, status: 'verified', source: 'manual' })
  for (const f of pending.connections) edits.connections.push({ from_id: f.from_exposure_id, to_id: f.to_dependency_id, status: 'verified', source: 'manual' })
  return edits
}

function pluralKind(kind) {
  return kind === 'exposure' ? 'exposures' : kind === 'dependency' ? 'dependencies' : kind === 'resource' ? 'resources' : 'connections'
}

function isPending(status) {
  return status && status !== 'verified'
}

function collectPending(graph) {
  if (!graph) return { exposures: [], dependencies: [], connections: [], total: 0 }
  const exposures = (graph.exposures || []).filter((f) => isPending(f.status))
  const dependencies = (graph.dependencies || []).filter((f) => isPending(f.status))
  const connections = (graph.connections || []).filter((f) => isPending(f.status))
  return { exposures, dependencies, connections, total: exposures.length + dependencies.length + connections.length }
}

function idMap(list) {
  const m = {}
  for (const x of list || []) m[x.id] = x.name
  return m
}
