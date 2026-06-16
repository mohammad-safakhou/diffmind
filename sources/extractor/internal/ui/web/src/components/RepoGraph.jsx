import { useEffect, useState } from 'preact/hooks'
import { getFileGraph, getRepoFile, putRepoFile } from '../lib/api.js'
import { Button, Card, EmptyState, useToast } from './ui/index.js'
import { OutcomeGraph } from './OutcomeGraph.jsx'

export function RepoGraph({ repo, onGenerate, onSaved }) {
  const toast = useToast()
  const path = repo.file_path || `${repo.path}/diffmind.yaml`
  const [graph, setGraph] = useState(null)
  const [file, setFile] = useState(null)
  const [draft, setDraft] = useState('')
  const [dirty, setDirty] = useState(false)
  const [busy, setBusy] = useState('')
  const [error, setError] = useState('')

  const load = async () => {
    setBusy('load')
    try {
      const [graphData, fileData] = await Promise.all([getFileGraph(path), getRepoFile(path)])
      setGraph(graphData)
      setFile(fileData)
      setDraft(fileData.content || '')
      setDirty(false)
      setError('')
    } catch (e) {
      setError(e.message || String(e))
    } finally {
      setBusy('')
    }
  }

  useEffect(() => { load() }, [repo.id, repo.file_path])

  const save = async () => {
    setBusy('save')
    try {
      await putRepoFile(path, draft)
      toast.success('diffmind.yaml saved.')
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
    <div class="repo-graph">
      {error && <div class="banner error">{error}</div>}
      <Card class="repo-graph-card">
        <div class="repo-graph-head">
          <div>
            <div class="repo-section-kicker">Graph</div>
            <h2>Resolved architecture</h2>
            <p>Rendered from the resolved <code>diffmind.yaml</code>, including vars and includes.</p>
          </div>
          <Button variant="secondary" size="tiny" onClick={load} disabled={!!busy}>Refresh</Button>
        </div>
        {graph && <OutcomeGraph graphData={graph} embedded />}
      </Card>

      <Card class="fw-editor-card">
        <div class="fw-editor-head">
          <span class="fw-editor-title">Inline YAML editor</span>
          <div class="fw-action-row">
            <Button size="tiny" onClick={save} disabled={!dirty || !!busy}>{busy === 'save' ? 'Saving…' : 'Save'}</Button>
            <Button variant="secondary" size="tiny" onClick={load} disabled={!!busy}>Reset</Button>
          </div>
        </div>
        <textarea
          class="fw-editor"
          spellcheck={false}
          value={draft}
          onInput={(e) => { setDraft(e.target.value); setDirty(true) }}
        />
      </Card>
    </div>
  )
}
