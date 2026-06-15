import { useEffect, useState } from 'preact/hooks'
import { selection, jobs, stages, runMeta, llmCalls } from '../lib/store.js'
import { getJob } from '../lib/api.js'

export function DetailDrawer() {
  const sel = selection.value
  const meta = runMeta.value
  const [detail, setDetail] = useState(null)

  useEffect(() => {
    setDetail(null)
    if (!sel || sel.type !== 'job' || !meta?.id) return
    const id = sel.id.replace(/^job:/, '')
    let cancelled = false
    getJob(meta.id, id).then((d) => { if (!cancelled) setDetail(d) }).catch(() => {})
    return () => { cancelled = true }
  }, [sel?.id, meta?.id])

  if (!sel) {
    return (
      <div class="empty">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor"><circle cx="12" cy="12" r="10" stroke-width="1.5" /><circle cx="12" cy="12" r="5" stroke-width="1.5" /><circle cx="12" cy="12" r="1.5" fill="currentColor" /></svg>
        <div>Click a stage or a node in the graph (or a row in the activity log) to inspect it.</div>
      </div>
    )
  }

  if (sel.type === 'stage') {
    const id = sel.id.replace(/^stage:/, '')
    const st = stages.value.get(id) || {}
    return (
      <div class="drawer">
        <h2>Stage · {id}</h2>
        <div class="muted">{st.tip || ''}</div>
        <dl class="kv">
          <dt>status</dt><dd>{st.status}</dd>
          <dt>progress</dt><dd>{st.done || 0} / {st.total || 0} ({Math.round(st.percent || 0)}%)</dd>
          <dt>started</dt><dd>{st.startedAt || '\u2013'}</dd>
          <dt>finished</dt><dd>{st.finishedAt || '\u2013'}</dd>
        </dl>
      </div>
    )
  }

  const id = sel.id.replace(/^job:/, '')
  const job = jobs.value.get(id)
  if (!job) {
    return (
      <div class="drawer">
        <h2>Job · {id}</h2>
        <div class="muted">No data yet.</div>
      </div>
    )
  }
  const llm = llmCalls.value.get(id)

  return (
    <div class="drawer">
      <h2>{job.payload?.name || id}</h2>
      <div class="muted">{job.stage} · {job.status}{job.payload?.duration_ms ? ` · ${job.payload.duration_ms}ms` : ''}</div>
      <dl class="kv">
        <dt>job id</dt><dd style="font-family: 'JetBrains Mono', monospace; font-size: 11px;">{id}</dd>
        {job.parentId && (<><dt>parent</dt><dd style="font-family: 'JetBrains Mono', monospace; font-size: 11px;">{job.parentId}</dd></>)}
        <dt>status</dt><dd>{job.status}</dd>
        {job.message && (<><dt>message</dt><dd>{job.message}</dd></>)}
        {job.startedAt && (<><dt>started</dt><dd>{job.startedAt}</dd></>)}
        {job.finishedAt && (<><dt>finished</dt><dd>{job.finishedAt}</dd></>)}
      </dl>

      <details>
        <summary>Payload</summary>
        <pre>{JSON.stringify(job.payload, null, 2)}</pre>
      </details>

      {llm && (
        <details>
          <summary>LLM call · {llm.session_id || ''}{llm.duration_ms ? ` · ${llm.duration_ms}ms` : ''}</summary>
          <pre>{JSON.stringify(llm, null, 2)}</pre>
        </details>
      )}

      <details open>
        <summary>Event history</summary>
        <pre>{JSON.stringify(job.history, null, 2)}</pre>
      </details>

      {detail && detail.prompt && (
        <details>
          <summary>Prompt</summary>
          <pre>{detail.prompt}</pre>
        </details>
      )}

      {detail && detail.response && (
        <details>
          <summary>LLM response (raw JSON)</summary>
          <pre>{detail.response}</pre>
        </details>
      )}
    </div>
  )
}
