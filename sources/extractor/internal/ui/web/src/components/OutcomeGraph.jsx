import { useEffect, useRef, useState } from 'preact/hooks'
import { getRunGraph } from '../lib/api.js'
import { runMeta } from '../lib/store.js'

// Edge legend items — shown at the bottom of the graph.
const EDGE_LEGEND = [
  { label: 'Always called', cls: '' },
  { label: 'Conditional (if)', cls: 'dashed' },
  { label: 'In a loop', cls: 'thick' },
  { label: 'On error only', cls: 'dotted' },
]

// ─── color palette per dependency type ──────────────────────────────────────
const TYPE_COLOR = {
  db_operation:           '#4f8cff',
  database:               '#4f8cff',
  postgres:               '#4f8cff',
  mysql:                  '#60a5fa',
  athena:                 '#f59e0b',
  redis:                  '#ef4444',
  outbound_http:          '#a78bfa',
  http:                   '#a78bfa',
  outbound_rpc:           '#fb923c',
  queue_publish:          '#22c55e',
  queue:                  '#22c55e',
  sqs:                    '#22c55e',
  sns:                    '#16a34a',
  kafka:                  '#10b981',
  cache_operation:        '#06b6d4',
  aws_sdk_client:         '#f59e0b',
  external_service:       '#ec4899',
  observability_service:  '#8b5cf6',
  http_remote_schema_loading: '#14b8a6',
  scheduled_job:          '#eab308',
}
const DEFAULT_COLOR = '#8593b3'
function typeColor(type) { return TYPE_COLOR[type] || DEFAULT_COLOR }

// Human-readable group labels
const TYPE_LABEL = {
  http_route:             'HTTP Routes',
  scheduled_job:          'Scheduled Jobs',
  webhook:                'Webhooks',
  webhook_consumer:       'Webhook Consumers',
  scheduler_configuration:'Scheduler Config',
  db_operation:           'Database',
  database:               'Database',
  postgres:               'PostgreSQL',
  athena:                 'Athena',
  redis:                  'Redis',
  outbound_http:          'Outbound HTTP',
  http:                   'HTTP Services',
  outbound_rpc:           'RPC / Service Calls',
  queue_publish:          'Queue / SNS',
  queue:                  'Queues',
  sqs:                    'SQS',
  sns:                    'SNS',
  kafka:                  'Kafka',
  cache_operation:        'Cache',
  aws_sdk_client:         'AWS SDK',
  external_service:       'External Services',
  observability_service:  'Observability',
  http_remote_schema_loading: 'Remote Schema',
}
function typeLabel(type) { return TYPE_LABEL[type] || type }
function groupKey(item, side) {
  if (side === 'dep') {
    return `${item.platform || item.type || 'unknown'}::${item.instance || item.name || 'unknown'}`
  }
  return item.platform || item.type || 'unknown'
}
function groupLabel(key) {
  const [platform, instance] = String(key).split('::')
  if (!instance) return typeLabel(platform)
  return `${typeLabel(platform)} / ${instance}`
}
function groupColor(key) { return typeColor(String(key).split('::')[0]) }

// ─── layout constants ────────────────────────────────────────────────────────
const NODE_H      = 28   // node box height
const NODE_W_EXP  = 280  // exposure node width
const NODE_W_DEP  = 260  // dependency node width
const GROUP_GAP   = 28   // gap between type groups
const NODE_GAP    = 6    // gap between nodes within a group
const GROUP_LABEL_H = 22 // height of group header

// ─── helpers ─────────────────────────────────────────────────────────────────
function groupBy(arr, key) {
  const map = new Map()
  for (const item of arr) {
    const k = item[key] || 'unknown'
    if (!map.has(k)) map.set(k, [])
    map.get(k).push(item)
  }
  return map
}

function flattenArtifact(obj) {
  if (!obj) return []
  if (Array.isArray(obj)) return obj
  return Object.values(obj).flat()
}

