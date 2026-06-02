import { useEffect, useRef, useState } from 'preact/hooks'
import { getRun, getRunGraph, cancelRun, runEventsURL } from '../lib/api.js'
import { navigate } from '../lib/router.js'
import { StatusBadge } from './tabs/RunsTab.jsx'

// RunView shows a single graph run: live progress events while running, then
// the rendered cross-service graph once graph.json is available.
export function RunView({ pid, rid }) {
  const [run, setRun] = useState(null)
  const [events, setEvents] = useState([])
  const [graph, setGraph] = useState(null)
  const [error, setError] = useState('')
  const esRef = useRef(null)

  const loadRun = async () => {
    try { const r = await getRun(pid, rid); setRun(r.run); return r.run }
    catch (e) { setError(e.message); return null }
  }
  const loadGraph = async () => {
    try { setGraph(await getRunGraph(pid, rid)) } catch { /* not ready */ }
  }

  useEffect(() => {
    loadRun().then((r) => { if (r && r.status === 'completed') loadGraph() })

    const es = new EventSource(runEventsURL(pid, rid))
    esRef.current = es
    const onEv = (ev) => {
      try {
        const e = JSON.parse(ev.data)
        setEvents((prev) => [...prev, e])
      } catch { /* ignore */ }
    }
    for (const t of ['run_started', 'phase_started', 'phase_completed', 'phase_failed', 'log', 'run_completed', 'run_failed', 'run_cancelled']) {
      es.addEventListener(t, onEv)
    }
    es.addEventListener('eof', () => { es.close(); loadRun().then((r) => { if (r && r.status === 'completed') loadGraph() }) })
    es.onerror = () => { /* EventSource auto-reconnects; final state comes from loadRun on eof */ }
    return () => { try { es.close() } catch {} }
  }, [pid, rid])

  const doCancel = async () => { try { await cancelRun(pid, rid); loadRun() } catch (e) { setError(e.message) } }
  const active = run && (run.status === 'running' || run.status === 'cancelling')

  return (
    <div class="page">
      <header class="topbar">
        <div class="crumbs">
          <button class="btn ghost tiny" onClick={() => navigate(`/projects/${pid}`)}>← {pid}</button>
          <h1 class="mono">{rid}</h1>
          {run && <StatusBadge status={run.status} />}
        </div>
        {active && <button class="btn ghost" onClick={doCancel}>Cancel run</button>}
      </header>

      {error && <div class="banner error">{error}</div>}
      {run && run.error && <div class="banner error">{run.error}</div>}

      <div class="content run-view">
        <section class="run-events">
          <h3>Progress</h3>
          <ul class="event-log">
            {events.length === 0 && <li class="muted">Waiting for events…</li>}
            {events.map((e, i) => (
              <li key={i} class={'ev ev-' + (e.status || e.type)}>
                <span class="ev-type">{e.stage || e.type}</span>
                {e.status && <span class="ev-status">{e.status}</span>}
                {e.message && <span class="ev-msg">{e.message}</span>}
              </li>
            ))}
          </ul>
        </section>

        <section class="run-graph">
          <h3>Graph</h3>
          {!graph && <p class="muted">{active ? 'Graph will appear when the run completes.' : 'No graph available.'}</p>}
          {graph && <GraphView graph={graph} />}
        </section>
      </div>
    </div>
  )
}

// GraphView renders the cross-service graph as a simple SVG: services on a
// circle, edges as lines, plus textual lists for edges, shared resources, and
// unresolved dependencies. Intentionally dependency-free.
function GraphView({ graph }) {
  const services = graph.services || []
  const edges = graph.edges || []
  const [sel, setSel] = useState(null)

  const W = 640, H = 420, cx = W / 2, cy = H / 2, R = Math.min(W, H) / 2 - 60
  const pos = {}
  services.forEach((s, i) => {
    const a = (2 * Math.PI * i) / Math.max(services.length, 1) - Math.PI / 2
    pos[s.name] = { x: cx + R * Math.cos(a), y: cy + R * Math.sin(a) }
  })

  return (
    <div class="graph-wrap">
      <div class="graph-stats">
        <span>{services.length} services</span>
        <span>{edges.length} edges</span>
        <span>{(graph.shared_resources || []).length} shared</span>
        <span>{(graph.unresolved || []).length} unresolved</span>
      </div>
      {services.length === 0 ? <p class="muted">No services resolved.</p> : (
        <svg viewBox={`0 0 ${W} ${H}`} class="graph-svg">
          {edges.map((e, i) => {
            const a = pos[e.from_service], b = pos[e.to_service]
            if (!a || !b) return null
            return <line key={i} x1={a.x} y1={a.y} x2={b.x} y2={b.y} class="graph-edge" />
          })}
          {services.map((s) => {
            const p = pos[s.name]
            return (
              <g key={s.name} transform={`translate(${p.x},${p.y})`} class="graph-node" onClick={() => setSel(s)}>
                <circle r="22" class={sel && sel.name === s.name ? 'sel' : ''} />
                <text class="graph-label" y="36">{s.name}</text>
              </g>
            )
          })}
        </svg>
      )}

      {sel && (
        <div class="node-detail">
          <h4>{sel.name}</h4>
          <p class="muted small">{sel.repo_path}</p>
          <p class="small">exposures: {sel.exposures_count} · dependencies: {sel.dependencies_count}</p>
          {sel.identity && sel.identity.aliases && sel.identity.aliases.length > 0 && (
            <div class="small"><strong>aliases:</strong> {sel.identity.aliases.map((a) => `${a.kind}:${a.value}`).join(', ')}</div>
          )}
          <button class="btn ghost tiny" onClick={() => setSel(null)}>close</button>
        </div>
      )}

      {edges.length > 0 && (
        <div class="edge-list">
          <h4>Edges</h4>
          <ul>{edges.map((e, i) => <li key={i}><code>{e.from_service}</code> → <code>{e.to_service}</code> <span class="muted small">({e.type}, {Math.round((e.confidence || 0) * 100)}%)</span></li>)}</ul>
        </div>
      )}
      {(graph.unresolved || []).length > 0 && (
        <div class="edge-list">
          <h4>Unresolved</h4>
          <ul>{graph.unresolved.map((u, i) => <li key={i}><code>{u.service}</code>: {u.dependency_name} → {u.target || '?'} <span class="muted small">({u.reason})</span></li>)}</ul>
        </div>
      )}
    </div>
  )
}
