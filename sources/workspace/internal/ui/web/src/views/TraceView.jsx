import { useEffect, useState } from 'preact/hooks'
import { getRunArchGraphFlow } from '../lib/api.js'
import { navigate } from '../lib/router.js'

export function TraceView({ pid, rid, params = {} }) {
  const [flow, setFlow] = useState(null)
  const [error, setError] = useState('')
  const service = params.service || params.service_name || ''
  const objectID = params.object_id || params.entrypoint_id || params.flow_id || ''
  const depth = params.depth || '6'

  useEffect(() => {
    let cancelled = false
    setFlow(null)
    setError('')
    getRunArchGraphFlow(pid, rid, { service, object_id: objectID, depth, max_nodes: params.max_nodes || 500, expand: params.expand || 'steps' })
      .then((data) => { if (!cancelled) setFlow(data) })
      .catch((e) => { if (!cancelled) setError(e.message) })
    return () => { cancelled = true }
  }, [pid, rid, service, objectID, depth])

  return (
    <div class="trace-page">
      <header class="run-topbar">
        <button class="btn ghost tiny" onClick={() => navigate(`/projects/${pid}/runs/${rid}`)}>← Graph</button>
        <h1 class="mono run-title">{rid}</h1>
        <div class="trace-title">
          <strong>{service || 'Trace'}</strong>
          {objectID && <code>{objectID}</code>}
        </div>
      </header>

      {error && <div class="banner error">{error}</div>}
      {!flow && !error && <div class="graph-empty muted">Loading flow…</div>}
      {flow && <FlowBody flow={flow} />}
    </div>
  )
}

function FlowBody({ flow }) {
  const services = flow.services || []
  const nodes = flow.nodes || []
  const edges = flow.edges || []
  return (
    <main class="trace-layout">
      <section class="trace-summary">
        <div>
          <div class={'trace-status trace-status-' + flow.status}>{flow.status}</div>
          <h2>{flow.entry?.service}</h2>
          {flow.entry?.object_id && <code>{flow.entry.object_id}</code>}
        </div>
        <div class="trace-stats">
          <Stat n={flow.stats?.service_count || services.length} label="services" />
          <Stat n={flow.stats?.node_count || nodes.length} label="nodes" />
          <Stat n={flow.stats?.edge_count || edges.length} label="edges" />
          <Stat n={flow.stats?.cycle_count || 0} label="cycles" />
        </div>
      </section>

      {flow.quality?.length > 0 && (
        <section class="trace-panel trace-quality-panel">
          <h3>Quality</h3>
          <ul>{flow.quality.map((q) => <li key={q}>{q}</li>)}</ul>
        </section>
      )}

      <section class="trace-grid">
        <div class="trace-panel">
          <h3>Services</h3>
          <div class="trace-list">
            {services.map((svc) => (
              <div class="trace-row service-row" key={svc.name}>
                <span class="trace-depth">d{svc.depth}</span>
                <strong>{svc.name}</strong>
                <code>{svc.entry_status}</code>
                {svc.team && <span class="muted">{svc.team}</span>}
              </div>
            ))}
          </div>
        </div>
        <div class="trace-panel">
          <h3>Nodes</h3>
          <div class="trace-list">
            {nodes.map((node) => (
              <div class="trace-row" key={node.id}>
                <span class={'trace-kind kind-' + node.kind}>{node.kind}</span>
                <strong>{node.label}</strong>
                {node.service && <code>{node.service}</code>}
              </div>
            ))}
          </div>
        </div>
      </section>

      <section class="trace-panel">
        <h3>Edges</h3>
        <div class="trace-edge-list">
          {edges.map((edge, i) => (
            <div class={'trace-edge ' + (edge.cross_service ? 'cross' : '')} key={`${edge.from}-${edge.to}-${edge.kind}-${i}`}>
              <div class="trace-edge-main">
                <code>{shortID(edge.from)}</code>
                <span>→</span>
                <code>{shortID(edge.to)}</code>
              </div>
              <div class="trace-edge-meta">
                <span>{edge.kind}</span>
                {edge.match_status && <span>{edge.match_status}</span>}
                {edge.reachability && <span>{edge.reachability}</span>}
                {edge.async && <span>async</span>}
                {edge.cross_service && <span>cross-service</span>}
                {edge.cycle && <span class="cycle">cycle</span>}
              </div>
            </div>
          ))}
        </div>
      </section>
    </main>
  )
}

function Stat({ n, label }) {
  return <div class="trace-stat"><strong>{n}</strong><span>{label}</span></div>
}

function shortID(id) {
  if (!id) return '-'
  return id.replace(/^object:/, '').replace(/^service:/, '').replace(/^resource:/, '')
}
