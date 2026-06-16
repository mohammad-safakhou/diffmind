import { useEffect, useState } from 'preact/hooks'
import { applyRepoFile, draftRepoFile, getFileGraph, getRepoFile } from '../lib/api.js'
import { Button, Card, EmptyState, useToast } from './ui/index.js'
import { ResourceGraph } from './ResourceGraph.jsx'

export function RepoGraph({ repo, onGenerate, onSaved }) {
  const toast = useToast()
  const path = repo.file_path || `${repo.path}/diffmind.yaml`
  const [graph, setGraph] = useState(null)
  const [file, setFile] = useState(null)
  const [yamlDraft, setYamlDraft] = useState('')
  const [drawerOpen, setDrawerOpen] = useState(false)
  const [pending, setPending] = useState(null)
  const [busy, setBusy] = useState('')
  const [error, setError] = useState('')

  const load = async () => {
    setBusy('load')
    try {
      const [graphData, fileData] = await Promise.all([getFileGraph(path), getRepoFile(path)])
      setGraph(graphData)
      setFile(fileData)
      setYamlDraft(fileData.content || '')
      setPending(null)
      setError('')
    } catch (e) {
      setError(e.message || String(e))
    } finally {
      setBusy('')
    }
  }

  useEffect(() => { load() }, [repo.id, repo.file_path])

  const previewEdits = async (edits) => {
    setBusy('draft')
    try {
      const next = await draftRepoFile(path, file?.sha256 || '', edits)
      setPending(next)
      setGraph(next.graph)
      setYamlDraft(next.yaml)
      const s = next.summary || {}
      toast.success(`Draft ready: ${sumSummary(s)} edited.`)
    } catch (e) {
      toast.error(e.message || String(e))
    } finally {
      setBusy('')
    }
  }

  const applyPending = async () => {
    if (!pending?.yaml) return
    setBusy('apply')
    try {
      await applyRepoFile(path, file?.sha256 || '', pending.yaml)
      toast.success('diffmind.yaml updated.')
      await load()
      onSaved?.()
    } catch (e) {
      toast.error(e.message || String(e))
    } finally {
      setBusy('')
    }
  }

  const applyYaml = async () => {
    setBusy('apply-yaml')
    try {
      await applyRepoFile(path, file?.sha256 || '', yamlDraft)
      toast.success('diffmind.yaml updated.')
      setDrawerOpen(false)
      await load()
      onSaved?.()
    } catch (e) {
      toast.error(e.message || String(e))
    } finally {
      setBusy('')
    }
  }

  if (busy === 'load' && !graph && !file) return <div class="catalog-loading">Loading graph…</div>

  if (!file?.exists) {
    return (
      <EmptyState
        title="No diffmind.yaml yet"
        hint="Generate one from a completed run, then return here to inspect and edit the graph."
        action={<Button onClick={onGenerate}>Generate from a run</Button>}
      />
    )
  }

  return (
    <div class="repo-graph repo-graph-full">
      {error && <div class="banner error">{error}</div>}
      <div class="repo-graph-actions">
        <div>
          <div class="repo-section-kicker">Source of truth</div>
          <code>{path}</code>
        </div>
        <div class="fw-action-row">
          <Button variant="secondary" size="tiny" onClick={load} disabled={!!busy}>Refresh</Button>
          <Button variant="secondary" size="tiny" onClick={() => setDrawerOpen(true)}>Edit YAML</Button>
        </div>
      </div>

      {graph && <ResourceGraph graph={graph} onDraft={previewEdits} busy={busy === 'draft'} />}

      {pending && (
        <Card class="rg-draft">
          <div class="rg-draft-head">
            <div>
              <div class="repo-section-kicker">Draft preview</div>
              <h2>Review before apply</h2>
              <p>This YAML has not been written yet. Apply will fail if the file changed since it was loaded.</p>
            </div>
            <div class="fw-action-row">
              <Button onClick={applyPending} disabled={!!busy}>{busy === 'apply' ? 'Applying…' : 'Apply draft'}</Button>
              <Button variant="secondary" onClick={load} disabled={!!busy}>Discard</Button>
            </div>
          </div>
          <textarea class="rg-yaml-preview" readOnly value={pending.yaml} />
        </Card>
      )}

      {drawerOpen && (
        <div class="rg-yaml-drawer">
          <div class="rg-yaml-panel">
            <div class="rg-editor-head">
              <div>
                <div class="repo-section-kicker">Raw YAML</div>
                <h2>Edit diffmind.yaml</h2>
              </div>
              <Button variant="secondary" size="tiny" onClick={() => setDrawerOpen(false)}>Close</Button>
            </div>
            <textarea
              class="fw-editor rg-yaml-editor"
              spellcheck={false}
              value={yamlDraft}
              onInput={(e) => setYamlDraft(e.target.value)}
            />
            <div class="rg-editor-actions">
              <Button onClick={applyYaml} disabled={!!busy}>{busy === 'apply-yaml' ? 'Applying…' : 'Apply YAML'}</Button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

function sumSummary(summary) {
  return (summary.resources || 0) + (summary.exposures || 0) + (summary.dependencies || 0) + (summary.connections || 0)
}
