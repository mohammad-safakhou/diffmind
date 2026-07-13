import { useEffect, useMemo, useRef, useState } from 'preact/hooks'
import * as d3 from 'd3'
import dagre from 'dagre'

// FlowRibbon renders a FlowView as a left-to-right execution ribbon: dagre
// lays out the object-level nodes, and each service's nodes get a hull band
// behind them so the cross-service structure reads as swimlanes. Async hops
// are dashed with an envelope glyph, conditional reachability is amber,
// cycles curve back in red.
const KIND_COLORS = {
  http_endpoint: { fill: '#12233f', stroke: '#4f8cff' },
  exposure: { fill: '#12233f', stroke: '#4f8cff' },
  queue_consumer: { fill: '#0f2b22', stroke: '#22c997' },
  queue: { fill: '#0f2b22', stroke: '#22c997' },
  queue_topic_stream: { fill: '#0f2b22', stroke: '#22c997' },
  database: { fill: '#2e1e0d', stroke: '#f5943a' },
  db_operation: { fill: '#2e1e0d', stroke: '#f5943a' },
  cache: { fill: '#2e1e0d', stroke: '#e0b341' },
  external: { fill: '#241a38', stroke: '#9b7cf9' },
  service: { fill: '#1a2233', stroke: '#8aa0c8' },
  scheduled_job: { fill: '#251a30', stroke: '#c77dff' },
  default: { fill: '#161e2e', stroke: '#5a6d92' },
}

function nodeColors(kind) {
  return KIND_COLORS[kind] || KIND_COLORS.default
}

function edgeClass(edge) {
  if (edge.cycle) return 'flow-edge cycle'
  if (edge.reachability === 'conditional' || edge.reachability === 'may') return 'flow-edge conditional'
  if (edge.async) return 'flow-edge async'
  if (edge.cross_service) return 'flow-edge cross'
  return 'flow-edge'
}

