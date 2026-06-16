import { useEffect, useState } from 'preact/hooks'
import { listRepos, upsertRepo, getConfig } from '../lib/api.js'
import { navigate } from '../lib/router.js'
import { Button, Card, Badge, StatusBadge, Modal, EmptyState, useToast } from '../components/ui/index.js'
import { FsPicker } from '../components/FsPicker.jsx'
import { RunForm } from '../components/RunForm.jsx'

// RepositoriesOverview is the landing page: the repositories you work with, each
// a card with its discovery-file status, file graph footprint, and last run.
export function RepositoriesOverview() {
  const toast = useToast()
  const [repos, setRepos] = useState(null)
  const [adding, setAdding] = useState(false)
  const [runFor, setRunFor] = useState(null) // repo to launch a run against
  const [prefill, setPrefill] = useState({})

  const load = () => listRepos().then((r) => setRepos(r.repos || [])).catch((e) => { toast.error(e.message); setRepos([]) })

  useEffect(() => {
    load()
    getConfig().then(setPrefill).catch(() => setPrefill({}))
  }, [])

  const addRepo = async (filePath) => {
    setAdding(false)
    // The picked file lives inside the repo; register the repo at its folder.
    const dir = parentDir(filePath)
    try {
      await upsertRepo({ path: dir, file_path: filePath })
      toast.success('Repository added.')
      load()
    } catch (e) { toast.error(e.message) }
  }

  if (repos === null) return <div class="catalog-loading">Loading repositories…</div>

  return (
    <div class="page">
      <header class="page-header">
        <div>
          <div class="page-eyebrow">Repositories</div>
          <h1>Repositories</h1>
          <p class="page-sub">Each repository owns a <code>diffmind.yaml</code> discovery file — the durable, version-controlled source of truth.</p>
        </div>
        <div class="page-header-actions">
          <Button onClick={() => setAdding(true)}>+ Add repository</Button>
        </div>
      </header>

      {repos.length === 0 ? (
        <EmptyState
          title="No repositories yet"
          hint="Add a repository to manage its diffmind.yaml, or launch an automation run to discover one."
          action={<Button onClick={() => setAdding(true)}>+ Add repository</Button>}
        />
      ) : (
        <div class="repo-grid">
          {repos.map((r) => (
            <Card key={r.id} interactive class="repo-card" onClick={() => navigate(`/repos/${encodeURIComponent(r.id)}`)}>
              <div class="repo-card-head">
                <h3 class="repo-card-name">{r.display_name || r.name}</h3>
                <FileBadge repo={r} />
              </div>
              <code class="repo-card-path">{r.path}</code>
              <div class="repo-card-stats">
                <span><b>{r.node_count || 0}</b> nodes</span>
                <span><b>{r.edge_count || 0}</b> connections</span>
                <span><b>{r.run_count || 0}</b> runs</span>
              </div>
              <div class="repo-card-foot">
                {r.last_status ? <StatusBadge status={r.last_status} /> : <span class="muted">no runs yet</span>}
                <div class="repo-card-actions" onClick={(e) => e.stopPropagation()}>
                  <Button variant="secondary" size="tiny" onClick={() => navigate(`/repos/${encodeURIComponent(r.id)}`)}>Open</Button>
                  <Button variant="secondary" size="tiny" onClick={() => setRunFor(r)}>New Run</Button>
                </div>
              </div>
            </Card>
          ))}
        </div>
      )}

      {adding && <FsPicker onPick={addRepo} onClose={() => setAdding(false)} />}

      {runFor && (
        <Modal title={`New Run · ${runFor.display_name || runFor.name}`} onClose={() => setRunFor(null)}>
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

function FileBadge({ repo }) {
  if (repo.file_present) return <Badge tone="success">file ✓</Badge>
  if (repo.file_path) return <Badge tone="warn">file missing</Badge>
  if (repo.run_count) return <Badge tone="warn">no file - generate</Badge>
  return <Badge tone="neutral">no file</Badge>
}

function parentDir(p) {
  const i = p.lastIndexOf('/')
  return i > 0 ? p.slice(0, i) : p
}
