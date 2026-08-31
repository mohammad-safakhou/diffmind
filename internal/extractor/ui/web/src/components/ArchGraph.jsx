import { useEffect, useRef, useState } from 'preact/hooks'
import cytoscape from 'cytoscape'

// ArchGraph renders the repository architecture as a real node-edge graph:
// exposures grouped by type on the left, resource clusters (datastores, caches,
// queues, services) on the right with their operations nested inside, and edges
// for each connection. Verified facts render solid; proposed facts are dashed
// amber. Click a node to select it; toggle Connect to wire an exposure to a
// dependency by clicking each in turn.
export function ArchGraph({ graph, selected, onSelect, onConnect }) {
  const containerRef = useRef(null)
  const cyRef = useRef(null)
  const [connectMode, setConnectMode] = useState(false)
  const connectSrc = useRef(null)
  const connectModeRef = useRef(false)
  const onConnectRef = useRef(onConnect)
  const onSelectRef = useRef(onSelect)
  onConnectRef.current = onConnect
  onSelectRef.current = onSelect
  connectModeRef.current = connectMode

  // Mount once.
  useEffect(() => {
    const cy = cytoscape({
      container: containerRef.current,
      style: GRAPH_STYLE,
      wheelSensitivity: 0.2,
      minZoom: 0.2,
      maxZoom: 2.5,
    })
    cyRef.current = cy
    cy.on('tap', 'node', (evt) => {
      const node = evt.target
      if (node.data('group')) return // clicking a cluster header does nothing
      const kind = node.data('kind')
      const id = node.id()
      if (connectModeRef.current) {
        handleConnectTap(kind, id)
        return
      }
      onSelectRef.current?.({ kind, id })
    })
    cy.on('tap', (evt) => { if (evt.target === cy) onSelectRef.current?.(null) })
    return () => cy.destroy()
  }, [])

  const handleConnectTap = (kind, id) => {
    if (!connectSrc.current) {
      if (kind !== 'exposure') return // must start from an exposure
      connectSrc.current = id
      cyRef.current.$id(id).addClass('connect-src')
      return
    }
    if (kind === 'dependency') {
      onConnectRef.current?.(connectSrc.current, id)
    }
    clearConnect()
  }

  const clearConnect = () => {
    if (connectSrc.current && cyRef.current) cyRef.current.$id(connectSrc.current).removeClass('connect-src')
    connectSrc.current = null
  }

  // Rebuild elements when the graph changes.
  useEffect(() => {
    const cy = cyRef.current
    if (!cy || !graph) return
    cy.elements().remove()
    cy.add(buildElements(graph))
    layout(cy)
    cy.fit(undefined, 40)
  }, [graph])

  // Reflect external selection.
  useEffect(() => {
    const cy = cyRef.current
    if (!cy) return
    cy.$('.selected').removeClass('selected')
    if (selected?.id) cy.$id(selected.id).addClass('selected')
  }, [selected])

  const toggleConnect = () => {
    clearConnect()
    setConnectMode((v) => !v)
  }

  return (
    <div class="archgraph">
      <div class="archgraph-toolbar">
        <button class={'ag-tool' + (connectMode ? ' active' : '')} onClick={toggleConnect}>
          {connectMode ? 'Connecting… (click exposure → dependency)' : '+ Connect'}
        </button>
        <button class="ag-tool" onClick={() => cyRef.current?.fit(undefined, 40)}>Fit</button>
        <Legend />
      </div>
      <div ref={containerRef} class="archgraph-canvas" />
    </div>
  )
}

function Legend() {
  return (
    <div class="ag-legend">
      <span><i class="ag-swatch exposure" /> exposure</span>
      <span><i class="ag-swatch datastore" /> datastore</span>
      <span><i class="ag-swatch cache" /> cache</span>
      <span><i class="ag-swatch message_bus" /> queue</span>
      <span><i class="ag-swatch service" /> service</span>
      <span><i class="ag-swatch proposed" /> proposed</span>
    </div>
  )
}

const KIND_CLASS = {
  datastore: 'datastore', cache: 'cache', message_bus: 'message_bus',
  service: 'service', process: 'process', resource: 'resource',
}

