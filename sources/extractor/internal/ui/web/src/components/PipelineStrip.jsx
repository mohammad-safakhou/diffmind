import { stages, runMeta } from '../lib/store.js'

// Six-stage strip across the top of the workspace. Each stage shows current
// status, count, progress, and a one-line tip.
const ORDER = ['repo_facts', 'discovery', 'reexamination', 'detail', 'connections', 'reconcile']
const PRETTY = {
  repo_facts: 'Repo Facts',
  discovery: 'Discovery',
  reexamination: 'Re-examination',
  detail: 'Detail',
  connections: 'Connections',
  reconcile: 'Reconcile',
}

export function PipelineStrip() {
  const map = stages.value
  const meta = runMeta.value
  const elapsed = meta && meta.startedAt ? humanDuration(Date.now() - new Date(meta.startedAt).getTime()) : '–'

  return (
    <div class="pipeline-strip">
      {ORDER.map((id) => {
        const s = map.get(id) || { name: id, status: 'pending', percent: 0 }
        const pct = Math.max(0, Math.min(100, s.percent || 0))
        return (
          <div class={'pipeline-stage ' + s.status} key={id}>
            <div class="name">{PRETTY[id]}</div>
            <div class="count">
              {s.done || 0}<span style="color: var(--text-dim)">/{s.total || 0}</span>
            </div>
            <div class="progress"><span style={'width: ' + pct + '%'} /></div>
            <div style="font-size:11px; color: var(--text-dim); height: 14px; overflow: hidden;">{s.tip}</div>
          </div>
        )
      })}
      <div class="pipeline-stage" style="flex: 0 0 130px; border-right: none;">
        <div class="name">Elapsed</div>
        <div class="count">{elapsed}</div>
        <div style="font-size:11px; color: var(--text-dim);">{meta?.status || 'waiting'}</div>
      </div>
    </div>
  )
}

function humanDuration(ms) {
  if (!Number.isFinite(ms) || ms < 0) return '–'
  const s = Math.floor(ms / 1000)
  if (s < 60) return s + 's'
  const m = Math.floor(s / 60)
  const r = s % 60
  if (m < 60) return m + 'm ' + r + 's'
  const h = Math.floor(m / 60)
  return h + 'h ' + (m % 60) + 'm'
}
