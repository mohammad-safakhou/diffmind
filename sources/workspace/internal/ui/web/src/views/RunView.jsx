import { useEffect, useRef, useState } from 'preact/hooks'
import { getRun, getRunArchGraph, cancelRun, runEventsURL } from '../lib/api.js'
import { navigate } from '../lib/router.js'
import { StatusBadge } from './tabs/RunsTab.jsx'
import { GraphCanvas } from './GraphCanvas.jsx'

// RunView shows a single graph run full-screen: the architecture graph fills the
// viewport, live progress events live in a collapsible drawer, and clicking a
// node or edge opens a details panel.
export function RunView({ pid, rid }) {
  const [run, setRun] = useState(null)
  const [events, setEvents] = useState([])
  const [graph, setGraph] = useState(null)
  const [error, setError] = useState('')
  const [showProgress, setShowProgress] = useState(false)
  const [sel, setSel] = useState(null)
  const esRef = useRef(null)

  const loadRun = async () => {
    try { const r = await getRun(pid, rid); setRun(r.run); return r.run }
    catch (e) { setError(e.message); return null }
  }
  const loadGraph = async () => {
    try { setGraph(await getRunArchGraph(pid, rid)) } catch { /* not ready */ }
  }

  useEffect(() => {
    loadRun().then((r) => { if (r && r.status === 'completed') loadGraph() })

    const es = new EventSource(runEventsURL(pid, rid))
    esRef.current = es
    const onEv = (ev) => {
      try { const e = JSON.parse(ev.data); setEvents((prev) => [...prev, e]) } catch { /* ignore */ }
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
    <div class="run-full">
      <header class="run-topbar">
        <button class="btn ghost tiny" onClick={() => navigate(`/projects/${pid}`)}>← {pid}</button>
        <h1 class="mono run-title">{rid}</h1>
        {run && <StatusBadge status={run.status} />}
        {graph && <GraphStats graph={graph} />}
        <div class="run-topbar-actions">
          {active && <button class="btn ghost tiny" onClick={doCancel}>Cancel</button>}
          <button class="btn ghost tiny" onClick={() => setShowProgress((v) => !v)}>
            {showProgress ? 'Hide' : 'Progress'} ({events.length})
          </button>
        </div>
      </header>

      {error && <div class="banner error">{error}</div>}
      {run && run.error && <div class="banner error">{run.error}</div>}
      <GraphQualityBanner quality={run?.graph_quality} />

      <div class="run-stage">
        {!graph && <div class="graph-empty muted">{active ? 'Graph will appear when the run completes…' : 'No graph available.'}</div>}
        {graph && <GraphCanvas graph={graph} onSelect={setSel} />}
        {graph && <Legend />}

        {showProgress && (
          <aside class="drawer drawer-left">
            <div class="drawer-head"><h3>Progress</h3><button class="btn ghost tiny" onClick={() => setShowProgress(false)}>×</button></div>
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
          </aside>
        )}

        {sel && (
          <aside class="drawer drawer-right">
            <div class="drawer-head"><h3>{detailTitle(sel)}</h3><button class="btn ghost tiny" onClick={() => setSel(null)}>×</button></div>
            <DetailBody sel={sel} />
          </aside>
        )}
      </div>
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

function GraphStats({ graph }) {
  const stat = (n, l, c) => <div class="gstat"><div class="gstat-n" style={c ? `color:${c}` : ''}>{n}</div><div class="gstat-l">{l}</div></div>
  return (
    <div class="gstats">
      {stat((graph.services || []).length, 'services')}
      {stat((graph.external_nodes || []).length, 'external', '#9b7cf9')}
      {stat((graph.queue_nodes || []).length, 'queues', '#22c997')}
      {stat((graph.database_nodes || []).length, 'databases', '#f5943a')}
      {stat((graph.edges || []).length, 'edges', '#3b9eff')}
    </div>
  )
}

function Legend() {
  return (
    <div class="legend-box">
      <div class="legend-title">Legend</div>
      <div class="legend-row"><span class="legend-swatch" style="background:#0d1424;border:1.5px solid #4f8cff;border-radius:4px"></span>Service island</div>
      <div class="legend-row"><span class="legend-swatch" style="background:#e9f7f5;border:1.5px solid #1d8b83;border-radius:8px"></span>Exposure group</div>
      <div class="legend-row"><span class="legend-swatch" style="background:#fff2e3;border:1.5px solid #b8671e;border-radius:8px"></span>Dependency group</div>
      <div class="legend-row"><span class="legend-swatch" style="background:#eef3ff;border:1.5px solid #5372c8;border-radius:8px"></span>Objective metadata</div>
      <div class="legend-row"><span class="legend-swatch" style="background:#f5f7fa;border:1.5px solid #8c98a4;border-radius:8px"></span>External/resource</div>
      <div class="legend-row" style="margin-top:4px"><span class="legend-line" style="background:#3b9eff"></span>HTTP</div>
      <div class="legend-row"><span class="legend-line" style="background:#22c997"></span>Queue</div>
      <div class="legend-row"><span class="legend-line" style="background:#f5943a"></span>Database</div>
      <div class="legend-row"><span class="legend-line" style="background:#ef5455"></span>Cache</div>
    </div>
  )
}

function detailTitle(sel) {
  const d = sel.data || {}
  if (sel.kind === 'edge') return `${d.from} → ${d.to}`
  return d.name || sel.id || sel.kind
}

function DetailBody({ sel }) {
  const d = sel.data || {}
  if (sel.kind === 'service') return <ServiceDetail s={d} />
  if (sel.kind === 'edge') return <EdgeDetail e={d} />
  if (sel.kind === 'group' || sel.kind === 'fact') return <GroupedFactDetail d={d} />
  if (sel.kind === 'queue') return <ResourceDetail d={d} rows={[['Name', d.name], ['Kind', d.kind], ['FIFO', d.fifo ? 'yes' : 'no']]} />
  if (sel.kind === 'db') return <ResourceDetail d={d} rows={[['Name', d.name], ['Kind', d.kind], ['Host', d.host || '—']]} />
  if (sel.kind === 'scheduler') return <KV rows={[['Job', d.name], ['Service', d.service], ['Schedule', d.schedule || '—'], ['Profile', d.profile || '—']]} />
  return <KV rows={[['Name', d.name], ['Kind', d.kind || 'external']]} />
}

function ServiceDetail({ s }) {
  const list = (title, items, key = 'name') => {
    const arr = items || []
    if (!arr.length) return null
    return (
      <div class="detail-sec">
        <h4>{title} <span class="muted">({arr.length})</span></h4>
        <ul class="detail-list">{arr.map((it, i) => <li key={i}>{typeof it === 'string' ? it : (it[key] || it.summary || '—')}</li>)}</ul>
      </div>
    )
  }
  return (
    <div>
      <KV rows={[['Service', s.name]]} />
      {list('HTTP routes', s.http_routes)}
      {list('Queue consumers', s.queue_consumers)}
      {list('Scheduled jobs', s.scheduled_jobs)}
      {list('Webhooks', s.webhooks)}
      {list('CLI commands', s.cli_commands)}
      {list('Databases', s.databases)}
      {list('Dependencies', s.dependencies)}
      {s.connections && s.connections.length > 0 && (
        <div class="detail-sec">
          <h4>Connections <span class="muted">({s.connections.length})</span></h4>
          <ul class="detail-list">{s.connections.map((c, i) => <li key={i}><code>{c.from_name}</code> → <code>{c.to_name}</code> {c.summary && <span class="muted small">{c.summary}</span>}</li>)}</ul>
        </div>
      )}
    </div>
  )
}

function EdgeDetail({ e }) {
  return (
    <div>
      <KV rows={[['From', e.from], ['To', e.to], ['Type', e.type], ['Label', e.label || '—']]} />
      {(e.details || []).filter((d) => d && (d.name || d.summary)).length > 0 && (
        <div class="detail-sec">
          <h4>Details</h4>
          <ul class="detail-list">{(e.details || []).filter((d) => d && (d.name || d.summary)).map((d, i) => <li key={i}><div class="name">{d.name}</div>{d.summary && <div class="muted small">{d.summary}</div>}</li>)}</ul>
        </div>
      )}
    </div>
  )
}

function ResourceDetail({ d, rows }) {
  const shared = d.shared || null
  return (
    <div>
      <KV rows={shared ? [...rows, ['Shared by', `${shared.serviceCount} services`], ['Source', d.inferred ? 'inferred from extracted facts' : 'explicit edge']] : rows} />
      {shared && shared.services && shared.services.length > 0 && (
        <div class="detail-sec">
          <h4>Services</h4>
          <ul class="detail-list">{shared.services.map((s) => <li key={s}><code>{s}</code></li>)}</ul>
        </div>
      )}
      {d.facts && d.facts.length > 0 && (
        <div class="detail-sec">
          <h4>Evidence</h4>
          <ul class="detail-list">
            {d.facts.slice(0, 40).map((f, i) => (
              <li key={i}><code>{f.service}</code><div class="name">{f.dep?.name || '—'}</div>{f.dep?.summary && <div class="muted small">{f.dep.summary}</div>}</li>
            ))}
          </ul>
        </div>
      )}
    </div>
  )
}

function GroupedFactDetail({ d }) {
  const items = d.items || []
  return (
    <div>
      <KV rows={[['Name', d.name], ['Group', d.kind], ['Extracted instances', d.count || items.length || 0]]} />
      {items.length > 0 && (
        <div class="detail-sec">
          <h4>Instances</h4>
          <ul class="detail-list">
            {items.map((item, i) => {
              const raw = item.items ? item.items[0] : item
              return (
                <li key={i}>
                  <div class="name">{raw.name || item.label || '—'}</div>
                  {(raw.summary || item.sublabel) && <div class="muted small">{raw.summary || item.sublabel}</div>}
                </li>
              )
            })}
          </ul>
        </div>
      )}
    </div>
  )
}

function KV({ rows }) {
  return (
    <div class="detail-sec">
      {rows.map(([k, v], i) => <div class="kv-row" key={i}><span class="kv-k">{k}</span><span class="kv-v">{v}</span></div>)}
    </div>
  )
}
