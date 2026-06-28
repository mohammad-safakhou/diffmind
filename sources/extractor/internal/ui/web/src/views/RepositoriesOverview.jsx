import { useEffect, useState } from 'preact/hooks'
import { listRepos, upsertRepo, getConfig } from '../lib/api.js'
import { navigate } from '../lib/router.js'
import { Button, Card, Badge, StatusBadge, Modal, EmptyState, useToast } from '../components/ui/index.js'
import { RunForm } from '../components/RunForm.jsx'

export function RepositoriesOverview() {
  const toast = useToast()
  const [repos, setRepos] = useState(null)
  const [adding, setAdding] = useState(false)
  const [newPath, setNewPath] = useState('')
  const [runFor, setRunFor] = useState(null)
  const [prefill, setPrefill] = useState({})

  const load = () => listRepos().then((r) => setRepos(r.repos || [])).catch((e) => { toast.error(e.message); setRepos([]) })

  useEffect(() => {
    load()
    getConfig().then(setPrefill).catch(() => setPrefill({}))
  }, [])

  const addRepo = async () => {
    const path = newPath.trim()
    if (!path) return
    try {
      await upsertRepo({ path })
      toast.success('Repository added.')
      setNewPath('')
      setAdding(false)
      load()
    } catch (e) { toast.error(e.message) }
  }

  if (repos === null) return <div class="catalog-loading">Loading repositories...</div>

  return (
    <div class="page">
      <header class="page-header">
        <div>
          <div class="page-eyebrow">Repositories</div>
          <h1>Deterministic Runs</h1>
          <p class="page-sub">Register repositories, run deterministic extraction, and inspect generated DiffMind protocol artifacts.</p>
        </div>
        <div class="page-header-actions">
          <Button onClick={() => setAdding(true)}>+ Add repository</Button>
        </div>
      </header>

      {repos.length === 0 ? (
        <EmptyState
          title="No repositories yet"
          hint="Add a repository path to start deterministic extraction."
          action={<Button onClick={() => setAdding(true)}>+ Add repository</Button>}
        />
      ) : (
        <div class="repo-grid">
          {repos.map((r) => (
            <Card key={r.id} interactive class="repo-card" onClick={() => setRunFor(r)}>
              <div class="repo-card-head">
                <h3 class="repo-card-name">{r.display_name || r.name}</h3>
                <Badge tone={r.run_count ? 'success' : 'neutral'}>{r.run_count || 0} runs</Badge>
              </div>
              <code class="repo-card-path">{r.path}</code>
              <div class="repo-card-stats">
                <span><b>{r.node_count || 0}</b> objects</span>
                <span><b>{r.edge_count || 0}</b> connections</span>
                <span><b>{r.pending_count || 0}</b> warnings</span>
              </div>
              <div class="repo-card-foot">
                {r.last_status ? <StatusBadge status={r.last_status} /> : <span class="muted">no runs yet</span>}
                <div class="repo-card-actions" onClick={(e) => e.stopPropagation()}>
                  {r.last_run_id && <Button variant="secondary" size="tiny" onClick={() => navigate(`/runs/${encodeURIComponent(r.last_run_id)}`)}>Latest Run</Button>}
                  <Button variant="secondary" size="tiny" onClick={() => setRunFor(r)}>Run</Button>
                </div>
              </div>
            </Card>
          ))}
        </div>
      )}

      {adding && (
        <Modal title="Add repository" onClose={() => setAdding(false)}>
          <div class="form">
            <div class="field">
              <label>Repository absolute path</label>
              <input value={newPath} onInput={(e) => setNewPath(e.target.value)} placeholder="/abs/path/to/repo" />
            </div>
            <div class="actions">
              <Button onClick={addRepo}>Add</Button>
              <Button variant="secondary" onClick={() => setAdding(false)}>Cancel</Button>
            </div>
          </div>
        </Modal>
      )}

      {runFor && (
        <Modal title={`Run deterministic extraction · ${runFor.display_name || runFor.name}`} onClose={() => setRunFor(null)}>
          <RunForm
            prefill={{ ...prefill, repo_path: runFor.path }}
            gateOnActiveRun={false}
            onLaunched={(runID) => { setRunFor(null); if (runID) navigate(`/runs/${encodeURIComponent(runID)}`) }}
          />
        </Modal>
      )}
    </div>
  )
}