export function FlowRibbon({ flow, onSelect }) {
  const svgRef = useRef(null)
  const [selected, setSelected] = useState(null)

  const layout = useMemo(() => {
    const nodes = flow?.nodes || []
    const edges = flow?.edges || []
    if (!nodes.length) return null
    const g = new dagre.graphlib.Graph({ multigraph: true })
    g.setGraph({ rankdir: 'LR', nodesep: 26, ranksep: 70, marginx: 40, marginy: 40 })
    g.setDefaultEdgeLabel(() => ({}))
    for (const n of nodes) {
      const label = n.label || n.id
      const width = Math.min(300, Math.max(140, label.length * 7.2 + 36))
      g.setNode(n.id, { width, height: 46 })
    }
    edges.forEach((e, i) => {
      if (g.hasNode(e.from) && g.hasNode(e.to) && e.from !== e.to) {
        g.setEdge(e.from, e.to, { weight: e.cycle ? 0 : 1 }, `e${i}`)
      }
    })
    dagre.layout(g)
    const placed = nodes
      .filter((n) => g.hasNode(n.id))
      .map((n) => ({ ...n, ...g.node(n.id) }))
    const byID = new Map(placed.map((n) => [n.id, n]))
    const lines = edges
      .filter((e) => byID.has(e.from) && byID.has(e.to))
      .map((e, i) => ({ ...e, key: `${e.from}|${e.to}|${e.kind}|${i}`, source: byID.get(e.from), target: byID.get(e.to) }))
    // Service hulls: bounding box per service around its placed nodes.
    const hulls = []
    const byService = new Map()
    for (const n of placed) {
      if (!n.service) continue
      if (!byService.has(n.service)) byService.set(n.service, [])
      byService.get(n.service).push(n)
    }
    const depths = new Map((flow.services || []).map((s) => [s.name, s.depth]))
    for (const [service, members] of byService) {
      const minX = Math.min(...members.map((n) => n.x - n.width / 2)) - 16
      const maxX = Math.max(...members.map((n) => n.x + n.width / 2)) + 16
      const minY = Math.min(...members.map((n) => n.y - n.height / 2)) - 30
      const maxY = Math.max(...members.map((n) => n.y + n.height / 2)) + 14
      hulls.push({ service, depth: depths.get(service), x: minX, y: minY, w: maxX - minX, h: maxY - minY })
    }
    const graphMeta = g.graph()
    return { nodes: placed, edges: lines, hulls, width: graphMeta.width || 800, height: graphMeta.height || 400 }
  }, [flow])

  useEffect(() => {
    const svgEl = svgRef.current
    if (!svgEl || !layout) return
    const svg = d3.select(svgEl)
    const inner = svg.select('g.flow-zoom')
    const zoom = d3.zoom().scaleExtent([0.2, 2.5]).on('zoom', (ev) => inner.attr('transform', ev.transform))
    svg.call(zoom)
    // Fit content on first render.
    const box = svgEl.getBoundingClientRect()
    const scale = Math.min(1, (box.width - 40) / layout.width, (box.height - 40) / layout.height)
    const tx = (box.width - layout.width * scale) / 2
    const ty = (box.height - layout.height * scale) / 2
    svg.call(zoom.transform, d3.zoomIdentity.translate(tx, ty).scale(scale))
    return () => svg.on('.zoom', null)
  }, [layout])

  if (!layout) return <div class="graph-empty muted">No flow nodes to draw.</div>

  const pick = (node) => {
    setSelected(node.id)
    onSelect && onSelect(node)
  }

  return (
    <svg ref={svgRef} class="flow-ribbon-svg" role="img" aria-label="Cross-service execution flow">
      <defs>
        <marker id="flow-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
          <path d="M 0 0 L 10 5 L 0 10 z" fill="#5a6d92" />
        </marker>
        <marker id="flow-arrow-async" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
          <path d="M 0 0 L 10 5 L 0 10 z" fill="#22c997" />
        </marker>
        <marker id="flow-arrow-cycle" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
          <path d="M 0 0 L 10 5 L 0 10 z" fill="#e5484d" />
        </marker>
      </defs>
      <g class="flow-zoom">
        {layout.hulls.map((h) => (
          <g key={h.service} class="flow-hull">
            <rect x={h.x} y={h.y} width={h.w} height={h.h} rx="10" />
            <text x={h.x + 10} y={h.y + 17}>
              {h.service}
              {Number.isFinite(h.depth) ? ` · hop ${h.depth}` : ''}
            </text>
          </g>
        ))}
        {layout.edges.map((e) => {
          const midX = (e.source.x + e.target.x) / 2
          const path = e.cycle
            ? `M ${e.source.x} ${e.source.y} C ${e.source.x} ${e.source.y - 90}, ${e.target.x} ${e.target.y - 90}, ${e.target.x} ${e.target.y}`
            : `M ${e.source.x} ${e.source.y} C ${midX} ${e.source.y}, ${midX} ${e.target.y}, ${e.target.x} ${e.target.y}`
          const marker = e.cycle ? 'url(#flow-arrow-cycle)' : e.async ? 'url(#flow-arrow-async)' : 'url(#flow-arrow)'
          return (
            <g key={e.key} class={edgeClass(e)}>
              <path d={path} marker-end={marker} />
              {e.async && <text class="flow-edge-glyph" x={midX} y={(e.source.y + e.target.y) / 2 - 6}>✉</text>}
              {(e.reachability === 'conditional' || e.reachability === 'may') && (
                <text class="flow-edge-label" x={midX} y={(e.source.y + e.target.y) / 2 + 14}>
                  {e.condition?.summary || e.reachability}
                </text>
              )}
            </g>
          )
        })}
        {layout.nodes.map((n) => {
          const colors = nodeColors(n.kind)
          return (
            <g
              key={n.id}
              class={'flow-node' + (selected === n.id ? ' selected' : '')}
              transform={`translate(${n.x - n.width / 2}, ${n.y - n.height / 2})`}
              onClick={() => pick(n)}
            >
              <rect width={n.width} height={n.height} rx="8" fill={colors.fill} stroke={colors.stroke} />
              <text class="flow-node-kind" x="10" y="16" fill={colors.stroke}>{n.kind}</text>
              <text class="flow-node-label" x="10" y="33">
                {(n.label || n.id).slice(0, 40)}
              </text>
            </g>
          )
        })}
      </g>
    </svg>
  )
}
