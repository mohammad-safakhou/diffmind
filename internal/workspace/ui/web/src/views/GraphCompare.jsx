import { useEffect, useState } from 'preact/hooks'
import { listGraphRuns, compareGraphs } from '../lib/api.js'
import { navigate } from '../lib/router.js'
import { comparisonDefaults, comparisonKeyLabel } from '../lib/comparison.js'
import './GraphCompare.css'

export function GraphCompare({ pid, params }) {
  const [runs, setRuns] = useState([])
  const [nextRuns, setNextRuns] = useState(null)
  const [runsOffset, setRunsOffset] = useState(0)
  const [historyReload, setHistoryReload] = useState(0)
  const [runsLoading, setRunsLoading] = useState(true)
  const [runsError, setRunsError] = useState('')
  const [from, setFrom] = useState(params.from || '')
  const [to, setTo] = useState(params.to || '')
  const [page, setPage] = useState({ pair: '', offset: 0 })
  const [result, setResult] = useState(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [reload, setReload] = useState(0)
  const pair = JSON.stringify([params.from, params.to])
  const offset = page.pair === pair ? page.offset : 0

  useEffect(() => {
    let alive = true
    setRunsLoading(true); setRunsError('')
    listGraphRuns(pid, runsOffset).then((data) => {
      if (!alive) return
      setRuns((previous) => runsOffset ? [...previous, ...data.runs] : data.runs)
      setNextRuns(data.next_offset ?? null)
    }).catch((e) => { if (alive) setRunsError(e.message) })
      .finally(() => { if (alive) setRunsLoading(false) })
    return () => { alive = false }
  }, [pid, runsOffset, historyReload])

  useEffect(() => {
    const defaults = comparisonDefaults(runs, params)
    setFrom(defaults.from); setTo(defaults.to)
  }, [params.from, params.to])

  useEffect(() => {
    const defaults = comparisonDefaults(runs, params)
    setFrom((value) => value || defaults.from)
    setTo((value) => value || defaults.to)
  }, [runs])

  useEffect(() => {
    let alive = true
    setResult(null); setError(''); setLoading(false)
    if (params.from && params.to) {
      setLoading(true)
      compareGraphs(pid, params.from, params.to, offset)
        .then((data) => { if (alive) setResult(data) })
        .catch((e) => { if (alive) setError(e.message) })
        .finally(() => { if (alive) setLoading(false) })
    }
    return () => { alive = false }
  }, [pid, params.from, params.to, offset, reload])

  const options = (value) => <>
    <option value="">Select a saved graph</option>
    {value && !runs.some((run) => run.id === value) && <option value={value}>{value} (pinned)</option>}
    {runs.map((run) => <option key={run.id} value={run.id} disabled={!run.graph_available}>{run.id} · {new Date(run.started_at).toLocaleString()}{run.graph_available ? '' : ` · ${run.status} / unavailable`}</option>)}
  </>
  const openRun = (id) => navigate(`/projects/${encodeURIComponent(pid)}/runs/${encodeURIComponent(id)}`)

  return <main class="comparison-page">
    <header class="comparison-header">
      <button class="btn ghost" onClick={() => navigate(`/projects/${encodeURIComponent(pid)}`)}>← {pid}</button>
      <div><h1>Compare graph snapshots</h1><p class="muted">Saved architectural facts and evidence, not a source diff or proof of runtime behavior.</p></div>
    </header>
    <form class="comparison-controls" onSubmit={(e) => {
      e.preventDefault()
      setPage({ pair: JSON.stringify([from, to]), offset: 0 })
      if (from === params.from && to === params.to) setReload((value) => value + 1)
      navigate(`/projects/${encodeURIComponent(pid)}/compare?${new URLSearchParams({ from, to })}`)
    }}>
      <label>Before<select value={from} onChange={(e) => setFrom(e.currentTarget.value)}>{options(from)}</select></label>
      <label>After<select value={to} onChange={(e) => setTo(e.currentTarget.value)}>{options(to)}</select></label>
      <button class="btn" disabled={!from || !to}>Compare</button>
      <button type="button" class="btn ghost" disabled={!from || !to} onClick={() => { setFrom(to); setTo(from) }}>Swap</button>
    </form>
    {runsLoading && <p role="status">Loading graph history…</p>}
    {runsError && <p class="banner error" role="alert">Graph history: {runsError} <button class="btn ghost" onClick={() => setHistoryReload((value) => value + 1)}>Retry history</button></p>}
    {nextRuns !== null && <button class="btn ghost" disabled={runsLoading} onClick={() => setRunsOffset(nextRuns)}>Load older runs</button>}
    {!runsLoading && !runsError && runs.filter((run) => run.graph_available).length < 2 && !params.from && <p class="banner">Build at least two completed graphs to compare changes. You can also compare a snapshot with itself.</p>}
    {loading && <p role="status">Comparing saved snapshots…</p>}
    {error && <p class="banner error" role="alert">Comparison failed: {error}</p>}
    {result && <section aria-label="Comparison result" class="comparison-result">
      <div class="comparison-summary">
        <h2>{result.total} changed facts</h2>
        <span>{result.counts.added || 0} added · {result.counts.removed || 0} removed · {result.counts.modified || 0} modified</span>
        <div class="comparison-links">
          <button class="btn ghost tiny" onClick={() => openRun(result.from.id)}>Before: {result.from.id}</button>
          <span>→</span><button class="btn ghost tiny" onClick={() => openRun(result.to.id)}>After: {result.to.id}</button>
        </div>
        <p class="muted">Repository artifacts changed: {result.repository_artifacts_changed?.join(', ') || 'none recorded'}. Pack digests: {result.from.pack_set_digest || 'not recorded'} → {result.to.pack_set_digest || 'not recorded'}.</p>
        <ul>{result.notes.map((note) => <li key={note}>{note}</li>)}</ul>
      </div>
      {result.total === 0 && <p class="banner">No architectural fact changes between these snapshots.</p>}
      {result.changes.map((change) => <details class={`comparison-change change-${change.change}`} key={`${change.kind}:${change.key}`}>
        <summary><span class="comparison-kind">{change.change} · {change.kind}</span> {comparisonKeyLabel(change.key)}</summary>
        {change.fields?.length > 0 && <p>Changed fields: {change.fields.join(', ')}</p>}
        <div class="comparison-evidence">
          <div><h3>Before</h3><pre>{change.before == null ? 'Not present' : JSON.stringify(change.before, null, 2)}</pre></div>
          <div><h3>After</h3><pre>{change.after == null ? 'Not present' : JSON.stringify(change.after, null, 2)}</pre></div>
        </div>
      </details>)}
      {result.total > 0 && <nav class="comparison-pagination" aria-label="Change pages">
        <button class="btn ghost" disabled={offset === 0} onClick={() => setPage({ pair, offset: Math.max(0, offset - 50) })}>Previous</button>
        <span>{offset + 1}–{offset + result.changes.length} of {result.total}</span>
        <button class="btn ghost" disabled={result.next_offset == null} onClick={() => setPage({ pair, offset: result.next_offset })}>Next</button>
      </nav>}
    </section>}
  </main>
}
