import { stages, runMeta } from '../lib/store.js'

// Deterministic pipeline stages in event-emission order.
const ORDER = ['ast_index', 'deterministic_discovery', 'connections', 'reconcile']
const PRETTY = {
  ast_index: 'AST Index',
  deterministic_discovery: 'Deterministic',
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
        const s = map.get(id) || { name: id, status: 'pending', summary: {} }
        const summary = formatSummary(s.summary)
        return (
          <div class={'pipeline-stage ' + s.status} key={id}>
            <div class="name">{PRETTY[id]}</div>
            <div class="count">{statusLabel(s.status)}</div>
            <div class="stage-tip">{s.tip}</div>
            <div class="stage-tokens">{summary}</div>
          </div>
        )
      })}
      <div class="pipeline-stage" style="flex: 0 0 160px; border-right: none;">
        <div class="name">Elapsed</div>
        <div class="count">{elapsed}</div>
        <div class="stage-tip">{meta?.status || 'waiting'}</div>
        <div class="stage-tokens" />
      </div>
    </div>
  )
}

function statusLabel(status) {
  if (status === 'success') return 'done'
  if (status === 'running') return 'running'
  if (status === 'failed') return 'failed'
  if (status === 'cancelled') return 'cancelled'
  if (status === 'skipped') return 'skipped'
  return 'pending'
}

function formatSummary(summary) {
  if (!summary || typeof summary !== 'object') return ''
  const pairs = Object.entries(summary)
    .filter(([k, v]) => typeof v === 'number' && Number.isFinite(v) && !k.endsWith('_ms'))
    .slice(0, 3)
  return pairs.map(([k, v]) => `${k}: ${v}`).join(' · ')
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
