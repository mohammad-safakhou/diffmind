import { useEffect, useState } from 'preact/hooks'
import { getConfig, listRepos } from '../lib/api.js'
import { navigate } from '../lib/router.js'
import { Button, Card, Modal } from '../components/ui/index.js'
import { RepoFileSync } from '../components/RepoFileSync.jsx'
import { RepoGraph } from '../components/RepoGraph.jsx'
import { RunForm } from '../components/RunForm.jsx'
import { RunsList } from '../components/RunsList.jsx'

// RepositoryDetail is the per-repository workspace: overview, run sync, runs,
// resolved file graph, and inline YAML editing all live under the repository.
export function RepositoryDetail({ repoID }) {
  const [repo, setRepo] = useState(undefined) // undefined=loading, null=not found
  const [tab, setTab] = useState('overview')
  const [showRunForm, setShowRunForm] = useState(false)
  const [prefill, setPrefill] = useState({})

  const load = () => listRepos()
    .then((r) => setRepo((r.repos || []).find((x) => x.id === repoID) || null))
    .catch(() => setRepo(null))

  useEffect(() => {
    load()
    getConfig().then(setPrefill).catch(() => setPrefill({}))
  }, [repoID])

  if (repo === undefined) return <div class="catalog-loading">Loading repository…</div>
  if (repo === null) {
    return (
      <div class="page">
        <div class="catalog-loading error">Repository not found.</div>
        <Button variant="secondary" onClick={() => navigate('/')}>← Repositories</Button>
      </div>
    )
  }

  return (
    <div class="page">
      <header class="page-header">
        <div>
          <div class="page-eyebrow"><button class="link-btn" onClick={() => navigate('/')}>Repositories</button> / {repo.display_name || repo.name}</div>
          <h1>{repo.display_name || repo.name}</h1>
          <code class="page-sub mono">{repo.path}</code>
        </div>
        <div class="page-header-actions">
          <Button variant="secondary" onClick={() => setTab('graph')}>Graph</Button>
          <Button onClick={() => setShowRunForm(true)}>+ New Run</Button>
        </div>
      </header>

      <div class="tabbar">
        <button class={'tab' + (tab === 'overview' ? ' active' : '')} onClick={() => setTab('overview')}>Overview</button>
        <button class={'tab' + (tab === 'graph' ? ' active' : '')} onClick={() => setTab('graph')}>Graph</button>
      </div>

      {tab === 'overview' && (
        <div class="repo-overview">
          <RepoSummary repo={repo} />
          <RepoFileSync repo={repo} onChanged={load} onGraph={() => setTab('graph')} />
          <section class="repo-runs-section">
            <div class="repo-section-head">
              <div>
                <div class="repo-section-kicker">Automation</div>
                <h2>Runs</h2>
              </div>
              <Button size="tiny" onClick={() => setShowRunForm(true)}>+ New Run</Button>
            </div>
            <RunsList lockedRepo={repo.path} />
          </section>
        </div>
      )}
      {tab === 'graph' && (
        <RepoGraph repo={repo} onGenerate={() => setTab('overview')} onSaved={load} />
      )}

      {showRunForm && (
        <Modal title={`New Run · ${repo.display_name || repo.name}`} onClose={() => setShowRunForm(false)}>
          <RunForm
            prefill={{ ...prefill, repo_path: repo.path }}
            gateOnActiveRun={false}
            onLaunched={(runID) => { setShowRunForm(false); if (runID) navigate(`/runs/${encodeURIComponent(runID)}`) }}
          />
        </Modal>
      )}
    </div>
  )
}

function RepoSummary({ repo }) {
  return (
    <div class="repo-summary-grid">
      <Card>
        <div class="repo-section-kicker">Discovery file</div>
        <h2>{repo.file_present ? 'File ready' : repo.file_path ? 'File missing' : 'No file yet'}</h2>
        <p class="page-sub mono">{repo.file_path || `${repo.path}/diffmind.yaml`}</p>
      </Card>
      <Card>
        <div class="repo-section-kicker">Graph</div>
        <h2>{repo.node_count || 0} nodes · {repo.edge_count || 0} connections</h2>
        <p class="page-sub">Counts are resolved from this repository's discovery file.</p>
      </Card>
      <Card>
        <div class="repo-section-kicker">Automation</div>
        <h2>{repo.run_count || 0} runs</h2>
        <p class="page-sub">{repo.last_status ? `Latest run: ${repo.last_status}` : 'No runs yet.'}</p>
      </Card>
    </div>
  )
}