// Given a sorted list of groups, compute the y-position for each node.
// Returns { nodeY: Map<id, y>, totalH: number }
function computeLayout(groups) {
  const nodeY = new Map()
  let y = 0
  for (const [, nodes] of groups) {
    y += GROUP_LABEL_H + 4
    for (const node of nodes) {
      nodeY.set(node.id, y)
      y += NODE_H + NODE_GAP
    }
    y += GROUP_GAP
  }
  return { nodeY, totalH: y }
}

// Sort group keys: prefer the order that matches the pipeline / importance
const EXP_ORDER = ['http_route','webhook','webhook_consumer','scheduled_job','scheduler_configuration']
const DEP_ORDER = ['db_operation','outbound_rpc','outbound_http','queue_publish','cache_operation',
                   'aws_sdk_client','external_service','observability_service','http_remote_schema_loading']

function sortGroups(groups, order) {
  const ordered = []
  for (const key of order) {
    if (groups.has(key)) ordered.push([key, groups.get(key)])
  }
  // Append any unknown types not in the order list
  for (const [key, val] of groups) {
    if (!order.includes(key)) ordered.push([key, val])
  }
  return ordered
}

// ─── main component ──────────────────────────────────────────────────────────
export function OutcomeGraph({ onClose }) {
  const [data, setData] = useState(null)
  const [error, setError] = useState(null)
  const [loading, setLoading] = useState(true)
  const [hovered, setHovered] = useState(null)   // node id
  const [pinned, setPinned]   = useState(null)   // node id (clicked)
  const [tooltip, setTooltip] = useState(null)   // { x, y, node }
  const svgRef = useRef(null)

  const runId = runMeta.value?.id

  // Load artifacts
  useEffect(() => {
    if (!runId) { setLoading(false); setError('No active run.'); return }
    setLoading(true)
    getRunGraph(runId)
      .then(d => { setData(d); setLoading(false) })
      .catch(e => { setError(e.message); setLoading(false) })
  }, [runId])

  // Keyboard: Escape closes pin, then closes graph
  useEffect(() => {
    const h = (e) => {
      if (e.key === 'Escape') {
        if (pinned) { setPinned(null); setTooltip(null) }
        else onClose()
      }
    }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }, [pinned, onClose])

  if (loading) return <GraphShell onClose={onClose}><div class="og-loading">Loading artifacts…</div></GraphShell>
  if (error)   return <GraphShell onClose={onClose}><div class="og-error">{error}</div></GraphShell>
  if (!data)   return <GraphShell onClose={onClose}><div class="og-error">No data.</div></GraphShell>

  // ── Prepare data ──────────────────────────────────────────────────────────
  const exposures    = flattenArtifact(data.exposures)
  const dependencies = flattenArtifact(data.dependencies)
  const connections  = flattenArtifact(data.edges || data.connections)
  const unresolved   = flattenArtifact(data.unresolved)

  const expById = new Map(exposures.map(e => [e.id, e]))
  const depById = new Map(dependencies.map(d => [d.id, d]))

  // Build adjacency sets for highlight
  const expToDeps = new Map()  // expId -> Set<depId>
  const depToExps = new Map()  // depId -> Set<expId>
  for (const c of connections) {
    if (!expToDeps.has(c.from_exposure_id)) expToDeps.set(c.from_exposure_id, new Set())
    if (!depToExps.has(c.to_dependency_id)) depToExps.set(c.to_dependency_id, new Set())
    expToDeps.get(c.from_exposure_id).add(c.to_dependency_id)
    depToExps.get(c.to_dependency_id).add(c.from_exposure_id)
  }

  // ── Group + layout ────────────────────────────────────────────────────────
  const expGroups = sortGroups(groupBy(exposures.map(e => ({ ...e, _group: groupKey(e, 'exp') })), '_group'), EXP_ORDER)
  const depGroups = sortGroups(groupBy(dependencies.map(d => ({ ...d, _group: groupKey(d, 'dep') })), '_group'), DEP_ORDER)

  const { nodeY: expY, totalH: expTotalH } = computeLayout(new Map(expGroups))
  const { nodeY: depY, totalH: depTotalH } = computeLayout(new Map(depGroups))

  const totalH   = Math.max(expTotalH, depTotalH) + 60
  const PAD_TOP  = 40
  const PAD_X    = 24
  const MID_GAP  = 120  // width of the middle lane for edges
  const SVG_W    = PAD_X + NODE_W_EXP + MID_GAP + NODE_W_DEP + PAD_X

  // x-coordinates
  const EXP_X = PAD_X
  const DEP_X = PAD_X + NODE_W_EXP + MID_GAP
  const EDGE_X_LEFT  = EXP_X + NODE_W_EXP        // right edge of exp nodes
  const EDGE_X_RIGHT = DEP_X                      // left edge of dep nodes

  // ── Determine highlighted set ─────────────────────────────────────────────
  const active = pinned || hovered
  let highlightedExp = null
  let highlightedDep = null
  let highlightedConns = null

  if (active) {
    if (expById.has(active)) {
      highlightedExp = active
      highlightedDep = expToDeps.get(active) || new Set()
      highlightedConns = new Set(
        connections
          .filter(c => c.from_exposure_id === active)
          .map(c => `${c.from_exposure_id}::${c.to_dependency_id}`)
      )
    } else if (depById.has(active)) {
      highlightedDep = active
      highlightedExp = depToExps.get(active) || new Set()
      highlightedConns = new Set(
        connections
          .filter(c => c.to_dependency_id === active)
          .map(c => `${c.from_exposure_id}::${c.to_dependency_id}`)
      )
    }
  }

  const isConnHighlighted = (c) => {
    if (!highlightedConns) return true
    return highlightedConns.has(`${c.from_exposure_id}::${c.to_dependency_id}`)
  }
  const isExpHighlighted = (id) => {
    if (!active) return true
    if (highlightedExp === id) return true
    if (highlightedExp instanceof Set) return highlightedExp.has(id)
    if (highlightedDep instanceof Set) return highlightedDep.has(id) ? false : true // not connected
    return highlightedExp === id
  }
  const isDepHighlighted = (id) => {
    if (!active) return true
    if (highlightedDep === id) return true
    if (highlightedDep instanceof Set) return highlightedDep.has(id)
    return false
  }

  // Smarter: dim everything not connected to the active node
  const dimExp = (id) => {
    if (!active) return false
    if (highlightedExp === id) return false
    if (highlightedExp instanceof Set && highlightedExp.has(id)) return false
    if (highlightedDep instanceof Set && !expToDeps.get(id)?.has(
      // if active is a dep, check if this exp connects to it
      typeof highlightedDep === 'string' ? highlightedDep : null
    )) return true
    return typeof highlightedDep === 'string'
      ? !(expToDeps.get(id)?.has(highlightedDep))
      : typeof highlightedExp === 'string'
        ? id !== highlightedExp
        : false
  }
  const dimDep = (id) => {
    if (!active) return false
    if (highlightedDep === id) return false
    if (highlightedDep instanceof Set && highlightedDep.has(id)) return false
    if (typeof highlightedExp === 'string') return !(depToExps.get(id)?.has(highlightedExp))
    return typeof highlightedDep === 'string' ? id !== highlightedDep : false
  }

  const handleNodeClick = (id, e) => {
    e.stopPropagation()
    if (pinned === id) { setPinned(null); setTooltip(null) }
    else { setPinned(id) }
  }

  const handleSvgClick = () => {
    setPinned(null)
    setTooltip(null)
    setHovered(null)
  }

  // ── Build edge paths ──────────────────────────────────────────────────────
  // Deduplicate: one bezier per (expId, depId) pair regardless of how many
  // connection records exist. The color is determined by the dep type.
  const seen = new Set()
  const edges = []
  for (const c of connections) {
    const key = `${c.from_exposure_id}::${c.to_dependency_id}`
    if (seen.has(key)) continue
    seen.add(key)
    const dep = depById.get(c.to_dependency_id)
    if (!dep) continue
    const ey = expY.get(c.from_exposure_id)
    const dy = depY.get(c.to_dependency_id)
    if (ey == null || dy == null) continue
    // Derive the dominant condition kind from all paths.
    const condKinds = (c.paths || []).map(p => p.condition?.kind).filter(Boolean)
    const domCond = condKinds.find(k => k === 'loop') ||
                    condKinds.find(k => k === 'if_guard') ||
                    condKinds.find(k => k === 'catch_block') ||
                    condKinds[0] || ''
    edges.push({
      key,
      fromId: c.from_exposure_id,
      toId:   c.to_dependency_id,
      depType: dep.platform || dep.type,
      y1: PAD_TOP + ey + NODE_H / 2,
      y2: PAD_TOP + dy + NODE_H / 2,
      paths: c.paths?.length || 0,
      condKind: domCond,
    })
  }

  // ── SVG bezier path ───────────────────────────────────────────────────────
  function edgePath(y1, y2) {
    const cp = (EDGE_X_RIGHT - EDGE_X_LEFT) * 0.45
    return `M ${EDGE_X_LEFT} ${y1} C ${EDGE_X_LEFT + cp} ${y1}, ${EDGE_X_RIGHT - cp} ${y2}, ${EDGE_X_RIGHT} ${y2}`
  }

  // ── Render ────────────────────────────────────────────────────────────────
  return (
    <GraphShell onClose={onClose}>
      <div class="og-header">
        <div class="og-counts">
          <span class="og-count-chip exp">{exposures.length} exposures</span>
          <span class="og-count-chip conn">{edges.length} connections</span>
          <span class="og-count-chip dep">{dependencies.length} dependencies</span>
          {unresolved.length > 0 && (
            <span class="og-count-chip unres">{unresolved.length} unresolved</span>
          )}
        </div>
        <div class="og-legend">
          {depGroups.map(([t]) => (
            <span key={t} class="og-legend-item">
              <span class="og-legend-dot" style={{ background: groupColor(t) }} />
              {groupLabel(t)}
            </span>
          ))}
        </div>
      </div>

      <div class="og-canvas-wrap" onClick={handleSvgClick}>
        <svg
          ref={svgRef}
          class="og-svg"
          width={SVG_W}
          height={totalH + PAD_TOP + 20}
          viewBox={`0 0 ${SVG_W} ${totalH + PAD_TOP + 20}`}
        >
          {/* ── Column headers ─────────────────────────────────────────── */}
          <text x={EXP_X + NODE_W_EXP / 2} y={22} class="og-col-header" text-anchor="middle">
            EXPOSURES
          </text>
          <text x={DEP_X + NODE_W_DEP / 2} y={22} class="og-col-header" text-anchor="middle">
            DEPENDENCIES
          </text>

          {/* ── Edges (drawn first, behind nodes) ──────────────────────── */}
          <g class="og-edges">
            {edges.map(edge => {
              const highlighted = isConnHighlighted(edge)
              const color = typeColor(edge.depType)
              const opacity = !active ? 0.22 : highlighted ? 0.75 : 0.05
              const strokeW  = !active ? 1 : highlighted ? (edge.condKind === 'loop' ? 2.5 : 1.8) : 0.7
              // Dash pattern encodes the condition kind.
              const dash = edge.condKind === 'if_guard' ? '5,4' :
                           edge.condKind === 'catch_block' ? '2,4' :
                           edge.condKind === 'loop' ? 'none' : 'none'
              return (
                <path
                  key={edge.key}
                  d={edgePath(edge.y1, edge.y2)}
                  fill="none"
                  stroke={color}
                  stroke-width={strokeW}
                  stroke-dasharray={dash}
                  opacity={opacity}
                  class="og-edge"
                  onMouseEnter={() => !pinned && setHovered(null)}
                />
              )
            })}
          </g>

          {/* ── Exposure nodes ─────────────────────────────────────────── */}
          <g class="og-nodes-exp">
            {expGroups.map(([type, nodes]) => {
              const firstY = expY.get(nodes[0].id)
              return (
                <g key={type}>
                  {/* Group label */}
                  <text
                    x={EXP_X + 8}
                    y={PAD_TOP + firstY - 6}
                    class="og-group-label"
                  >
                    {typeLabel(type).toUpperCase()}
                  </text>
                  {nodes.map(node => {
                    const y = PAD_TOP + expY.get(node.id)
                    const dim = dimExp(node.id)
                    const isActive = active === node.id
                    const hasDeps = (expToDeps.get(node.id)?.size || 0) > 0
                    return (
                      <g
                        key={node.id}
                        class={'og-node' + (isActive ? ' active' : '') + (dim ? ' dim' : '')}
                        style={{ cursor: 'pointer' }}
                        onClick={e => handleNodeClick(node.id, e)}
                        onMouseEnter={() => !pinned && setHovered(node.id)}
                        onMouseLeave={() => !pinned && setHovered(null)}
                      >
                        <rect
                          x={EXP_X}
                          y={y}
                          width={NODE_W_EXP}
                          height={NODE_H}
                          rx={5}
                          class={'og-node-rect exp' + (isActive ? ' active' : '')}
                        />
                        {/* Connection dot on right edge */}
                        {hasDeps && (
                          <circle
                            cx={EXP_X + NODE_W_EXP}
                            cy={y + NODE_H / 2}
                            r={3}
                            class="og-port"
                          />
                        )}
                        <text
                          x={EXP_X + 10}
                          y={y + NODE_H / 2 + 1}
                          class="og-node-text"
                          dominant-baseline="middle"
                        >
                          {node.name.length > 38 ? node.name.slice(0, 36) + '…' : node.name}
                        </text>
                        {/* Connection count badge */}
                        {hasDeps && (
                          <text
                            x={EXP_X + NODE_W_EXP - 8}
                            y={y + NODE_H / 2 + 1}
                            class="og-node-badge"
                            dominant-baseline="middle"
                            text-anchor="end"
                          >
                            {expToDeps.get(node.id)?.size}
                          </text>
                        )}
                      </g>
                    )
                  })}
                </g>
              )
            })}
          </g>

          {/* ── Dependency nodes ────────────────────────────────────────── */}
          <g class="og-nodes-dep">
            {depGroups.map(([type, nodes]) => {
              const firstY = depY.get(nodes[0].id)
              const color = groupColor(type)
              return (
                <g key={type}>
                  {/* Group label with color stripe */}
                  <rect
                    x={DEP_X}
                    y={PAD_TOP + firstY - GROUP_LABEL_H + 4}
                    width={3}
                    height={GROUP_LABEL_H - 4}
                    fill={color}
                    rx={1}
                    opacity={0.8}
                  />
                  <text
                    x={DEP_X + 10}
                    y={PAD_TOP + firstY - 6}
                    class="og-group-label"
                    style={{ fill: color }}
                  >
                    {groupLabel(type).toUpperCase()}
                  </text>
                  {nodes.map(node => {
                    const y = PAD_TOP + depY.get(node.id)
                    const dim = dimDep(node.id)
                    const isActive = active === node.id
                    const expCount = depToExps.get(node.id)?.size || 0
                    return (
                      <g
                        key={node.id}
                        class={'og-node' + (isActive ? ' active' : '') + (dim ? ' dim' : '')}
                        style={{ cursor: 'pointer' }}
                        onClick={e => handleNodeClick(node.id, e)}
                        onMouseEnter={() => !pinned && setHovered(node.id)}
                        onMouseLeave={() => !pinned && setHovered(null)}
                      >
                        <rect
                          x={DEP_X}
                          y={y}
                          width={NODE_W_DEP}
                          height={NODE_H}
                          rx={5}
                          class={'og-node-rect dep' + (isActive ? ' active' : '')}
                          style={{ '--dep-color': color }}
                        />
                        {/* Connection dot on left edge */}
                        {expCount > 0 && (
                          <circle
                            cx={DEP_X}
                            cy={y + NODE_H / 2}
                            r={3}
                            class="og-port"
                            fill={color}
                          />
                        )}
                        <text
                          x={DEP_X + 10}
                          y={y + NODE_H / 2 + 1}
                          class="og-node-text"
                          dominant-baseline="middle"
                        >
                          {node.name.length > 35 ? node.name.slice(0, 33) + '…' : node.name}
                        </text>
                        {expCount > 0 && (
                          <text
                            x={DEP_X + NODE_W_DEP - 8}
                            y={y + NODE_H / 2 + 1}
                            class="og-node-badge"
                            dominant-baseline="middle"
                            text-anchor="end"
                          >
                            {expCount}
                          </text>
                        )}
                      </g>
                    )
                  })}
                </g>
              )
            })}
          </g>
        </svg>
      </div>

      {/* ── Pinned node detail panel ──────────────────────────────────────── */}
      {pinned && <PinnedDetail
        pinned={pinned}
        expById={expById}
        depById={depById}
        expToDeps={expToDeps}
        depToExps={depToExps}
        connections={connections}
        typeColor={typeColor}
        onClose={() => { setPinned(null) }}
      />}

      {/* ── Edge legend ────────────────────────────────────────────────────── */}
      {!pinned && (
        <div class="og-edge-legend">
          {EDGE_LEGEND.map(l => (
            <span key={l.label} class="og-edge-legend-item">
              <span class={'og-edge-legend-line ' + l.cls} />
              {l.label}
            </span>
          ))}
        </div>
      )}

      {/* ── Unresolved sidebar ────────────────────────────────────────────── */}
      {unresolved.length > 0 && !pinned && (
        <div class="og-unresolved">
          <div class="og-unresolved-header">Unresolved ({unresolved.length})</div>
          {unresolved.map((u, i) => (
            <div key={i} class="og-unresolved-item">
              <span class="og-unresolved-badge" style={{ background: typeColor(u.type) }}>
                {u.type?.replace(/_/g, ' ')}
              </span>
              <span class="og-unresolved-name">{u.name}</span>
              <span class="og-unresolved-reason">{u.reason_code?.replace(/_/g, ' ')}</span>
            </div>
          ))}
        </div>
      )}
    </GraphShell>
  )
}

