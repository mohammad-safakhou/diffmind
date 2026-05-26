import { useEffect, useRef, useState, useCallback } from 'preact/hooks'
import { getRunArtifacts } from '../lib/api.js'
import { runMeta } from '../lib/store.js'

// ─── color palette per dependency type ──────────────────────────────────────
const TYPE_COLOR = {
  db_operation:           '#4f8cff',
  outbound_http:          '#a78bfa',
  outbound_rpc:           '#fb923c',
  queue_publish:          '#22c55e',
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
  outbound_http:          'Outbound HTTP',
  outbound_rpc:           'RPC / Service Calls',
  queue_publish:          'Queue / SNS',
  cache_operation:        'Cache',
  aws_sdk_client:         'AWS SDK',
  external_service:       'External Services',
  observability_service:  'Observability',
  http_remote_schema_loading: 'Remote Schema',
}
function typeLabel(type) { return TYPE_LABEL[type] || type }

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
    getRunArtifacts(runId)
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
  const connections  = flattenArtifact(data.connections)
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
  const expGroups = sortGroups(groupBy(exposures, 'type'), EXP_ORDER)
  const depGroups = sortGroups(groupBy(dependencies, 'type'), DEP_ORDER)

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
    edges.push({
      key,
      fromId: c.from_exposure_id,
      toId:   c.to_dependency_id,
      depType: dep.type,
      y1: PAD_TOP + ey + NODE_H / 2,
      y2: PAD_TOP + dy + NODE_H / 2,
      paths: c.paths?.length || 0,
      conditions: (c.paths || []).filter(p => p.condition?.kind).map(p => p.condition.kind),
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
          {DEP_ORDER.filter(t => depGroups.some(([k]) => k === t)).map(t => (
            <span key={t} class="og-legend-item">
              <span class="og-legend-dot" style={{ background: typeColor(t) }} />
              {typeLabel(t)}
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
              const opacity = !active ? 0.25 : highlighted ? 0.72 : 0.06
              const strokeW  = !active ? 1 : highlighted ? 1.8 : 0.7
              return (
                <path
                  key={edge.key}
                  d={edgePath(edge.y1, edge.y2)}
                  fill="none"
                  stroke={color}
                  stroke-width={strokeW}
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
              const color = typeColor(type)
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
                    {typeLabel(type).toUpperCase()}
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
  const isExp = expById.has(pinned)
  const node  = isExp ? expById.get(pinned) : depById.get(pinned)
  if (!node) return null

  const connectedConns = connections.filter(c =>
    isExp ? c.from_exposure_id === pinned : c.to_dependency_id === pinned
  )

  // Deduplicate by the other side's id
  const connectedIds = isExp
    ? [...(expToDeps.get(pinned) || [])]
    : [...(depToExps.get(pinned) || [])]

  const map = isExp ? depById : expById

  return (
    <div class="og-detail-panel">
      <div class="og-detail-header">
        <div>
          <span class="og-detail-type" style={{ color: typeColor(node.type) }}>
            {typeLabel(node.type)}
          </span>
          <div class="og-detail-name">{node.name}</div>
        </div>
        <button class="og-detail-close" onClick={onClose}>✕</button>
      </div>

      {node.summary && (
        <div class="og-detail-summary">{node.summary}</div>
      )}

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
        <div class="og-detail-section-title">
          {isExp ? 'Connected to' : 'Called by'} ({connectedIds.length})
        </div>
        <div class="og-detail-conns">
          {connectedIds.slice(0, 12).map(id => {
            const other = map.get(id)
            if (!other) return null
            const relConns = connectedConns.filter(c =>
              isExp ? c.to_dependency_id === id : c.from_exposure_id === id
            )
            const cond = relConns[0]?.paths?.find(p => p.condition?.kind)?.condition
            const pathCount = relConns.reduce((s, c) => s + (c.paths?.length || 0), 0)
            return (
              <div key={id} class="og-detail-conn-item">
                <span class="og-detail-conn-dot" style={{ background: typeColor(other.type) }} />
                <span class="og-detail-conn-name">{other.name}</span>
                {pathCount > 0 && (
                  <span class="og-detail-conn-meta">{pathCount} path{pathCount !== 1 ? 's' : ''}</span>
                )}
                {cond && (
                  <span class="og-detail-conn-cond">{cond.kind?.replace(/_/g, ' ')}</span>
                )}
              </div>
            )
          })}
          {connectedIds.length > 12 && (
            <div class="og-detail-more">+{connectedIds.length - 12} more</div>
          )}
        </div>
      </div>
    </div>
  )
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
