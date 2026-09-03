import { useEffect, useState } from 'preact/hooks'
import { navigate } from '../lib/router.js'
import { createRepo, createRun, deleteRepo, getDiffMindConfigurationYaml, getIngestion, getRunArchGraph, getRunArchGraphResource, getRunArchGraphService, getRunArchGraphTrace, getWorkspace, importRepos, putDiffMindConfigurationYaml, startDiffMindBatch, startIngestion, startRepoDiffMind, syncRepo } from '../lib/api.js'
import { Modal, ConfirmDialog } from '../components/Modal.jsx'
import { GraphCanvas } from './GraphCanvas.jsx'
import { GraphDetailBody } from './GraphDetails.jsx'
import { StatusBadge } from './tabs/RunsTab.jsx'
import { PacksTab } from './tabs/PacksTab.jsx'
import { cancelIngestion, resumeIngestion } from '../lib/api.js'
import { ingestionCanResume, ingestionProgress } from '../lib/ingestion.js'
import { useProjectCapabilities } from '../lib/access.js'
import { enqueueRefresh } from '../lib/api.js'

export function ProjectWorkspace({ pid }) {
  const { data: caps, error: accessError } = useProjectCapabilities(pid)
  const [workspace, setWorkspace] = useState(null)
  const [ingestion, setIngestion] = useState(null)
  const [selected, setSelected] = useState(null)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [busy, setBusy] = useState('')
  const [pendingDiffMind, setPendingDiffMind] = useState({})
  const [pendingGraphRun, setPendingGraphRun] = useState(null)
  const [graphData, setGraphData] = useState(null)
  const [graphRunID, setGraphRunID] = useState('')
  const [graphDetailLoaded, setGraphDetailLoaded] = useState(false)
  const [graphLoading, setGraphLoading] = useState(false)
  const [graphError, setGraphError] = useState('')
  const [addOpen, setAddOpen] = useState(false)
  const [importOpen, setImportOpen] = useState(false)
  const [batchOpen, setBatchOpen] = useState(false)
  const [yamlRepo, setYamlRepo] = useState(null)
  const [diffmindRepo, setDiffMindRepo] = useState(null)
  const [deleteTarget, setDeleteTarget] = useState(null)
  const [packsOpen, setPacksOpen] = useState(false)

  const refresh = async () => {
    try {
      const [next, nextIngestion] = await Promise.all([
        getWorkspace(pid, { graph: false }),
        getIngestion(pid),
      ])
      setWorkspace(next)
      setIngestion(nextIngestion)
      setSelected((cur) => {
        if (cur?.kind === 'repo') {
          const repo = (next.repos || []).find((r) => r.id === cur.data.id)
          return repo ? { kind: 'repo', data: repo } : cur
        }
        if (cur?.kind === 'service') {
          const svc = (next.graph?.services || []).find((s) => s.name === cur.data.name)
          return svc ? { kind: 'service', data: svc } : cur
        }
        return cur
      })
      setPendingDiffMind((cur) => {
        const nextPending = { ...cur }
        for (const repo of next.repos || []) {
          if (repo.sync_status && repo.sync_status !== 'diffmind_running') {
            delete nextPending[repo.id]
          }
        }
        return nextPending
      })
      setPendingGraphRun((cur) => {
        const run = next.current_run
        if (!cur && !isGraphRunActive(run)) return null
        if (isGraphRunActive(run)) return run
        if (cur && run?.id === cur.id) return null
        return null
      })
      if (!(next.repos || []).some((repo) => repo.sync_status === 'diffmind_running') && !isGraphRunActive(next.current_run)) {
        setNotice('')
      }
      setError('')
    }
    catch (e) { setError(e.message) }
  }
  useEffect(() => { refresh() }, [pid])
  useEffect(() => {
    const run = workspace?.latest_run
    if (!run?.id || run.status !== 'completed') {
      setGraphData(null)
      setGraphRunID('')
      setGraphDetailLoaded(false)
      return
    }
    if (graphRunID === run.id && graphData) return
    let cancelled = false
    setGraphLoading(true)
    setGraphError('')
    getRunArchGraph(pid, run.id, { view: 'overview' })
      .then((graph) => {
        if (cancelled) return
        setGraphData(graph)
        setGraphRunID(run.id)
        setGraphDetailLoaded(false)
      })
      .catch((e) => {
        if (!cancelled) setGraphError(e.message)
      })
      .finally(() => {
        if (!cancelled) setGraphLoading(false)
      })
    return () => { cancelled = true }
  }, [pid, workspace?.latest_run?.id, workspace?.latest_run?.status])
  const repos = (workspace?.repos || []).map((repo) => {
    const pending = pendingDiffMind[repo.id]
    if (!pending) return repo
    if (repo.sync_status && repo.sync_status !== 'unknown' && repo.sync_status !== 'diffmind_running') return repo
    return { ...repo, sync_status: 'diffmind_running', sync_error: '', pending_diffmind_started_at: pending.started_at }
  })
  const hasRunningDiffMind = repos.some((r) => r.sync_status === 'diffmind_running')
  const currentGraphRun = workspace?.current_run || null
  const activeGraphRun = isGraphRunActive(currentGraphRun) ? currentGraphRun : pendingGraphRun
  const graphIsRunning = Boolean(activeGraphRun)
  const ingestionIsRunning = ingestion?.status === 'running'
  useEffect(() => {
    const t = setInterval(refresh, hasRunningDiffMind || graphIsRunning || ingestionIsRunning ? 2000 : 10000)
    return () => clearInterval(t)
  }, [pid, hasRunningDiffMind, graphIsRunning, ingestionIsRunning])

  const graph = graphData || workspace?.graph
  const selectedRepo = selected?.kind === 'repo' ? selected.data : null
  const selectedService = selected?.kind === 'service' ? selected.data : null
  const live = workspace?.live_status || {}

  const graphRun = async () => {
    setBusy('graph')
    try {
      const serviceRepos = repos.filter((r) => (r.kind || 'service_repo') !== 'infra_repo')
      const available = serviceRepos
        .map((r) => ({ repo: r, runID: diffmindRunID(r) }))
        .filter((r) => r.runID)
      const missing = serviceRepos.filter((r) => !diffmindRunID(r))
      if (!available.length) {
        throw new Error('No service repositories have generated DiffMind runs yet. Run DiffMind deterministic on at least one repo first.')
      }
      const refs = available.map(({ repo, runID }) => ({ repo_id: repo.id, diffmind_run_id: runID }))
      const run = await createRun(pid, { repos: refs })
      setPendingGraphRun(run)
      const skipped = missing.length ? ` ${missing.length} repos without DiffMind output were skipped.` : ''
      setNotice(`Graph build ${run.id} started from ${refs.length} DiffMind runs.${skipped} Status refreshes every 2 seconds while it is running.`)
      setTimeout(refresh, 500)
    } catch (e) { setError(e.message) }
    finally { setBusy('') }
  }

  const loadFullGraph = async () => {
    const runID = workspace?.latest_run?.id || graphRunID
    if (!runID || graphDetailLoaded) return
    setGraphLoading(true)
    setGraphError('')
    try {
      const full = await getRunArchGraph(pid, runID, { view: 'full' })
      setGraphData(full)
      setGraphRunID(runID)
      setGraphDetailLoaded(true)
    } catch (e) {
      setGraphError(e.message)
      throw e
    } finally {
      setGraphLoading(false)
    }
  }

  const handleGraphSelect = async (sel) => {
    setSelected(sel)
    const runID = workspace?.latest_run?.id || graphRunID
    if (!sel || !runID) return
    try {
      if (sel.kind === 'service' && sel.data?.name) {
        const view = await getRunArchGraphService(pid, runID, sel.data.name)
        setSelected((cur) => cur?.kind === 'service' && cur.data?.name === sel.data.name
          ? { kind: 'service', data: { ...(view.service || sel.data), service_view: view } }
          : cur)
      } else if (sel.kind === 'fact' && sel.data?.service) {
        const objectID = sel.data.objectID || sel.data.items?.[0]?.id || sel.data.items?.[0]?.details?.id || ''
        if (objectID) {
          const view = await getRunArchGraphTrace(pid, runID, { service: sel.data.service, object_id: objectID })
          setSelected((cur) => cur?.kind === 'fact' && cur.data?.service === sel.data.service && (cur.data?.objectID || '') === (sel.data?.objectID || '')
            ? { kind: 'fact', id: sel.id, data: { ...sel.data, trace_view: view } }
            : cur)
        }
      } else if (['db', 'queue', 'cache', 'object_storage', 'scheduler', 'workflow'].includes(sel.kind) && sel.id) {
        const view = await getRunArchGraphResource(pid, runID, sel.id)
        setSelected((cur) => cur?.id === sel.id
          ? { kind: sel.kind, id: sel.id, data: { ...(view.resource || sel.data), resource_view: view, services: view.services, tables: view.tables } }
          : cur)
      }
    } catch {
      // Keep the immediate overview selection; the banner already covers hard graph-load failures.
    }
  }

  const doSync = async (repo) => runAction('sync:' + repo.id, async () => { await syncRepo(pid, repo.id); await refresh() })
  const updateRepoStatus = (repoID, patch) => {
    setWorkspace((cur) => cur ? {
      ...cur,
      repos: (cur.repos || []).map((repo) => repo.id === repoID ? { ...repo, ...patch } : repo),
    } : cur)
    setSelected((cur) => cur?.kind === 'repo' && cur.data.id === repoID ? { kind: 'repo', data: { ...cur.data, ...patch } } : cur)
  }
  const doDiffMind = async (repo, options) => runAction('diffmind:' + repo.id, async () => {
    const startedAt = new Date().toISOString()
    await startRepoDiffMind(pid, repo.id, options)
    setPendingDiffMind((cur) => ({ ...cur, [repo.id]: { started_at: startedAt, options } }))
    updateRepoStatus(repo.id, { sync_status: 'diffmind_running', sync_error: '', pending_diffmind_started_at: startedAt })
    setNotice(`DiffMind deterministic run started for ${repo.name}. Status refreshes every 2 seconds while it is running.`)
    setDiffMindRepo(null)
    setTimeout(refresh, 500)
  })
  const doDelete = async (repo) => runAction('delete:' + repo.id, async () => { await deleteRepo(pid, repo.id); setDeleteTarget(null); await refresh() })
  const doImport = async (body) => runAction('import', async () => {
    const { run_pipeline: runPipeline, ...importRequest } = body
    const res = runPipeline && !importRequest.dry_run
      ? await startIngestion(pid, { import: importRequest, concurrency: importRequest.concurrency || 4 })
      : await importRepos(pid, importRequest)
    setImportOpen(false)
    if (runPipeline && !importRequest.dry_run) {
      setIngestion(res)
      setNotice('Repository ingestion started. DiffMind will import, sync, analyze, and build the graph automatically.')
    } else {
      setNotice(`Repository import processed ${res.count || 0} repositories.`)
    }
    await refresh()
  })
  const doBatchDiffMind = async (body) => runAction('batch-diffmind', async () => {
    const res = await startDiffMindBatch(pid, body)
    setBatchOpen(false)
    setNotice(`Batch DiffMind started for ${res.count || 0} repositories with concurrency ${res.concurrency || body.concurrency || 4}.`)
    setTimeout(refresh, 500)
  })
  const runAction = async (key, fn) => {
    setBusy(key); setError('')
    try { await fn() } catch (e) { setError(e.message) }
    finally { setBusy('') }
  }

  if (accessError) return <div class="page"><button class="btn ghost" onClick={() => navigate('/')}>Projects</button><p class="banner error">{accessError}</p></div>
  return (
    <div class="workspace">
      <header class="workspace-topbar">
        <div class="workspace-crumbs">
          <button class="btn ghost tiny" onClick={() => navigate('/')}>Projects</button>
          <h1>{workspace?.project?.name || pid}</h1>
          {workspace?.latest_run && <StatusBadge status={workspace.latest_run.status} />}
        </div>
        <div class="workspace-actions">
          <button class="btn ghost" onClick={() => navigate(`/projects/${encodeURIComponent(pid)}/operations`)}>Operations</button>
          {caps?.can_manage_access && <button class="btn ghost" onClick={() => navigate(`/projects/${encodeURIComponent(pid)}/access`)}>Project access</button>}
          <button class="btn ghost" onClick={() => navigate(`/projects/${encodeURIComponent(pid)}/compare`)}>Compare graphs</button>
          <button class="btn ghost" disabled={ingestionIsRunning} onClick={() => setPacksOpen(true)}>Knowledge packs</button>
          <button class="btn ghost" onClick={() => navigate(`/projects/${encodeURIComponent(pid)}/pull-requests`)}>
            PR impact{sumOpenPRs(live) > 0 ? ` · ${sumOpenPRs(live)}` : ''}
          </button>
          <button class="btn ghost" onClick={refresh}>Refresh</button>
          {ingestionIsRunning && caps?.can_refresh && <button class="btn danger" disabled={busy === 'ingestion'} onClick={() => runAction('ingestion', async () => { await cancelIngestion(pid); await refresh() })}>Cancel ingestion</button>}
          {caps?.can_configure && ingestionCanResume(ingestion) && <button class="btn" disabled={busy === 'ingestion'} onClick={() => runAction('ingestion', async () => { await resumeIngestion(pid); await refresh() })}>Resume / retry</button>}
          <button class="btn ghost" disabled={!caps?.can_refresh || !repos.length || ingestionIsRunning || hasRunningDiffMind || graphIsRunning || busy === 'ingestion'} onClick={() => runAction('ingestion', async () => { if (caps?.mode === 'scoped') { await enqueueRefresh(pid); await refresh(); setNotice('Refresh queued. Open Operations to follow it.') } else { await startIngestion(pid); await refresh() } })}>Update graph</button>
          <button class="btn" disabled={!caps?.can_configure || ingestionIsRunning} onClick={() => setImportOpen(true)}>{ingestionIsRunning ? 'Importing & building...' : 'Import & build'}</button>
          <button class="btn ghost" disabled={!caps?.can_configure || ingestionIsRunning} onClick={() => setAddOpen(true)}>Add repo</button>
          <button class="btn ghost" disabled={!caps?.can_configure || !repos.length || hasRunningDiffMind || ingestionIsRunning || busy === 'batch-diffmind'} onClick={() => setBatchOpen(true)}>{busy === 'batch-diffmind' ? 'Starting batch...' : 'Run DiffMind all'}</button>
          <button class="btn ghost" disabled={!caps?.can_configure || busy === 'graph' || graphIsRunning || ingestionIsRunning} onClick={graphRun}>{graphIsRunning ? 'Building graph...' : busy === 'graph' ? 'Starting...' : 'Build graph'}</button>
        </div>
      </header>

      {error && <div class="workspace-error banner error">{error}</div>}
      {graphError && <div class="workspace-error banner error">Graph load failed: {graphError}</div>}
      {currentGraphRun?.status === 'failed' && currentGraphRun.error && <div class="workspace-error banner error">Graph build failed: {currentGraphRun.error}</div>}
      {notice && <div class="workspace-notice banner ok">{notice}</div>}
      {ingestion && !['running', 'not_started'].includes(ingestion.status) && <IngestionResult ingestion={ingestion} />}
      {packsOpen && (
        <Modal title="Knowledge packs" onClose={() => setPacksOpen(false)} wide>
          <p class="muted">Teach DiffMind your organization’s repository conventions with deterministic, versioned extraction rules.</p>
          <PacksTab pid={pid} capabilities={caps} />
        </Modal>
      )}
      <GraphQualityBanner quality={(currentGraphRun?.graph_quality || workspace?.latest_run?.graph_quality)} />
      {(ingestionIsRunning || (!ingestionIsRunning && (hasRunningDiffMind || graphIsRunning))) && (
        <div class="workspace-activity-stack">
          {ingestionIsRunning
            ? <IngestionActivity ingestion={ingestion} />
            : <>{hasRunningDiffMind && <DiffMindActivity repos={repos} />}{graphIsRunning && <GraphActivity run={activeGraphRun} />}</>}
        </div>
      )}

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
        {graph
          ? <GraphCanvas graph={graph} onSelect={handleGraphSelect} detailLoaded={graphDetailLoaded} onRequestFullDetail={loadFullGraph} />
          : graphLoading
            ? <GraphLoading run={workspace?.latest_run} />
            : <EmptyBoard repos={repos} busy={ingestionIsRunning} canConfigure={caps?.can_configure} onImport={() => setImportOpen(true)} onAdd={() => setAddOpen(true)} />}
      </main>

      <aside class="workspace-right">
        <Inspector
          selection={selected}
          live={selectedRepo ? live[selectedRepo.id] : null}
          onSync={selectedRepo ? () => doSync(selectedRepo) : null}
          onDiffMind={selectedRepo ? () => setDiffMindRepo(selectedRepo) : null}
          onYaml={selectedRepo ? () => setYamlRepo(selectedRepo) : null}
          onDelete={selectedRepo && caps?.can_delete ? () => setDeleteTarget(selectedRepo) : null}
          busy={busy}
          locked={ingestionIsRunning || !caps?.can_configure}
        />
      </aside>

      <footer class="workspace-status">
        <span>{repos.length} repos</span>
        <span>{(workspace?.teams || []).length} teams</span>
        <span>{graph ? `${(graph.services || []).length} services` : 'no graph yet'}</span>
        <span>{currentGraphRun ? `graph ${currentGraphRun.status}` : 'graph idle'}</span>
        <span>{ingestion?.status && ingestion.status !== 'not_started' ? `ingestion ${ingestion.status}` : 'ingestion idle'}</span>
        <span>{repos.filter((r) => r.freshness === 'stale').length} stale</span>
      </footer>

      {addOpen && <AddRepoModal pid={pid} onClose={() => setAddOpen(false)} onDone={() => { setAddOpen(false); refresh() }} />}
      {importOpen && <ImportOrgModal busy={busy === 'import'} onClose={() => setImportOpen(false)} onImport={doImport} />}
      {batchOpen && <BatchDiffMindModal repoCount={repos.length} busy={busy === 'batch-diffmind'} onClose={() => setBatchOpen(false)} onRun={doBatchDiffMind} />}
      {yamlRepo && <YamlModal pid={pid} repo={yamlRepo} onClose={() => setYamlRepo(null)} onSaved={() => { setYamlRepo(null); refresh() }} />}
      {diffmindRepo && <DiffMindRunModal repo={diffmindRepo} busy={busy === 'diffmind:' + diffmindRepo.id} onClose={() => setDiffMindRepo(null)} onRun={(options) => doDiffMind(diffmindRepo, options)} />}
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

function GraphQualityBanner({ quality }) {
  const warnings = quality?.warnings || []
  if (!warnings.length) return null
  return (
    <details class="banner warn graph-quality">
      <summary>
        <strong>Graph quality warnings</strong>
        <span>{warnings.length}</span>
      </summary>
      <ul>
        {warnings.map((w, i) => <li key={i}>{w}</li>)}
      </ul>
    </details>
  )
}

function RepoButton({ repo, live, active, onClick }) {
  const primary = repo.repo_metrics?.languages?.[0]?.language || 'unknown'
  const status = runStatusLabel(repo.sync_status)
  return (
    <button class={'repo-pill ' + (active ? 'active ' : '') + (repo.freshness === 'stale' ? 'stale' : '')} onClick={onClick}>
      <span class="repo-name">{repo.name}</span>
      <span class="repo-meta">{primary} · {repo.freshness || 'unknown'} · PR {live?.pull_requests ?? '-'}</span>
      {status && <span class={'repo-run-status ' + status.kind}>{status.label}</span>}
    </button>
  )
}

function sumOpenPRs(live) {
  return Object.values(live || {}).reduce((sum, item) => sum + (Number(item?.pull_requests) || 0), 0)
}

function DiffMindActivity({ repos }) {
  const running = repos.filter((r) => r.sync_status === 'diffmind_running')
  return (
    <div class="diffmind-activity" role="status" aria-live="polite">
      <div class="activity-spinner" />
      <div>
        <strong>DiffMind running</strong>
        <span>{running.map((r) => r.name).join(', ')}</span>
      </div>
    </div>
  )
}

function GraphActivity({ run }) {
  return (
    <div class="graph-activity" role="status" aria-live="polite">
      <div class="activity-spinner" />
      <div>
        <strong>Graph build {run?.status || 'running'}</strong>
        <span>{run?.id || 'starting'} · {(run?.repos || []).length} DiffMind runs</span>
      </div>
    </div>
  )
}

function IngestionActivity({ ingestion }) {
  const processed = ingestionProgress(ingestion)
  const total = ingestion.repositories || ingestion.discovered || 0
  return (
    <div class="ingestion-activity" role="status" aria-live="polite">
      <div class="activity-spinner" />
      <div>
        <strong>{ingestionPhaseLabel(ingestion.phase)}</strong>
        <span>{total ? `${processed} of ${total} repositories processed` : 'Discovering repositories'}{ingestion.source ? ` · ${ingestion.source}` : ''}</span>
        <span>{ingestion.reused || 0} unchanged repositories reused</span>
        <details><summary>Repository progress</summary><ul>{(ingestion.repo_progress || []).map((repo) => <li key={repo.repo_id}>{repo.repo_id}: {repo.status}{repo.error ? ` — ${repo.error}` : ''}</li>)}</ul></details>
      </div>
    </div>
  )
}

function IngestionResult({ ingestion }) {
  const errors = ingestion.errors || []
  const failed = ['failed', 'interrupted', 'cancelled'].includes(ingestion.status)
  return (
    <details class={'workspace-ingestion-result banner ' + (failed ? 'error' : ingestion.status === 'partial' ? 'warn' : 'ok')}>
      <summary>
        <strong>Last ingestion {ingestion.status}</strong>
        <span>{ingestion.analyzed || 0} analyzed · {ingestion.reused || 0} reused · attempt {ingestion.attempt || 1} · {ingestion.graph_run_id ? 'graph run recorded' : 'no graph'}</span>
      </summary>
      {errors.length > 0 && <ul>{errors.map((message, index) => <li key={index}>{message}</li>)}</ul>}
    </details>
  )
}

function ingestionPhaseLabel(phase) {
  switch (phase) {
    case 'discovering': return 'Discovering repositories'
    case 'importing': return 'Importing repositories'
    case 'processing_repositories': return 'Syncing and analyzing repositories'
    case 'building_graph': return 'Building the system graph'
    case 'resuming': return 'Resuming ingestion from saved checkpoints'
    default: return 'Starting repository ingestion'
  }
}

function diffmindRunID(repo) {
  return repo?.latest_diffmind_run?.run_id || repo?.last_diffmind_run_id || ''
}

function isGraphRunActive(run) {
  return run?.status === 'running' || run?.status === 'cancelling'
}

function EmptyBoard({ repos, busy, canConfigure, onImport, onAdd }) {
  return (
    <div class="workspace-empty">
      <h2>Graph workspace</h2>
      <p>{!canConfigure ? 'No saved graph yet. Ask an administrator to configure repositories and build the first graph.' : repos.length ? 'Import more repositories and rebuild everything in one operation, or use the manual controls above.' : 'Import your organization or a local repository directory to build the first graph.'}</p>
      <div class="actions">
        <button class="btn" disabled={busy || !canConfigure} onClick={onImport}>{busy ? 'Building graph...' : 'Import repositories and build graph'}</button>
        <button class="btn ghost" disabled={busy || !canConfigure} onClick={onAdd}>Add one repository</button>
      </div>
    </div>
  )
}

function GraphLoading({ run }) {
  return (
    <div class="workspace-empty graph-loading-panel">
      <div class="activity-spinner" />
      <h2>Loading graph</h2>
      <p>Opening project metadata first. The large graph loads separately so the workspace stays responsive.</p>
      {run?.id && <code>{run.id}</code>}
    </div>
  )
}

function Inspector({ selection, live, onSync, onDiffMind, onYaml, onDelete, busy, locked }) {
  if (!selection) return <div class="inspector-empty">Select a repo, service, resource, or edge.</div>
  if (selection.kind === 'repo') {
    const repo = selection.data
    const metrics = repo.repo_metrics
    const isRunning = repo.sync_status === 'diffmind_running'
    return (
      <div class="inspector">
        <h2>{repo.name}</h2>
        <div class="inspector-badges">
          <span class={'freshness ' + (repo.freshness || 'unknown')}>{repo.freshness || 'unknown'}</span>
          <span>{repo.effective_team || 'default'}</span>
          <span>{repo.source_type || 'local'}</span>
          {repo.sync_status && <span class={'run-state ' + repo.sync_status}>{repo.sync_status}</span>}
        </div>
        <div class="inspector-actions">
          <button class="btn ghost" disabled={locked || busy === 'sync:' + repo.id || isRunning} onClick={onSync}>Sync git</button>
          <button class="btn" disabled={locked || busy === 'diffmind:' + repo.id || isRunning} onClick={onDiffMind}>{isRunning ? 'DiffMind running...' : 'Configure run'}</button>
          <button class="btn ghost" disabled={locked} onClick={onYaml}>Configuration</button>
          <button class="btn danger" disabled={locked || !onDelete} onClick={onDelete}>Remove</button>
        </div>
        {isRunning && (
          <div class="run-progress-card">
            <div class="activity-spinner" />
            <div>
              <strong>Run in progress</strong>
              <span>DiffMind is polling this repository every 2 seconds.</span>
            </div>
          </div>
        )}
        {repo.sync_status === 'diffmind_failed' && repo.sync_error && <div class="banner error">{repo.sync_error}</div>}
        <KV rows={[
          ['Path', repo.path],
          ['Git URL', repo.git_url || '-'],
          ['Branch', repo.default_branch || '-'],
          ['HEAD', shortSha(repo.head_sha)],
          ['Remote', shortSha(repo.remote_head_sha)],
          ['DiffMind run', repo.latest_diffmind_run?.run_id || '-'],
          ['Status', repo.sync_status || '-'],
          ['Error', repo.sync_error || '-'],
          ['LOC', metrics?.total_loc || 0],
          ['Open PRs', live?.pull_requests ?? '-'],
          ['Open issues', live?.issues ?? '-'],
          ['Actions', live?.actions_state || '-'],
        ]} />
      </div>
    )
  }
  if (selection.kind === 'service') {
    const svc = selection.data
    return (
      <div class="inspector">
        <h2>{svc.name}</h2>
        <div class="inspector-badges">
          <span>{svc.team || 'default'}</span>
          <span class={'freshness ' + (svc.diffmind_freshness || 'unknown')}>{svc.diffmind_freshness || 'unknown'}</span>
        </div>
        <GraphDetailBody sel={selection} />
      </div>
    )
  }
  if (['edge', 'group', 'fact', 'queue', 'db', 'scheduler'].includes(selection.kind)) {
    return (
      <div class="inspector">
        <h2>{selection.data?.name || selection.id || selection.kind}</h2>
        <GraphDetailBody sel={selection} />
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

function runStatusLabel(status) {
  switch (status) {
    case 'diffmind_running':
      return { kind: 'running', label: 'Running' }
    case 'diffmind_completed':
      return { kind: 'completed', label: 'Completed' }
    case 'diffmind_failed':
      return { kind: 'failed', label: 'Failed' }
    case 'syncing':
      return { kind: 'running', label: 'Syncing' }
    default:
      return null
  }
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

function ImportOrgModal({ busy, onClose, onImport }) {
  const [provider, setProvider] = useState('github')
  const [org, setOrg] = useState('')
  const [root, setRoot] = useState('')
  const [apiBase, setAPIBase] = useState('')
  const [include, setInclude] = useState('')
  const [exclude, setExclude] = useState('')
  const [team, setTeam] = useState('default')
  const [limit, setLimit] = useState('')
  const [clone, setClone] = useState(true)
  const [cloneTransport, setCloneTransport] = useState('auto')
  const [concurrency, setConcurrency] = useState('4')
  const [recursive, setRecursive] = useState(false)
  const [maxDepth, setMaxDepth] = useState('2')
  const [dryRun, setDryRun] = useState(false)
  const [runPipeline, setRunPipeline] = useState(true)
  const [error, setError] = useState('')
  const submit = async () => {
    setError('')
    try {
      await onImport({
        provider,
        org,
        root,
        api_base: apiBase,
        include,
        exclude,
        team,
        limit: limit === '' ? 0 : Number(limit),
        clone: runPipeline && !dryRun ? true : clone,
        clone_transport: cloneTransport,
        concurrency: concurrency === '' ? 4 : Number(concurrency),
        recursive,
        max_depth: maxDepth === '' ? 2 : Number(maxDepth),
        dry_run: dryRun,
        run_pipeline: runPipeline,
      })
    } catch (e) { setError(e.message) }
  }
  return (
    <Modal title="Import repositories" onClose={onClose} wide>
      <div class="mode-toggle">
        <button class={provider === 'github' ? 'active' : ''} onClick={() => setProvider('github')}>GitHub org</button>
        <button class={provider === 'local' ? 'active' : ''} onClick={() => setProvider('local')}>Local directory</button>
      </div>
      {provider === 'github' ? (
        <div class="option-grid">
          <TextField label="GitHub org" value={org} onInput={setOrg} placeholder="company" />
          <TextField label="API base" value={apiBase} onInput={setAPIBase} placeholder="https://api.github.com" />
          <TextField label="Team" value={team} onInput={setTeam} />
        </div>
      ) : (
        <div class="option-grid">
          <TextField label="Root directory" value={root} onInput={setRoot} placeholder="/path/to/repos" />
          <TextField label="Team" value={team} onInput={setTeam} />
          <NumberField label="Max depth" value={maxDepth} onInput={setMaxDepth} min="1" max="8" />
        </div>
      )}
      <div class="option-grid">
        <TextField label="Include regex" value={include} onInput={setInclude} placeholder=".*-api$" />
        <TextField label="Exclude regex" value={exclude} onInput={setExclude} placeholder="archive|template" />
        <NumberField label="Limit" value={limit} onInput={setLimit} placeholder="0 = all" min="0" />
      </div>
      <div class="check-grid">
        {provider === 'github' && <Check label="Clone repositories" checked={runPipeline && !dryRun ? true : clone} disabled={runPipeline && !dryRun} onInput={setClone} />}
        {provider === 'local' && <Check label="Recursive scan" checked={recursive} onInput={setRecursive} />}
        <Check label="Dry run only" checked={dryRun} onInput={setDryRun} />
        <Check label="Sync, analyze, and build graph" checked={runPipeline && !dryRun} disabled={dryRun} onInput={setRunPipeline} />
      </div>
      {provider === 'github' && (
        <div class="option-grid">
          <div class="field">
            <label>Clone transport</label>
            <select value={cloneTransport} onInput={(e) => setCloneTransport(e.currentTarget.value)}>
              <option value="auto">Auto</option>
              <option value="https">HTTPS</option>
              <option value="ssh">SSH</option>
            </select>
          </div>
          <NumberField label="Clone concurrency" value={concurrency} onInput={setConcurrency} min="1" max="16" />
        </div>
      )}
      <p class="muted small">
        {provider === 'github'
          ? <>Uses <code>GITHUB_TOKEN</code>, <code>GH_TOKEN</code>, or <code>gh auth token</code>. Auto prefers HTTPS when a token is available and SSH otherwise.</>
          : <>Scans for directories containing <code>.git</code>. Imported repos keep their existing local paths; no clone is performed.</>}
      </p>
      {error && <div class="banner error">{error}</div>}
      <div class="actions">
        <button class="btn" disabled={busy || (provider === 'github' ? !org.trim() : !root.trim())} onClick={submit}>{busy ? 'Starting...' : dryRun ? 'Preview import' : runPipeline ? 'Import and build graph' : 'Import repositories'}</button>
        <button class="btn ghost" disabled={busy} onClick={onClose}>Cancel</button>
      </div>
    </Modal>
  )
}

const defaultDiffMindOptions = {
  config_path: '',
  out_dir: '',
  log_file: '',
  workers: '',
  min_confidence: '',
  verbose: false,
  trace: false,
}

function BatchDiffMindModal({ repoCount, busy, onClose, onRun }) {
  const [opts, setOpts] = useState({ ...defaultDiffMindOptions })
  const [concurrency, setConcurrency] = useState('4')
  const [skipFresh, setSkipFresh] = useState(true)
  const [error, setError] = useState('')
  const set = (key, value) => setOpts((cur) => ({ ...cur, [key]: value }))
  const payload = diffmindPayload(opts)
  const run = async () => {
    setError('')
    try {
      await onRun({
        all: true,
        skip_fresh: skipFresh,
        concurrency: concurrency === '' ? 4 : Number(concurrency),
        options: payload,
      })
    } catch (e) { setError(e.message) }
  }
  return (
    <Modal title="Run DiffMind on all repositories" onClose={onClose} wide>
      <div class="run-options-layout">
        <section class="run-options-section">
          <h3>Batch</h3>
          <KV rows={[['Repositories', repoCount], ['Pipeline', 'deterministic']]} />
          <NumberField label="Concurrency" value={concurrency} onInput={setConcurrency} min="1" max="16" />
          <Check label="Skip fresh repositories" checked={skipFresh} onInput={setSkipFresh} />
          <NumberField label="Workers per DiffMind run" value={opts.workers} onInput={(v) => set('workers', v)} />
          <NumberField label="Min confidence" value={opts.min_confidence} onInput={(v) => set('min_confidence', v)} step="0.01" min="0" max="1" />
          <div class="check-grid">
            <Check label="Verbose" checked={opts.verbose} onInput={(v) => set('verbose', v)} />
            <Check label="Trace" checked={opts.trace} onInput={(v) => set('trace', v)} />
          </div>
        </section>
        <section class="run-options-section command-section">
          <h3>Command Preview</h3>
          <pre class="command-preview">{`diffmind run --repo <each selected repo>\nbatch concurrency: ${concurrency || 4}`}</pre>
        </section>
      </div>
      {error && <div class="banner error">{error}</div>}
      <div class="actions">
        <button class="btn" disabled={busy} onClick={run}>{busy ? 'Starting...' : 'Start batch'}</button>
        <button class="btn ghost" disabled={busy} onClick={onClose}>Cancel</button>
      </div>
    </Modal>
  )
}

function DiffMindRunModal({ repo, busy, onClose, onRun }) {
  const [opts, setOpts] = useState(defaultDiffMindOptions)
  const [error, setError] = useState('')
  const set = (key, value) => setOpts((cur) => ({ ...cur, [key]: value }))
  const payload = diffmindPayload(opts)
  const command = diffmindCommandPreview(repo.path, payload)
  const run = async () => {
    setError('')
    try { await onRun(payload) }
    catch (e) { setError(e.message) }
  }
  return (
    <Modal title={`DiffMind run · ${repo.name}`} onClose={onClose} wide>
      <div class="run-options-layout">
        <section class="run-options-section">
          <h3>Run</h3>
          <KV rows={[['Pipeline', 'deterministic']]} />
          <TextField label="Config JSON" value={opts.config_path} onInput={(v) => set('config_path', v)} placeholder="/abs/path/config.json" />
          <TextField label="Output directory" value={opts.out_dir} onInput={(v) => set('out_dir', v)} placeholder="default ~/.diffmind/runs" />
          <TextField label="Log file" value={opts.log_file} onInput={(v) => set('log_file', v)} placeholder="/abs/path/diffmind.log" />
          <div class="option-grid">
            <NumberField label="Workers" value={opts.workers} onInput={(v) => set('workers', v)} />
            <NumberField label="Min confidence" value={opts.min_confidence} onInput={(v) => set('min_confidence', v)} step="0.01" min="0" max="1" />
          </div>
          <div class="check-grid">
            <Check label="Verbose" checked={opts.verbose} onInput={(v) => set('verbose', v)} />
            <Check label="Trace" checked={opts.trace} onInput={(v) => set('trace', v)} />
          </div>
        </section>

        <section class="run-options-section command-section">
          <h3>Command Preview</h3>
          <pre class="command-preview">{command}</pre>
        </section>
      </div>
      {error && <div class="banner error">{error}</div>}
      <div class="actions">
        <button class="btn" disabled={busy} onClick={run}>{busy ? 'Starting...' : 'Start run'}</button>
        <button class="btn ghost" disabled={busy} onClick={onClose}>Cancel</button>
      </div>
    </Modal>
  )
}

function TextField({ label, value, onInput, placeholder, type = 'text', disabled, min, max, step }) {
  return (
    <div class="field">
      <label>{label}</label>
      <input type={type} value={value} disabled={disabled} min={min} max={max} step={step} placeholder={placeholder || ''} onInput={(e) => onInput(e.target.value)} />
    </div>
  )
}

function NumberField({ label, value, onInput, placeholder, disabled, min, max, step = '1' }) {
  return <TextField label={label} type="number" value={value} disabled={disabled} placeholder={placeholder} onInput={onInput} min={min} max={max} step={step} />
}

function Check({ label, checked, onInput, disabled }) {
  return (
    <label class={'check-row ' + (disabled ? 'disabled' : '')}>
      <input type="checkbox" checked={checked} disabled={disabled} onInput={(e) => onInput(e.target.checked)} />
      <span>{label}</span>
    </label>
  )
}

function diffmindPayload(opts) {
  const out = {}
  const stringKeys = ['config_path', 'out_dir', 'log_file']
  stringKeys.forEach((k) => { if (opts[k]) out[k] = opts[k] })
  const intKeys = ['workers']
  intKeys.forEach((k) => {
    if (opts[k] !== '') out[k] = Number(opts[k])
  })
  const boolKeys = ['verbose', 'trace']
  boolKeys.forEach((k) => { if (opts[k]) out[k] = true })
  if (opts.min_confidence !== '') out.min_confidence = Number(opts.min_confidence)
  return out
}

function diffmindCommandPreview(repoPath, opts) {
  const args = ['diffmind', 'run', '--repo', shellArg(repoPath || '<repo>')]
  const add = (flag, value, secret) => {
    if (value !== undefined && value !== null && value !== '') args.push(flag, shellArg(secret ? '********' : value))
  }
  const addBool = (flag, value) => { if (value) args.push(flag) }
  add('--config', opts.config_path)
  add('--out', opts.out_dir)
  add('--log-file', opts.log_file)
  add('--workers', opts.workers)
  add('--min-confidence', opts.min_confidence)
  addBool('--verbose', opts.verbose)
  addBool('--trace', opts.trace)
  return args.join(' ')
}

function shellArg(v) {
  const s = String(v)
  if (/^[A-Za-z0-9_./:=@+-]+$/.test(s)) return s
  return `'${s.replace(/'/g, `'\\''`)}'`
}

function YamlModal({ pid, repo, onClose, onSaved }) {
  const [body, setBody] = useState('')
  const [error, setError] = useState('')
  useEffect(() => { getDiffMindConfigurationYaml(pid, repo.id).then((r) => setBody(r.body || '')).catch((e) => setError(e.message)) }, [pid, repo.id])
  const save = async () => {
    setError('')
    try { await putDiffMindConfigurationYaml(pid, repo.id, body); onSaved() }
    catch (e) { setError(e.message) }
  }
  return (
    <Modal title={`diffmind-configuration.yaml · ${repo.name}`} onClose={onClose} wide>
      <textarea class="code-editor" rows="24" value={body} onInput={(e) => setBody(e.target.value)} spellcheck={false} />
      {error && <div class="banner error">{error}</div>}
      <div class="actions"><button class="btn" onClick={save}>Save</button><button class="btn ghost" onClick={onClose}>Cancel</button></div>
    </Modal>
  )
}

function shortSha(v) {
  return v ? v.slice(0, 8) : '-'
}