// ─── Pinned node detail panel ────────────────────────────────────────────────
function PinnedDetail({ pinned, expById, depById, expToDeps, depToExps, connections, typeColor, onClose }) {
  const [tab, setTab] = useState('connections')  // 'connections' | 'sequence' | 'inputs'
  const isExp = expById.has(pinned)
  const node  = isExp ? expById.get(pinned) : depById.get(pinned)
  if (!node) return null

  const connectedConns = connections.filter(c =>
    isExp ? c.from_exposure_id === pinned : c.to_dependency_id === pinned
  )
  const connectedIds = isExp
    ? [...(expToDeps.get(pinned) || [])]
    : [...(depToExps.get(pinned) || [])]
  const map = isExp ? depById : expById

  // Extract source identity from details.
  const details = node.details || {}
  const endpointHints = [
    details.database_table && `table: ${details.database_table}`,
    details.data_query_table && `table: ${details.data_query_table}`,
    details.defaultUrl && `url: ${details.defaultUrl}`,
    details.baseUrl && `base: ${details.baseUrl}`,
    details.event_publishing_topic && `topic: ${details.event_publishing_topic}`,
    details.topic && `topic: ${details.topic}`,
    details.queue_name && `queue: ${details.queue_name}`,
  ].filter(Boolean)

  // Build sequence from all paths of exposure connections.
  const hasSequence = isExp && connectedConns.length > 0
  const inputs = node.inputs || []

  return (
    <div class="og-detail-panel">
      <div class="og-detail-header">
        <div>
          <span class="og-detail-type" style={{ color: typeColor(node.type) }}>
            {typeLabel(node.type)}
          </span>
          <div class="og-detail-name">{node.name}</div>
          {endpointHints.length > 0 && (
            <div class="og-detail-endpoint">{endpointHints[0]}</div>
          )}
        </div>
        <button class="og-detail-close" onClick={onClose}>✕</button>
      </div>

      {/* Tab bar */}
      <div class="og-detail-tabs">
        <button
          class={'og-detail-tab' + (tab === 'connections' ? ' active' : '')}
          onClick={() => setTab('connections')}
        >
          {isExp ? 'Dependencies' : 'Called by'} ({connectedIds.length})
        </button>
        {hasSequence && (
          <button
            class={'og-detail-tab' + (tab === 'sequence' ? ' active' : '')}
            onClick={() => setTab('sequence')}
          >
            Sequence
          </button>
        )}
        {inputs.length > 0 && (
          <button
            class={'og-detail-tab' + (tab === 'inputs' ? ' active' : '')}
            onClick={() => setTab('inputs')}
          >
            Inputs ({inputs.length})
          </button>
        )}
      </div>

      <div class="og-detail-body">
        {node.summary && tab === 'connections' && (
          <div class="og-detail-summary">{node.summary}</div>
        )}

        {tab === 'connections' && (
          <>
            {node.source_locations?.length > 0 && (
              <div class="og-detail-section">
                <div class="og-detail-section-title">Source</div>
                {node.source_locations.slice(0, 2).map((loc, i) => (
                  <div key={i} class="og-detail-loc">
                    <code>{loc.file?.split('/').pop()}:{loc.start_line}</code>
                  </div>
                ))}
              </div>
            )}
            <div class="og-detail-section">
              <div class="og-detail-conns">
                {connectedIds.slice(0, 15).map(id => {
                  const other = map.get(id)
                  if (!other) return null
                  const relConns = connectedConns.filter(c =>
                    isExp ? c.to_dependency_id === id : c.from_exposure_id === id
                  )
                  const cond = relConns[0]?.paths?.find(p => p.condition?.kind && p.condition.kind !== 'unconditional')?.condition
                  const pathCount = relConns.reduce((s, c) => s + (c.paths?.length || 0), 0)
                  return (
                    <div key={id} class="og-detail-conn-item">
                      <span class="og-detail-conn-dot" style={{ background: typeColor(other.type) }} />
                      <span class="og-detail-conn-name">{other.name}</span>
                      {pathCount > 0 && (
                        <span class="og-detail-conn-meta">{pathCount}p</span>
                      )}
                      {cond && (
                        <span class={'og-detail-conn-cond ' + cond.kind}>
                          {condLabel(cond.kind)}
                        </span>
                      )}
                    </div>
                  )
                })}
                {connectedIds.length > 15 && (
                  <div class="og-detail-more">+{connectedIds.length - 15} more</div>
                )}
              </div>
            </div>
          </>
        )}

        {tab === 'sequence' && hasSequence && (
          <SequencePanel connections={connectedConns} depById={depById} typeColor={typeColor} />
        )}

        {tab === 'inputs' && inputs.length > 0 && (
          <div class="og-detail-section">
            {inputs.map((inp, i) => (
              <div key={i} class="og-detail-input-item">
                <div class="og-detail-input-name">
                  {inp.name}
                  {inp.required && <span class="og-detail-input-required">required</span>}
                  <span class="og-detail-input-type">{inp.type}</span>
                </div>
                {inp.description && (
                  <div class="og-detail-input-desc">{inp.description}</div>
                )}
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}

// ─── Sequence panel ───────────────────────────────────────────────────────────

function SequencePanel({ connections, depById, typeColor }) {
  // Merge all paths across all connections into a sequence view.
  // Each connection may have multiple paths; we show the most representative
  // (shortest) path per dependency.
  const items = []
  const seenDeps = new Set()

  for (const conn of connections) {
    const dep = depById.get(conn.to_dependency_id)
    if (!dep || seenDeps.has(conn.to_dependency_id)) continue
    seenDeps.add(conn.to_dependency_id)

    // Pick the shortest path (fewest hops).
    const paths = conn.paths || []
    const shortest = paths.slice().sort((a, b) =>
      (a.steps?.length || 0) - (b.steps?.length || 0)
    )[0]

    const cond = conn.condition || {}
    const isConditional = cond.kind && cond.kind !== 'unconditional'

    items.push({ dep, conn, path: shortest, isConditional, cond })
  }

  // Sort: unconditional first, then by dep name.
  items.sort((a, b) => {
    if (a.isConditional !== b.isConditional) return a.isConditional ? 1 : -1
    return (a.dep.name || '').localeCompare(b.dep.name || '')
  })

  return (
    <div class="og-sequence">
      {items.map(({ dep, conn, path, isConditional, cond }, i) => (
        <div key={i} class={'og-seq-item' + (isConditional ? ' conditional' : '')}>
          <div class="og-seq-dep">
            <span class="og-seq-bullet" style={{ background: typeColor(dep.type) }} />
            <span class="og-seq-dep-name">{dep.name}</span>
            {isConditional && (
              <span class={'og-seq-cond-badge ' + cond.kind}>
                {condLabel(cond.kind)}
                {cond.expression && <span class="og-seq-expr"> {cond.expression.slice(0, 40)}</span>}
              </span>
            )}
          </div>
          {path?.steps && path.steps.length > 0 && (
            <div class="og-seq-steps">
              {path.steps.map((step, si) => {
                const sc = step.condition || {}
                const hasStepCond = sc.kind && sc.kind !== 'unconditional'
                const file = step.location?.file?.split('/').pop() || ''
                const line = step.location?.start_line || ''
                return (
                  <div key={si} class="og-seq-step">
                    <span class="og-seq-step-num">{si + 1}</span>
                    <span class="og-seq-step-callee">{lastIdent(step.to || '')}</span>
                    {hasStepCond && (
                      <span class={'og-seq-step-cond ' + sc.kind}>{condLabel(sc.kind)}</span>
                    )}
                    {file && (
                      <span class="og-seq-step-loc">{file}:{line}</span>
                    )}
                  </div>
                )
              })}
            </div>
          )}
        </div>
      ))}
      {items.length === 0 && (
        <div class="og-seq-empty">No sequence data available</div>
      )}
    </div>
  )
}

function lastIdent(sym) {
  const i = sym.lastIndexOf('.')
  const j = sym.lastIndexOf('#')
  const k = sym.lastIndexOf('/')
  const cut = Math.max(i, j, k)
  return cut >= 0 ? sym.slice(cut + 1) : sym
}

function condLabel(kind) {
  const labels = {
    if_guard: '⚠ if',
    loop: '⟲ loop',
    catch_block: '⚠ on error',
    finally_block: 'always',
    try_block: '↩ try',
    goroutine: '⟲ async',
    async_block: '⟲ await',
    closure: '⟲ fn',
    fan_out: '⟲ fan-out',
    batch: '⟲ batch',
    null_check: '? null',
    ternary: '? ternary',
    match_arm: '⇒ match',
    auth_gate: '🔒 auth',
    feature_flag: '⚑ flag',
  }
  return labels[kind] || kind?.replace(/_/g, ' ') || ''
}

// ─── Shell (overlay + close button) ─────────────────────────────────────────
function GraphShell({ children, onClose }) {
  return (
    <div class="og-overlay">
      <div class="og-container">
        <button class="og-close-btn" onClick={onClose} title="Close (Esc)">✕</button>
        {children}
      </div>
    </div>
  )
}
