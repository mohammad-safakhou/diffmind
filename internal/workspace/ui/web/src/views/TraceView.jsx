import { useEffect, useState } from 'preact/hooks'
import { getRunArchGraphFlow, getRunArchGraphImpact } from '../lib/api.js'
import { navigate } from '../lib/router.js'
import { FlowRibbon } from './FlowRibbon.jsx'
import { TracePicker } from '../components/TracePicker.jsx'

export function TraceView({ pid, rid, params = {} }) {
  const [flow, setFlow] = useState(null)
  const [error, setError] = useState('')
  const [selectedNode, setSelectedNode] = useState(null)
  const impactNode = params.node || ''
  const service = impactNode || params.service || params.service_name || ''
  const objectID = params.object_id || params.entrypoint_id || params.flow_id || ''
  const depth = params.depth || '6'
  const isImpact = Boolean(impactNode)

  useEffect(() => {
    if (!service) return
    let cancelled = false
    setFlow(null)
    setError('')
    setSelectedNode(null)
    const req = isImpact
      ? getRunArchGraphImpact(pid, rid, { node: impactNode, depth, max_nodes: params.max_nodes || 500 })
      : getRunArchGraphFlow(pid, rid, { service, object_id: objectID, depth, max_nodes: params.max_nodes || 500, expand: params.expand || 'steps' })
    req
      .then((data) => { if (!cancelled) setFlow(data) })
      .catch((e) => { if (!cancelled) setError(e.message) })
    return () => { cancelled = true }
  }, [pid, rid, service, objectID, depth, impactNode])

  const openTrace = (ref, nextDepth) => {
    const qs = new URLSearchParams()
    qs.set('service', ref.service)
    if (ref.id) qs.set('object_id', ref.id)
    qs.set('depth', nextDepth || depth)
    navigate(`/projects/${encodeURIComponent(pid)}/runs/${encodeURIComponent(rid)}/trace?${qs.toString()}`)
  }
  const openImpact = (node, nextDepth) => {
    const qs = new URLSearchParams()
    qs.set('node', node)
    qs.set('depth', nextDepth || depth)
    navigate(`/projects/${encodeURIComponent(pid)}/runs/${encodeURIComponent(rid)}/trace?${qs.toString()}`)
  }

  return (
    <div class="trace-page">
      <header class="run-topbar">
        <button class="btn ghost tiny" onClick={() => navigate(`/projects/${pid}/runs/${rid}`)}>← Graph</button>
        <h1 class="mono run-title">{rid}</h1>
        <div class="trace-title">
          {isImpact && <span class="trace-mode-badge">impact</span>}
          <strong>{isImpact ? `What breaks if ${impactNode} changes?` : service || 'Trace an execution flow'}</strong>
          {!isImpact && objectID && <code>{objectID}</code>}
        </div>
        {service && (
          <div class="trace-controls">
            <label class="muted small">depth</label>
            <select value={depth} onChange={(e) => (isImpact ? openImpact(impactNode, e.target.value) : openTrace({ service, id: objectID }, e.target.value))}>
              {['2', '3', '4', '6', '8', '12'].map((d) => <option key={d} value={d}>{d}</option>)}
            </select>
            {!isImpact && <button class="btn ghost tiny" onClick={() => openImpact(service)}>Impact of {service}</button>}
            {isImpact && <button class="btn ghost tiny" onClick={() => openTrace({ service: impactNode, id: '' })}>Trace from {impactNode}</button>}
            <button class="btn ghost tiny" onClick={() => navigate(`/projects/${pid}/runs/${rid}/trace`)}>New trace</button>
          </div>
        )}
      </header>

      {!service && (
        <main class="trace-layout">
          <section class="trace-panel trace-picker-panel">
            <h3>Pick an entry point</h3>
            <p class="muted">Start from an HTTP endpoint, queue consumer, scheduled job, or webhook — the flow follows every hop across services.</p>
            <TracePicker pid={pid} rid={rid} onPick={openTrace} />
          </section>
        </main>
      )}

      {error && <div class="banner error">{error}</div>}
      {service && !flow && !error && <div class="graph-empty muted">Walking the flow…</div>}
      {service && flow && (
        <FlowBody
          flow={flow}
          selectedNode={selectedNode}
          onSelectNode={setSelectedNode}
          onOpenService={(svc) => navigate(`/projects/${pid}/runs/${rid}`)}
          onTraceFrom={(node) => node.service && openTrace({ service: node.service, id: node.details?.object_id || nodeObjectID(node) })}
          onImpact={(node) => openImpact(node.service || node.id.replace(/^resource:/, ''))}
        />
      )}
    </div>
  )
}

