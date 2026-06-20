import { useEffect, useState } from 'preact/hooks'
import { navigate } from '../lib/router.js'
import { createRepo, createRun, deleteRepo, getDiffMindYaml, getWorkspace, putDiffMindYaml, startRepoDiffMind, syncRepo } from '../lib/api.js'
import { Modal, ConfirmDialog } from '../components/Modal.jsx'
import { GraphCanvas } from './GraphCanvas.jsx'
import { StatusBadge } from './tabs/RunsTab.jsx'

export function ProjectWorkspace({ pid }) {
  const [workspace, setWorkspace] = useState(null)
  const [selected, setSelected] = useState(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState('')
  const [addOpen, setAddOpen] = useState(false)
  const [yamlRepo, setYamlRepo] = useState(null)
  const [deleteTarget, setDeleteTarget] = useState(null)

  const refresh = async () => {
    try { setWorkspace(await getWorkspace(pid)); setError('') }
    catch (e) { setError(e.message) }
  }
  useEffect(() => { refresh() }, [pid])
  useEffect(() => {
    const t = setInterval(refresh, 10000)
    return () => clearInterval(t)
  }, [pid])

  const repos = workspace?.repos || []
  const graph = workspace?.graph
  const selectedRepo = selected?.kind === 'repo' ? selected.data : null
  const selectedService = selected?.kind === 'service' ? selected.data : null
  const live = workspace?.live_status || {}

  const graphRun = async () => {
    setBusy('graph')
    try {
      const refs = repos
        .filter((r) => r.latest_diffmind_run?.run_id && r.latest_diffmind_run.run_id !== 'repo:diffmind.yaml')
        .map((r) => ({ repo_id: r.id, diffmind_run_id: r.latest_diffmind_run.run_id }))
      if (!refs.length) throw new Error('No DiffMind runs available for graph build.')
      await createRun(pid, { repos: refs })
      setTimeout(refresh, 2500)
    } catch (e) { setError(e.message) }
    finally { setBusy('') }
  }

  const doSync = async (repo) => runAction('sync:' + repo.id, async () => { await syncRepo(pid, repo.id); await refresh() })
  const doDiffMind = async (repo) => runAction('diffmind:' + repo.id, async () => { await startRepoDiffMind(pid, repo.id); setTimeout(refresh, 1500) })
  const doDelete = async (repo) => runAction('delete:' + repo.id, async () => { await deleteRepo(pid, repo.id); setDeleteTarget(null); await refresh() })
  const runAction = async (key, fn) => {
    setBusy(key); setError('')
    try { await fn() } catch (e) { setError(e.message) }
    finally { setBusy('') }
  }

  return (
    <div class="workspace">
      <header class="workspace-topbar">
        <div class="workspace-crumbs">
          <button class="btn ghost tiny" onClick={() => navigate('/')}>Projects</button>
          <h1>{workspace?.project?.name || pid}</h1>
          {workspace?.latest_run && <StatusBadge status={workspace.latest_run.status} />}
        </div>
        <div class="workspace-actions">
          <button class="btn ghost" onClick={refresh}>Refresh</button>
          <button class="btn ghost" onClick={() => setAddOpen(true)}>Add repo</button>
          <button class="btn" disabled={busy === 'graph'} onClick={graphRun}>{busy === 'graph' ? 'Starting...' : 'Build graph'}</button>
        </div>
      </header>

      {error && <div class="workspace-error banner error">{error}</div>}

      <aside class="workspace-left">
        <div class="rail-title">Teams</div>
        {(workspace?.teams || []).map((team) => (
          <section class="team-section" key={team.name}>
            <div class="team-heading">
              <span>{team.name}</span>
              <span>{team.repo_ids.length}</span>
            </div>
            {repos.filter((r) => (r.effective_team || 'default') === team.name).map((repo) => (
              <RepoButton key={repo.id} repo={repo} live={live[repo.id]} active={selectedRepo?.id === repo.id} onClick={() => setSelected({ kind: 'repo', data: repo })} />
            ))}
          </section>
        ))}
      </aside>

      <main class="workspace-board">
        {graph ? <GraphCanvas graph={graph} onSelect={setSelected} /> : <EmptyBoard repos={repos} onAdd={() => setAddOpen(true)} />}
      </main>

      <aside class="workspace-right">
        <Inspector
          selection={selected}
          live={selectedRepo ? live[selectedRepo.id] : null}
          onSync={selectedRepo ? () => doSync(selectedRepo) : null}
          onDiffMind={selectedRepo ? () => doDiffMind(selectedRepo) : null}
          onYaml={selectedRepo ? () => setYamlRepo(selectedRepo) : null}
          onDelete={selectedRepo ? () => setDeleteTarget(selectedRepo) : null}
          busy={busy}
        />
      </aside>

      <footer class="workspace-status">
        <span>{repos.length} repos</span>
        <span>{(workspace?.teams || []).length} teams</span>
        <span>{graph ? `${(graph.services || []).length} services` : 'no graph yet'}</span>
        <span>{repos.filter((r) => r.freshness === 'stale').length} stale</span>
      </footer>

      {addOpen && <AddRepoModal pid={pid} onClose={() => setAddOpen(false)} onDone={() => { setAddOpen(false); refresh() }} />}
      {yamlRepo && <YamlModal pid={pid} repo={yamlRepo} onClose={() => setYamlRepo(null)} onSaved={() => { setYamlRepo(null); refresh() }} />}
      {deleteTarget && (
        <ConfirmDialog
          title="Remove repository?"
          message={`This removes DiffMind metadata for ${deleteTarget.name}. Source files are not deleted.`}
          confirmLabel="Remove"
          onConfirm={() => doDelete(deleteTarget)}
          onCancel={() => setDeleteTarget(null)}
        />
      )}
    </div>
  )
}

function RepoButton({ repo, live, active, onClick }) {
  const primary = repo.repo_metrics?.languages?.[0]?.language || 'unknown'
  return (
    <button class={'repo-pill ' + (active ? 'active ' : '') + (repo.freshness === 'stale' ? 'stale' : '')} onClick={onClick}>
      <span class="repo-name">{repo.name}</span>
      <span class="repo-meta">{primary} · {repo.freshness || 'unknown'} · PR {live?.pull_requests ?? '-'}</span>
    </button>
  )
}

function EmptyBoard({ repos, onAdd }) {
  return (
    <div class="workspace-empty">
      <h2>Graph workspace</h2>
      <p>{repos.length ? 'Run DiffMind, then build a graph from the latest repository facts.' : 'Add repositories to start building the company graph.'}</p>
      <button class="btn" onClick={onAdd}>Add repository</button>
    </div>
  )
}

function Inspector({ selection, live, onSync, onDiffMind, onYaml, onDelete, busy }) {
  if (!selection) return <div class="inspector-empty">Select a repo, service, resource, or edge.</div>
  if (selection.kind === 'repo') {
    const repo = selection.data
    const metrics = repo.repo_metrics
    return (
      <div class="inspector">
        <h2>{repo.name}</h2>
        <div class="inspector-badges">
          <span class={'freshness ' + (repo.freshness || 'unknown')}>{repo.freshness || 'unknown'}</span>
          <span>{repo.effective_team || 'default'}</span>
          <span>{repo.source_type || 'local'}</span>
        </div>
        <KV rows={[
          ['Path', repo.path],
          ['Git URL', repo.git_url || '-'],
          ['Branch', repo.default_branch || '-'],
          ['HEAD', shortSha(repo.head_sha)],
          ['Remote', shortSha(repo.remote_head_sha)],
          ['DiffMind run', repo.latest_diffmind_run?.run_id || '-'],
          ['LOC', metrics?.total_loc || 0],
          ['Open PRs', live?.pull_requests ?? '-'],
          ['Open issues', live?.issues ?? '-'],
          ['Actions', live?.actions_state || '-'],
        ]} />
        <div class="inspector-actions">
          <button class="btn ghost" disabled={busy === 'sync:' + repo.id} onClick={onSync}>Sync git</button>
          <button class="btn" disabled={busy === 'diffmind:' + repo.id} onClick={onDiffMind}>Run DiffMind</button>
          <button class="btn ghost" onClick={onYaml}>diffmind.yaml</button>
          <button class="btn danger" onClick={onDelete}>Remove</button>
        </div>
      </div>
    )
  }
  if (selection.kind === 'service') {
    const svc = selection.data
    const metrics = svc.repo_metrics
    return (
      <div class="inspector">
        <h2>{svc.name}</h2>
        <div class="inspector-badges">
          <span>{svc.team || 'default'}</span>
          <span class={'freshness ' + (svc.diffmind_freshness || 'unknown')}>{svc.diffmind_freshness || 'unknown'}</span>
        </div>
        <KV rows={[
          ['Repo', svc.repo_path || '-'],
          ['Primary language', metrics?.languages?.[0]?.language || '-'],
          ['LOC', metrics?.total_loc || 0],
          ['Routes', (svc.http_routes || []).length],
          ['Dependencies', (svc.dependencies || []).length],
          ['Flows', (svc.connections || []).length],
        ]} />
      </div>
    )
  }
  return (
    <div class="inspector">
      <h2>{selection.data?.name || selection.kind}</h2>
      <KV rows={Object.entries(selection.data || {}).slice(0, 12).map(([k, v]) => [k, typeof v === 'object' ? JSON.stringify(v) : String(v)])} />
    </div>
  )
}

function KV({ rows }) {
  return <div class="kv-list">{rows.map(([k, v]) => <div class="kv-row" key={k}><span>{k}</span><code>{v || '-'}</code></div>)}</div>
}

function AddRepoModal({ pid, onClose, onDone }) {
  const [source, setSource] = useState('git')
  const [name, setName] = useState('')
  const [path, setPath] = useState('')
  const [gitURL, setGitURL] = useState('')
  const [team, setTeam] = useState('default')
  const [error, setError] = useState('')
  const submit = async () => {
    setError('')
    try {
      await createRepo(pid, { name, path, git_url: gitURL, source_type: source, team, kind: 'service_repo' })
      onDone()
    } catch (e) { setError(e.message) }
  }
  return (
    <Modal title="Add repository" onClose={onClose}>
      <div class="segmented">
        <button class={source === 'git' ? 'active' : ''} onClick={() => setSource('git')}>Git</button>
        <button class={source === 'local' ? 'active' : ''} onClick={() => setSource('local')}>Local</button>
      </div>
      {source === 'git'
        ? <div class="field"><label>Git URL</label><input value={gitURL} onInput={(e) => setGitURL(e.target.value)} placeholder="https://github.com/org/repo.git" /></div>
        : <div class="field"><label>Path</label><input value={path} onInput={(e) => setPath(e.target.value)} placeholder="/abs/path/to/repo" /></div>}
      <div class="field"><label>Name</label><input value={name} onInput={(e) => setName(e.target.value)} placeholder="optional" /></div>
      <div class="field"><label>Team</label><input value={team} onInput={(e) => setTeam(e.target.value)} /></div>
      {error && <div class="banner error">{error}</div>}
      <div class="actions"><button class="btn" onClick={submit}>Add</button><button class="btn ghost" onClick={onClose}>Cancel</button></div>
    </Modal>
  )
}

function YamlModal({ pid, repo, onClose, onSaved }) {
  const [body, setBody] = useState('')
  const [error, setError] = useState('')
  useEffect(() => { getDiffMindYaml(pid, repo.id).then((r) => setBody(r.body || '')).catch((e) => setError(e.message)) }, [pid, repo.id])
  const save = async () => {
    setError('')
    try { await putDiffMindYaml(pid, repo.id, body); onSaved() }
    catch (e) { setError(e.message) }
  }
  return (
    <Modal title={`diffmind.yaml · ${repo.name}`} onClose={onClose} wide>
      <textarea class="code-editor" rows="24" value={body} onInput={(e) => setBody(e.target.value)} spellcheck={false} />
      {error && <div class="banner error">{error}</div>}
      <div class="actions"><button class="btn" onClick={save}>Save</button><button class="btn ghost" onClick={onClose}>Cancel</button></div>
    </Modal>
  )
}

function shortSha(v) {
  return v ? v.slice(0, 8) : '-'
}