function buildElements(graph) {
  const els = []
  const expByType = new Map()
  for (const e of graph.exposures || []) {
    const t = e.type || 'exposure'
    if (!expByType.has(t)) expByType.set(t, [])
    expByType.get(t).push(e)
  }
  // Exposure type clusters + nodes.
  for (const [type, list] of expByType) {
    const gid = 'etype:' + type
    els.push({ data: { id: gid, label: humanize(type), group: 'exposure-type' }, classes: 'grp grp-exposure' })
    for (const e of list) {
      els.push({
        data: { id: e.id, parent: gid, label: e.name, kind: 'exposure', status: e.status || 'verified' },
        classes: 'leaf exposure ' + statusClass(e.status),
      })
    }
  }
  // Resource clusters + operation nodes.
  const depsByResource = new Map()
  for (const d of graph.dependencies || []) {
    const rid = d.resource_id || 'res_misc'
    if (!depsByResource.has(rid)) depsByResource.set(rid, [])
    depsByResource.get(rid).push(d)
  }
  const resByID = new Map((graph.resources || []).map((r) => [r.id, r]))
  for (const [rid, list] of depsByResource) {
    const r = resByID.get(rid) || { id: rid, name: rid, kind: 'resource' }
    const gid = 'res:' + rid
    const kindCls = KIND_CLASS[r.kind] || 'resource'
    els.push({ data: { id: gid, label: r.name + (r.platform ? ' · ' + r.platform : ''), group: 'resource' }, classes: 'grp grp-resource ' + kindCls })
    for (const d of list) {
      els.push({
        data: { id: d.id, parent: gid, label: d.name, kind: 'dependency', status: d.status || 'verified' },
        classes: 'leaf dependency ' + kindCls + ' ' + statusClass(d.status),
      })
    }
  }
  // Edges.
  for (const c of graph.connections || []) {
    if (!c.from_exposure_id || !c.to_dependency_id) continue
    els.push({
      data: { id: 'e:' + c.from_exposure_id + '->' + c.to_dependency_id, source: c.from_exposure_id, target: c.to_dependency_id, status: c.status || 'verified' },
      classes: 'edge ' + statusClass(c.status),
    })
  }
  return els
}

// layout positions exposure clusters in a left column and resource clusters in a
// right column, stacking children vertically. Deterministic (preset).
function layout(cy) {
  const COL_L = 0
  const COL_R = 620
  const ROW_H = 48
  const GROUP_GAP = 34
  const place = (groupSelector, x) => {
    let y = 0
    cy.nodes(groupSelector).forEach((grp) => {
      const children = grp.children()
      if (children.length === 0) return
      children.forEach((child, i) => child.position({ x, y: y + i * ROW_H }))
      y += children.length * ROW_H + GROUP_GAP
    })
  }
  place('.grp-exposure', COL_L)
  place('.grp-resource', COL_R)
}

function statusClass(status) {
  const s = (status || '').trim()
  if (s === 'proposed') return 'st-proposed'
  if (s === 'needs_review') return 'st-needs-review'
  return 'st-verified'
}

function humanize(type) {
  return (type || '').replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase())
}

// Cytoscape stylesheet. Colors mirror the dashboard's design tokens (kept inline
// here because Cytoscape renders to canvas and can't read CSS variables).
const GRAPH_STYLE = [
  {
    selector: 'node.leaf',
    style: {
      'label': 'data(label)', 'font-size': 11, 'color': '#e9eefa',
      'text-valign': 'center', 'text-halign': 'center', 'text-max-width': 180,
      'text-wrap': 'ellipsis', 'width': 188, 'height': 30,
      'shape': 'round-rectangle', 'background-color': '#1c2746',
      'border-width': 1.5, 'border-color': '#3a4d72',
      'padding': 4,
    },
  },
  { selector: 'node.exposure', style: { 'background-color': '#21304f', 'border-color': '#4f8cff' } },
  { selector: 'node.datastore', style: { 'border-color': '#4f8cff' } },
  { selector: 'node.cache', style: { 'border-color': '#06b6d4' } },
  { selector: 'node.message_bus', style: { 'border-color': '#22c55e' } },
  { selector: 'node.service', style: { 'border-color': '#a78bfa' } },
  { selector: 'node.process', style: { 'border-color': '#f59e0b' } },
  {
    selector: 'node.st-proposed',
    style: { 'border-color': '#f59e0b', 'border-style': 'dashed', 'background-color': '#2a2310' },
  },
  { selector: 'node.st-needs-review', style: { 'border-style': 'dashed' } },
  {
    selector: 'node.selected',
    style: { 'border-width': 3, 'border-color': '#e9eefa' },
  },
  {
    selector: 'node.connect-src',
    style: { 'border-width': 3, 'border-color': '#22c55e' },
  },
  {
    selector: 'node.grp',
    style: {
      'label': 'data(label)', 'font-size': 10, 'font-weight': 700,
      'color': '#8593b3', 'text-valign': 'top', 'text-halign': 'center',
      'text-transform': 'uppercase', 'text-margin-y': -4,
      'background-opacity': 0.06, 'background-color': '#4f8cff',
      'border-width': 1, 'border-color': '#233158', 'border-style': 'dashed',
      'shape': 'round-rectangle', 'padding': 14,
    },
  },
  { selector: 'node.grp-resource', style: { 'background-color': '#22c55e' } },
  {
    selector: 'edge',
    style: {
      'width': 1.6, 'line-color': '#3a4d72', 'curve-style': 'bezier',
      'target-arrow-shape': 'triangle', 'target-arrow-color': '#3a4d72',
      'arrow-scale': 0.9,
    },
  },
  {
    selector: 'edge.st-proposed',
    style: { 'line-color': '#f59e0b', 'target-arrow-color': '#f59e0b', 'line-style': 'dashed' },
  },
]