function nodeObjectID(node) {
  // Object node IDs are "object:<service>:<object_id>".
  const parts = (node.id || '').split(':')
  return parts.length >= 3 ? parts.slice(2).join(':') : ''
}

function FlowBody({ flow, selectedNode, onSelectNode, onOpenService, onTraceFrom, onImpact }) {
  const services = flow.services || []
  const edges = flow.edges || []
  const dataDeps = flow.data_dependencies || []
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
          <Stat n={flow.stats?.node_count || (flow.nodes || []).length} label="nodes" />
          <Stat n={flow.stats?.edge_count || edges.length} label="edges" />
          <Stat n={edges.filter((e) => e.async).length} label="async hops" />
          <Stat n={flow.stats?.cycle_count || 0} label="cycles" />
        </div>
      </section>

      {flow.quality?.length > 0 && (
        <details class="trace-panel trace-quality-panel">
          <summary>Quality notes ({flow.quality.length})</summary>
          <ul>{flow.quality.map((q) => <li key={q}>{q}</li>)}</ul>
        </details>
      )}

      <section class="trace-ribbon-wrap">
        <FlowRibbon flow={flow} onSelect={onSelectNode} />
        {selectedNode && (
          <aside class="trace-node-drawer">
            <header>
              <span class={'trace-kind kind-' + selectedNode.kind}>{selectedNode.kind}</span>
              <button class="btn ghost tiny" onClick={() => onSelectNode(null)}>✕</button>
            </header>
            <h4>{selectedNode.label || selectedNode.id}</h4>
            {selectedNode.service && <p class="muted">service: <strong>{selectedNode.service}</strong></p>}
            {selectedNode.details && Object.keys(selectedNode.details).length > 0 && (
              <dl class="trace-node-details">
                {Object.entries(selectedNode.details).slice(0, 12).map(([k, v]) => (
                  <div key={k}><dt>{k}</dt><dd>{typeof v === 'string' ? v : JSON.stringify(v)}</dd></div>
                ))}
              </dl>
            )}
            <div class="trace-node-actions">
              {selectedNode.service && <button class="btn tiny" onClick={() => onTraceFrom(selectedNode)}>Trace from here</button>}
              <button class="btn ghost tiny" onClick={() => onImpact(selectedNode)}>Impact</button>
              {selectedNode.service && <button class="btn ghost tiny" onClick={() => onOpenService(selectedNode.service)}>Open in graph</button>}
            </div>
          </aside>
        )}
      </section>

      <section class="trace-grid">
        <div class="trace-panel">
          <h3>Services on this flow</h3>
          <div class="trace-list">
            {services.map((svc) => (
              <div class="trace-row service-row" key={svc.name}>
                <span class="trace-depth">hop {svc.depth}</span>
                <strong>{svc.name}</strong>
                <code>{svc.entry_status}</code>
                {svc.team && <span class="muted">{svc.team}</span>}
              </div>
            ))}
          </div>
        </div>
        <div class="trace-panel">
          <h3>Data dependencies {dataDeps.length > 0 && <span class="muted">({dataDeps.length})</span>}</h3>
          {dataDeps.length === 0 && <p class="muted small">No field-level data dependencies extracted along this flow.</p>}
          <div class="trace-list">
            {dataDeps.slice(0, 60).map((dep, i) => (
              <div class="trace-row data-dep-row" key={i}>
                <span class="muted small">{dep.service}</span>
                <strong>{dep.from}</strong>
                <span>→</span>
                <strong>{dep.to}</strong>
                <DataDepDetail deps={dep.dependencies} />
              </div>
            ))}
          </div>
        </div>
      </section>

      <details class="trace-panel">
        <summary>All hops ({edges.length})</summary>
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
      </details>
    </main>
  )
}

function DataDepDetail({ deps }) {
  if (!deps) return null
  const items = Array.isArray(deps) ? deps : [deps]
  return (
    <span class="data-dep-fields muted small">
      {items.slice(0, 4).map((d, i) => {
        const from = d?.from?.expression || d?.from || ''
        const to = d?.to?.expression || d?.to || ''
        const text = from && to ? `${from} → ${to}` : JSON.stringify(d).slice(0, 60)
        return <code key={i}>{text}</code>
      })}
    </span>
  )
}

function Stat({ n, label }) {
  return <div class="trace-stat"><strong>{n}</strong><span>{label}</span></div>
}

function shortID(id) {
  if (!id) return '-'
  return id.replace(/^object:/, '').replace(/^service:/, '').replace(/^resource:/, '')
}
