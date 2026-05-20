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
            <div class="stage-tip">{s.tip}</div>
            {/*
              When the stage has batches (currently only detail),
              show a second line "X/B batches" so the user sees the
              LLM-call axis alongside the entity axis. The detail
              stage batches related entities so far fewer LLM calls
              are needed than the entity count would suggest.
            */}
            {s.batchesTotal > 0 && (
              <div class="stage-batches" title={'detail entities are processed in batches of up to 12 related entities per LLM call'}>
                {(s.batchesDone || 0)}/{s.batchesTotal} batches
              </div>
            )}
            <div class="stage-tokens" title={s.tokens ? tokensTooltip(s.tokens) : 'token stats appear when the stage completes'}>
              {s.tokens ? compactTokens(s.tokens) : ''}
            </div>
          </div>
        )
      })}
      <div class="pipeline-stage" style="flex: 0 0 160px; border-right: none;">
        <div class="name">Elapsed</div>
        <div class="count">{elapsed}</div>
        <div class="stage-tip">{meta?.status || 'waiting'}</div>
        <div class="stage-tokens" title={meta?.tokens ? tokensTooltip(meta.tokens.total || meta.tokens) : ''}>
          {meta?.tokensTotal ? compactTokens({ total: meta.tokensTotal, cost: meta.tokensCost }) : ''}
        </div>
      </div>
    </div>
  )
}

// compactTokens renders a 2-line cost summary for a stage. The
// first line is the total token count abbreviated to k/M; the
// second is the dollar cost when the provider reports it.
function compactTokens(t) {
  if (!t) return ''
  const total = t.total ?? 0
  const cost = t.cost ?? 0
  const parts = []
  if (total) parts.push(humanTokens(total))
  if (cost) parts.push('$' + cost.toFixed(4))
  return parts.join(' · ')
}

function humanTokens(n) {
  if (!Number.isFinite(n) || n <= 0) return '0'
  if (n < 1000) return String(n)
  if (n < 1_000_000) return (n / 1000).toFixed(n < 10_000 ? 1 : 0) + 'k'
  return (n / 1_000_000).toFixed(2) + 'M'
}

function tokensTooltip(t) {
  if (!t) return ''
  return [
    'input ' + (t.input ?? 0),
    'output ' + (t.output ?? 0),
    'reasoning ' + (t.reasoning ?? 0),
    'cache_read ' + (t.cache_read ?? 0),
    'cache_write ' + (t.cache_write ?? 0),
    'cost $' + (t.cost ?? 0).toFixed(6),
  ].join('  ')
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
