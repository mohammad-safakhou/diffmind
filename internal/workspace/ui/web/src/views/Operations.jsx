import { useEffect, useState } from 'preact/hooks'
import { listJobs, enqueueRefresh, cancelJob, retryJob, ingestionHistory, getCapabilities } from '../lib/api.js'
import { jobCanCancel, jobCanRetry, jobStatus } from '../lib/operations.js'
import { navigate } from '../lib/router.js'
import { ProjectLimits } from './ProjectLimits.jsx'
import './Operations.css'

const timestamp = (value) => !value || value.startsWith('0001-') ? '—' : new Date(value).toLocaleString()

export function Operations({ pid }) {
  const [data, setData] = useState(null)
  const [history, setHistory] = useState(null)
  const [role, setRole] = useState('viewer')
  const [offset, setOffset] = useState(0)
  const [historyOffset, setHistoryOffset] = useState(0)
  const [reload, setReload] = useState(0)
  const [error, setError] = useState('')
  const [actionError, setActionError] = useState('')
  const [notice, setNotice] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    let alive = true, timer
    setData(null); setHistory(null)
    const refresh = async () => {
      try {
        const [jobs, attempts, session] = await Promise.all([listJobs(pid, offset), ingestionHistory(pid, historyOffset), getCapabilities(pid)])
        if (alive) { setData(jobs); setHistory(attempts); setRole(session.role); setError('') }
      } catch (e) { if (alive) { setError(e.message); setData(null); setHistory(null); setRole('viewer') } }
      finally { if (alive) timer = setTimeout(refresh, 3000) }
    }
    refresh()
    return () => { alive = false; clearTimeout(timer) }
  }, [pid, offset, historyOffset, reload])

  const action = async (fn, message) => {
    setBusy(true); setActionError(''); setNotice('')
    try { await fn(); setNotice(message); setReload((v) => v + 1) }
    catch (e) { setActionError(e.message) }
    finally { setBusy(false) }
  }
  const openGraph = (id) => navigate(`/projects/${encodeURIComponent(pid)}/runs/${encodeURIComponent(id)}`)

  return <main class="operations-page">
    <header class="operations-header">
      <button class="btn ghost" onClick={() => navigate(`/projects/${encodeURIComponent(pid)}`)}>← {pid}</button>
      <div><h1>Operations</h1><p class="muted">Durable refresh jobs and ingestion attempts. Updates every 3 seconds.</p></div>
      <button class="btn" disabled={busy || !['editor', 'admin'].includes(role)} onClick={() => action(() => enqueueRefresh(pid), 'Project refresh queued. Existing queued manual work may be reused.')}>Queue refresh</button>
    </header>
    {error && <p class="banner error" role="alert">Could not load operations: {error}</p>}
    {actionError && <p class="banner error" role="alert">{actionError}</p>}
    {notice && <p class="banner ok" role="status">{notice}</p>}
    {!data && !error && <p role="status">Loading operations…</p>}
    <ProjectLimits key={pid} pid={pid} canManage={role === 'admin'} />
    {data && <>
      <p class="operations-limits">{data.workers} project workers · {data.repository_workers} global repository slots · queue capacity {data.capacity} · scheduler {data.healthy ? 'healthy' : 'stopped — check server logs'}</p>
      <section aria-label="Refresh jobs">
        <h2>Refresh jobs <span class="muted">({data.total})</span></h2>
        <p class="muted">Manual, scheduled, and signed webhook requests share this queue. Failed attempts retry automatically, up to three attempts per submission or explicit retry.</p>
        {!data.jobs.length && <p>No refresh jobs yet. Queue a refresh or configure company refresh/webhooks.</p>}
        {data.jobs.map((job) => <article class="operation-card" key={job.id}>
          <div class="operation-title"><strong>{jobStatus(job)}</strong><span>{job.trigger} · {timestamp(job.created_at)}</span>
            {jobCanCancel(job, role) && <button class="btn ghost tiny" disabled={busy} onClick={() => action(() => cancelJob(job.id), 'Cancellation requested; active processes will drain.')}>Cancel</button>}
            {jobCanRetry(job, role) && <button class="btn ghost tiny" disabled={busy} onClick={() => action(() => retryJob(job.id), 'Retry queued; earlier attempts remain in history.')}>Retry</button>}
          </div>
          <p class="mono muted">{job.id}</p>
          {job.status === 'queued' && <p class="muted">Eligible after {timestamp(job.not_before)}; busy projects wait without consuming an attempt.</p>}
          <details><summary>{job.attempts.length} attempts · limit {job.max_attempts}</summary>
            {job.attempts.map((attempt) => <div class="operation-attempt" key={attempt.number}>
              <strong>Attempt {attempt.number}: {attempt.status}</strong>
              <p>{timestamp(attempt.started_at)} → {timestamp(attempt.finished_at)}</p>
              <p>{attempt.synced} synced · {attempt.analyzed} analyzed · {attempt.reused} reused</p>
              {attempt.error && <pre class="operation-error">{attempt.error}</pre>}
              {attempt.ingestion_id && <p class="mono muted">Ingestion: {attempt.ingestion_id}</p>}
              {attempt.graph_run_id && <button class="btn ghost tiny" onClick={() => openGraph(attempt.graph_run_id)}>Open graph {attempt.graph_run_id}</button>}
            </div>)}
          </details>
        </article>)}
        <PageControls data={data} offset={offset} setOffset={setOffset} label="Job pages" />
      </section>
    </>}
    {history && <section aria-label="Ingestion history">
      <h2>Ingestion attempts <span class="muted">({history.total})</span></h2>
      <p class="muted">Includes direct imports, builds, and queued refresh work. Request bodies are omitted. Each retry retains the previous attempt and its timestamps.</p>
      {!history.attempts.length && <p>No ingestion attempts recorded.</p>}
      {history.attempts.map((attempt) => <details class="operation-card" key={`${attempt.id}:${attempt.attempt}`}>
        <summary><strong>{attempt.status}</strong> · Attempt {attempt.attempt || 1} · {timestamp(attempt.attempt_started_at || attempt.started_at)}</summary>
        <p class="mono muted">{attempt.id}{attempt.job_id ? ` · Job ${attempt.job_id}` : ''}</p>
        <p>{attempt.analyzed} analyzed · {attempt.reused} reused · finished {timestamp(attempt.finished_at)}</p>
        {attempt.errors?.map((message, i) => <pre class="operation-error" key={i}>{message}</pre>)}
        {attempt.repo_progress?.map((repo) => <p key={repo.repo_id}><strong>{repo.repo_id}</strong>: {repo.status} {repo.error || ''}</p>)}
        {attempt.graph_run_id && <button class="btn ghost tiny" onClick={() => openGraph(attempt.graph_run_id)}>Open graph {attempt.graph_run_id}</button>}
      </details>)}
      <PageControls data={history} offset={historyOffset} setOffset={setHistoryOffset} label="Attempt pages" />
    </section>}
  </main>
}

function PageControls({ data, offset, setOffset, label }) {
  return <nav class="operations-pagination" aria-label={label}>
    <button class="btn ghost tiny" disabled={!offset} onClick={() => setOffset(Math.max(0, offset - 25))}>Previous</button>
    <span>{data.total ? offset + 1 : 0}–{Math.min(offset + 25, data.total)} of {data.total}</span>
    <button class="btn ghost tiny" disabled={data.next_offset == null} onClick={() => setOffset(data.next_offset)}>Next</button>
  </nav>
}
