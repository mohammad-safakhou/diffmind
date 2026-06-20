import { useEffect, useRef } from 'preact/hooks'
import * as d3 from 'd3'
import dagre from 'dagre'

const GROUP_COLORS = {
  exposure: { fill: '#0e2b28', stroke: '#22c997', inner: '#45d6cb', port: '#22c997' },
  dependency: { fill: '#301f11', stroke: '#f5943a', inner: '#d9954f', port: '#f5943a' },
  objective: { fill: '#131d38', stroke: '#7291ff', inner: '#8aa0e8', port: '#7291ff' },
}

const EDGE_COLORS = {
  http: '#3b9eff',
  queue_publish: '#22c997',
  queue_consume: '#22c997',
  database: '#f5943a',
  cache: '#ef5455',
  scheduler: '#f0c040',
}

const NODE_KINDS = {
  external: { fill: '#16141e', stroke: '#9b7cf9', text: '#d9cdfd' },
  queue: { fill: '#0e1a16', stroke: '#22c997', text: '#b9f5df' },
  db: { fill: '#1a1510', stroke: '#f5943a', text: '#ffe0bd' },
  scheduler: { fill: '#151118', stroke: '#f0c040', text: '#f7e7a6' },
}

const MIN_GROUP_W = 300
const GROUP_GAP = 26
const TOP_GROUP_GAP = 24
const COLUMN_GAP = 64
const HULL_MIN_W = 390
const HULL_H = 156
const CHIP_H = 23
const CHIP_ROW_GAP = 7

function cleanLabel(s) {
  if (!s) return ''
  return String(s).replace(/\$\{[^}]+\}/g, '').replace(/^https?:\/\//, '').trim()
}

function shortLabel(s, max = 22) {
  const str = cleanLabel(s) || 'unknown'
  return str.length > max ? str.slice(0, max - 1) + '…' : str
}

function normalizeKey(s) {
  return cleanLabel(s).toLowerCase().replace(/[^a-z0-9]+/g, ' ').trim()
}

function first(...vals) {
  for (const v of vals) {
    if (v !== undefined && v !== null && String(v).trim() !== '') return String(v).trim()
  }
  return ''
}

function hostFromURL(raw) {
  const value = cleanLabel(raw)
  if (!value) return ''
  try {
    return new URL(value.startsWith('http') ? value : `https://${value}`).host || value
  } catch {
    return value.split('/')[0]
  }
}

function detailsOf(item) {
  return (item && item.details) || {}
}

function itemName(item) {
  return first(item?.name, item?.summary, 'instance')
}

function groupItems(items, getKey, build) {
  const buckets = new Map()
  ;(items || []).forEach((item) => {
    const d = detailsOf(item)
    const keyRaw = getKey(item, d) || itemName(item)
    const key = normalizeKey(keyRaw) || normalizeKey(itemName(item)) || `item-${buckets.size}`
    if (!buckets.has(key)) buckets.set(key, { key, label: cleanLabel(keyRaw) || itemName(item), items: [], matchKeys: new Set() })
    const bucket = buckets.get(key)
    bucket.items.push(item)
    bucket.matchKeys.add(normalizeKey(keyRaw))
    bucket.matchKeys.add(normalizeKey(itemName(item)))
    for (const value of Object.values(d)) {
      if (typeof value === 'string') bucket.matchKeys.add(normalizeKey(value))
    }
  })
  return Array.from(buckets.values()).map((bucket) => {
    const derived = build ? build(bucket) : {}
    return {
      ...bucket,
      matchKeys: Array.from(bucket.matchKeys).filter(Boolean),
      sublabel: bucket.items.length > 1 ? `${bucket.items.length} instances` : derived.sublabel,
      ...derived,
    }
  })
}

function classifyDependency(dep) {
  const d = detailsOf(dep)
  const hay = `${dep?.name || ''} ${dep?.summary || ''} ${Object.keys(d).join(' ')} ${Object.values(d).join(' ')}`.toLowerCase()
  if (hay.includes('cache') || hay.includes('redis') || hay.includes('key_pattern')) return 'cache_operations'
  if (hay.includes('database') || hay.includes('table') || hay.includes('entity') || hay.includes('postgres') || hay.includes('dynamo') || hay.includes('sql')) return 'db_operations'
  if (hay.includes('queue') || hay.includes('topic') || hay.includes('kafka') || hay.includes('sqs') || hay.includes('sns') || hay.includes('publish')) return 'event_outbound'
  if (hay.includes('http') || hay.includes('url') || hay.includes('endpoint') || hay.includes('service')) return 'http_outbound'
  return 'other_dependencies'
}

function operationLabel(item) {
  const d = detailsOf(item)
  const ops = d.operations
  if (Array.isArray(ops) && ops.length) return ops.join(', ')
  return first(d.operation, item?.operation, item?.operation_kind, item?.summary)
}

function sourceBadge(raw, fallback = '') {
  const value = cleanLabel(raw || fallback).toLowerCase()
  if (value.includes('dynamo')) return 'DDB'
  if (value.includes('postgres') || value.includes('pgsql')) return 'PG'
  if (value.includes('redis')) return 'REDIS'
  if (value.includes('elastic') || value.includes('opensearch')) return 'ES'
  if (value.includes('athena')) return 'ATHENA'
  if (value.includes('mongo')) return 'MONGO'
  if (value.includes('kafka')) return 'KAFKA'
  if (value.includes('sqs')) return 'SQS'
  if (value.includes('sns')) return 'SNS'
  if (value.includes('kinesis')) return 'KINESIS'
  if (value.includes('http')) return 'HTTP'
  return cleanLabel(raw || fallback).slice(0, 10).toUpperCase()
}

function itemBadge(item, fallback = '') {
  const d = detailsOf(item)
  return sourceBadge(first(d.database_type, d.cache_type, d.platform, d.kind, d.type, d.source, d.method, item?.operation_kind), fallback)
}

function measureGroup(group) {
  const rows = Math.max(1, Math.ceil(group.items.length / 2))
  const titleW = Math.max(group.title.length, group.subtitle.length) * 8 + 72
  const longestItem = group.items.reduce((max, item) => Math.max(max, cleanLabel(item.label).length + cleanLabel(item.badge).length), 0)
  const chipW = Math.max(122, Math.min(210, longestItem * 7 + 44))
  return {
    ...group,
    width: Math.max(MIN_GROUP_W, titleW, chipW * 2 + 48),
    height: 76 + rows * (CHIP_H + CHIP_ROW_GAP) + 12,
    chipW,
  }
}

function measureGroups(groups) {
  return groups.map(measureGroup)
}

function stackHeight(groups) {
  if (!groups.length) return 0
  return groups.reduce((sum, group) => sum + group.height, 0) + (groups.length - 1) * GROUP_GAP
}

function maxGroupWidth(groups) {
  return groups.reduce((max, group) => Math.max(max, group.width), 0)
}

function sumGroupWidth(groups) {
  if (!groups.length) return 0
  return groups.reduce((sum, group) => sum + group.width, 0) + (groups.length - 1) * TOP_GROUP_GAP
}

function layoutServiceModel(model) {
  const leftW = maxGroupWidth(model.left)
  const rightW = maxGroupWidth(model.right)
  const topW = sumGroupWidth(model.objectives)
  const topH = model.objectives.reduce((max, group) => Math.max(max, group.height), 0)
  const hullW = Math.max(HULL_MIN_W, Math.min(660, model.service.name.length * 18 + 150))
  const leftStackH = stackHeight(model.left)
  const rightStackH = stackHeight(model.right)
  const bodyH = Math.max(leftStackH, rightStackH, HULL_H)
  const topBlockH = model.objectives.length ? topH + 78 : 0
  const bodyW = leftW + hullW + rightW + COLUMN_GAP * 2

  model.hull = {
    x: 0,
    y: -((topBlockH + bodyH + 96) / 2) + topBlockH + bodyH / 2 + 42,
    width: hullW,
    height: HULL_H,
  }
  model.width = Math.max(bodyW, topW) + 96
  model.height = topBlockH + bodyH + 96

  const bodyCenterY = model.hull.y
  const leftX = -hullW / 2 - COLUMN_GAP - leftW / 2
  const rightX = hullW / 2 + COLUMN_GAP + rightW / 2
  let y = bodyCenterY - leftStackH / 2
  model.left.forEach((group) => {
    group.x = leftX + (leftW - group.width) / 2
    group.y = y + group.height / 2
    y += group.height + GROUP_GAP
  })
  y = bodyCenterY - rightStackH / 2
  model.right.forEach((group) => {
    group.x = rightX - (rightW - group.width) / 2
    group.y = y + group.height / 2
    y += group.height + GROUP_GAP
  })

  const topStartX = -topW / 2
  let x = topStartX
  const topY = -model.height / 2 + 42 + topH / 2
  model.objectives.forEach((group) => {
    group.x = x + group.width / 2
    group.y = topY
    x += group.width + TOP_GROUP_GAP
  })

  return model
}

function buildServiceModel(service, graphEdges) {
  const deps = service.dependencies || []
  const depByClass = deps.reduce((acc, dep) => {
    const cls = classifyDependency(dep)
    ;(acc[cls] ||= []).push(dep)
    return acc
  }, {})

  const left = [
    {
      key: 'http_inbound',
      title: 'http_inbound',
      subtitle: 'routes grouped by endpoint',
      items: groupItems(service.http_routes, (it, d) => first(d.route, d.path, d.endpoint, it.name), (b) => {
        const d = detailsOf(b.items[0])
        return { badge: sourceBadge(first(d.method, 'HTTP')), label: shortLabel(b.label, 28), sublabel: shortLabel(first(d.handler, d.controller, b.items[0]?.summary), 22) }
      }),
    },
    {
      key: 'event_inbound',
      title: 'event_inbound',
      subtitle: 'consumers grouped by queue/topic',
      items: groupItems(service.queue_consumers, (it, d) => first(d.queue, d.queue_name, d.destination, d.stream_arn, d.source, it.name), (b) => ({ badge: itemBadge(b.items[0], 'QUEUE'), label: shortLabel(b.label, 28), sublabel: shortLabel(first(detailsOf(b.items[0]).kind, detailsOf(b.items[0]).type, b.items[0]?.summary), 22) })),
    },
    {
      key: 'scheduled_jobs',
      title: 'scheduled_jobs',
      subtitle: 'time-based entry points',
      items: groupItems(service.scheduled_jobs, (it, d) => first(d.k8s_cronjob_name, d.schedule, d.cron, it.name), (b) => ({ badge: 'CRON', label: shortLabel(b.label, 28), sublabel: shortLabel(first(detailsOf(b.items[0]).schedule, detailsOf(b.items[0]).cron, b.items[0]?.summary), 22) })),
    },
    {
      key: 'webhooks',
      title: 'webhooks',
      subtitle: 'external callbacks',
      items: groupItems(service.webhooks, (it, d) => first(d.provider, d.source, d.path, it.name), (b) => ({ badge: 'HOOK', label: shortLabel(b.label, 28), sublabel: shortLabel(b.items[0]?.summary, 22) })),
    },
    {
      key: 'cli_commands',
      title: 'cli_commands',
      subtitle: 'manual/runtime commands',
      items: groupItems(service.cli_commands, (it, d) => first(d.command, it.name), (b) => ({ badge: 'CLI', label: shortLabel(b.label, 28), sublabel: shortLabel(b.items[0]?.summary, 22) })),
    },
  ].filter((g) => g.items.length)

  const right = [
    {
      key: 'http_outbound',
      title: 'http_outbound',
      subtitle: 'targets grouped by host/service',
      items: groupItems(depByClass.http_outbound, (it, d) => first(d.target_service, hostFromURL(d.target_url), hostFromURL(d.url), d.host, it.name), (b) => ({ badge: sourceBadge(first(detailsOf(b.items[0]).method, 'HTTP')), label: shortLabel(b.label, 28), sublabel: shortLabel(first(detailsOf(b.items[0]).endpoint, b.items[0]?.summary), 22) })),
    },
    {
      key: 'db_operations',
      title: 'db_operations',
      subtitle: 'instances grouped by resource + op',
      items: groupItems(depByClass.db_operations, (it, d) => `${first(d.database_name, d.resource_name, d.table_or_entity, d.table, d.entity, it.name)} ${operationLabel(it)}`, (b) => ({ badge: itemBadge(b.items[0], 'DB'), label: shortLabel(b.label, 28), sublabel: shortLabel(operationLabel(b.items[0]), 22) })),
    },
    {
      key: 'cache_operations',
      title: 'cache_operations',
      subtitle: 'cache instances and key spaces',
      items: groupItems(depByClass.cache_operations, (it, d) => first(d.cache_name, d.resource_name, d.key_pattern, it.name), (b) => ({ badge: itemBadge(b.items[0], 'CACHE'), label: shortLabel(b.label, 28), sublabel: shortLabel(operationLabel(b.items[0]), 22) })),
    },
    {
      key: 'event_outbound',
      title: 'event_outbound',
      subtitle: 'publish targets grouped by queue/topic',
      items: groupItems(depByClass.event_outbound, (it, d) => first(d.queue, d.queue_name, d.destination, d.topic, it.name), (b) => ({ badge: itemBadge(b.items[0], 'QUEUE'), label: shortLabel(b.label, 28), sublabel: shortLabel(first(detailsOf(b.items[0]).kind, detailsOf(b.items[0]).type, b.items[0]?.summary), 22) })),
    },
    {
      key: 'other_dependencies',
      title: 'dependencies',
      subtitle: 'uncategorized downstream facts',
      items: groupItems(depByClass.other_dependencies, (it) => it.name, (b) => ({ badge: 'DEP', label: shortLabel(b.label, 28), sublabel: shortLabel(b.items[0]?.summary, 22) })),
    },
  ].filter((g) => g.items.length)

  const connections = service.connections || []
  const objectives = [
    {
      key: 'runtime',
      title: 'runtime',
      subtitle: service.known ? 'known service' : 'external/unknown',
      items: [{ key: 'service', badge: 'SVC', label: service.known ? 'known' : 'unknown', sublabel: 'identity', items: [service], matchKeys: [normalizeKey(service.name)] }],
    },
    connections.length ? {
      key: 'flows',
      title: 'flows',
      subtitle: `${connections.length} internal paths`,
      items: groupItems(connections, (it) => first(it.from_name, it.to_name, it.summary), (b) => ({ badge: 'FLOW', label: shortLabel(b.label, 28), sublabel: shortLabel(b.items[0]?.summary, 22) })),
    } : null,
  ].filter(Boolean)

  return layoutServiceModel({
    service,
    left: measureGroups(left),
    right: measureGroups(right),
    objectives: measureGroups(objectives),
    width: 0,
    height: 0,
    edges: graphEdges.filter((e) => e.from === service.name || e.to === service.name),
  })
}

function groupForEdge(edge, role) {
  if (role === 'source') {
    if (edge.type === 'http') return 'http_outbound'
    if (edge.type === 'database') return 'db_operations'
    if (edge.type === 'cache') return 'cache_operations'
    if (edge.type === 'queue_publish') return 'event_outbound'
    return 'other_dependencies'
  }
  if (edge.type === 'http') return 'http_inbound'
  if (edge.type === 'queue_consume') return 'event_inbound'
  if (edge.type === 'scheduler') return 'scheduled_jobs'
  return 'http_inbound'
}

function edgeMatchKeys(edge, role) {
  const keys = new Set([normalizeKey(edge.label)])
  keys.add(normalizeKey(role === 'source' ? edge.to : edge.from))
  ;(edge.details || []).forEach((detail) => {
    keys.add(normalizeKey(detail.name))
    const d = detailsOf(detail)
    for (const value of Object.values(d)) {
      if (typeof value === 'string') {
        keys.add(normalizeKey(value))
        keys.add(normalizeKey(hostFromURL(value)))
      }
    }
  })
  return Array.from(keys).filter(Boolean)
}

function pointsForCurve(a, b) {
  const dx = Math.max(100, Math.abs(b.x - a.x) * 0.42)
  return [
    a,
    { x: a.x + (b.x >= a.x ? dx : -dx), y: a.y },
    { x: b.x - (b.x >= a.x ? dx : -dx), y: b.y },
    b,
  ]
}

function drawText(g, text, x, y, cls, anchor = 'middle') {
  return g.append('text').attr('class', cls).attr('x', x).attr('y', y).attr('text-anchor', anchor).text(text)
}

function visualEdges(edges) {
  const byKey = new Map()
  edges.forEach((edge) => {
    const key = `${edge.from}|${edge.to}|${edge.type}`
    if (!byKey.has(key)) {
      byKey.set(key, { ...edge, label: edge.label, details: [], count: 0, raw: [] })
    }
    const merged = byKey.get(key)
    merged.count += 1
    merged.raw.push(edge)
    merged.details.push(...(edge.details || []))
  })
  return Array.from(byKey.values()).map((edge) => ({
    ...edge,
    label: edge.count > 1 ? `${edge.count} ${edge.type || 'links'}` : edge.label,
  }))
}

function sharedResourceMeta(graph, serviceNames) {
  const servicesByResource = new Map()
  ;(graph.edges || []).forEach((edge) => {
    const fromSvc = serviceNames.has(edge.from) ? edge.from : ''
    const toSvc = serviceNames.has(edge.to) ? edge.to : ''
    const resourceID = fromSvc && !toSvc ? edge.to : toSvc && !fromSvc ? edge.from : ''
    if (!resourceID || serviceNames.has(resourceID)) return
    if (resourceID.startsWith('sched:')) return
    if (!resourceID.startsWith('queue:') && !resourceID.startsWith('db:')) return
    if (!servicesByResource.has(resourceID)) servicesByResource.set(resourceID, new Set())
    servicesByResource.get(resourceID).add(fromSvc || toSvc)
  })
  const visible = new Map()
  servicesByResource.forEach((services, resourceID) => {
    if (services.size > 1) visible.set(resourceID, { serviceCount: services.size, services: Array.from(services).sort() })
  })
  return visible
}

function sharedResourceFacts(graph) {
  const byKey = new Map()
  ;(graph.services || []).forEach((service) => {
    ;(service.dependencies || []).forEach((dep) => {
      const cls = classifyDependency(dep)
      if (!['db_operations', 'cache_operations', 'event_outbound'].includes(cls)) return
      const d = detailsOf(dep)
      const badge = itemBadge(dep, cls === 'event_outbound' ? 'QUEUE' : 'DB')
      const name = semanticResourceName(dep, d)
      const semantic = normalizeSharedName(name)
      if (!semantic || semantic === 'unknown') return
      const tech = badge || sourceBadge(first(d.database_type, d.cache_type, d.platform, d.kind, d.type), 'DB')
      const key = `${tech}:${semantic}`
      if (!byKey.has(key)) {
        byKey.set(key, {
          id: `shared:${normalizeKey(key).replace(/\s+/g, '_')}`,
          name: semantic,
          kind: tech,
          type: tech === 'SQS' || tech === 'SNS' || tech === 'KAFKA' || tech === 'KINESIS' ? 'queue' : 'db',
          services: new Set(),
          facts: [],
        })
      }
      const rec = byKey.get(key)
      rec.services.add(service.name)
      rec.facts.push({ service: service.name, dep })
    })
  })
  const nodes = []
  const edges = []
  byKey.forEach((rec) => {
    if (rec.services.size < 2) return
    const services = Array.from(rec.services).sort()
    const node = {
      id: rec.id,
      name: rec.name,
      kind: rec.kind,
      inferred: true,
      shared: { serviceCount: services.length, services },
      facts: rec.facts,
    }
    nodes.push({ ...node, type: rec.type })
    services.forEach((svc) => {
      edges.push({
        from: svc,
        to: rec.id,
        type: rec.type === 'queue' ? 'queue_publish' : rec.kind === 'REDIS' ? 'cache' : 'database',
        label: rec.kind,
        inferred: true,
        details: rec.facts.filter((f) => f.service === svc).map((f) => f.dep),
      })
    })
  })
  return { nodes, edges }
}

function semanticResourceName(item, d) {
  const raw = first(
    d.resolved_table,
    d.table_or_entity,
    d.database_name,
    d.cache_name,
    d.queue_name,
    d.queue,
    d.destination,
    d.topic,
    d.namespace,
    d.cache_namespace,
    d.key_pattern,
    d.resource_name,
    d.entity_name,
    d.entity,
    item?.name,
  )
  return cleanLabel(raw)
}

function normalizeSharedName(raw) {
  const value = cleanLabel(raw).toLowerCase().replace(/[`"'{}()[\]]/g, ' ').replace(/[_:./-]+/g, ' ').replace(/\s+/g, ' ').trim()
  if (!value) return ''
  if (value.includes('traffic info')) return 'traffic-info'
  if (value.includes('traffic profile')) return 'traffic-profile'
  if (value.includes('automated facts catalogue')) return 'automated_facts_catalogue'
  return value
    .replace(/\b(redis|dynamodb|postgresql|postgres|database|cache|entry|records|record|items|item|entity|entities|get|set|delete|read|write|update|upsert|pipeline|keyspace)\b/g, ' ')
    .replace(/\s+/g, ' ')
    .trim()
}

export function GraphCanvas({ graph, onSelect }) {
  const svgRef = useRef(null)
  const transformRef = useRef(d3.zoomIdentity)
  const graphLayoutKeyRef = useRef('')
  const userMovedRef = useRef(false)
  const programmaticZoomRef = useRef(false)
  const renderKey = graphRenderKey(graph)
  const layoutKey = graphLayoutKey(graph)

  useEffect(() => {
    if (!graph || !svgRef.current) return
    const svgEl = svgRef.current
    const svg = d3.select(svgEl)
    svg.selectAll('*').remove()

    const W = svgEl.clientWidth || 1200
    const H = svgEl.clientHeight || 800
    const services = graph.services || []
    const edges = graph.edges || []
    const serviceNames = new Set(services.map((s) => s.name))
    const visibleResourceIDs = sharedResourceMeta(graph, serviceNames)
    const inferredShared = sharedResourceFacts(graph)
    const displayEdges = [...edges, ...inferredShared.edges]
    const serviceModels = new Map(services.map((s) => [s.name, buildServiceModel(s, edges)]))

    const top = new dagre.graphlib.Graph({ compound: false, multigraph: true })
    top.setGraph({ rankdir: 'LR', nodesep: 100, ranksep: 190, edgesep: 45, marginx: 90, marginy: 90 })
    top.setDefaultEdgeLabel(() => ({}))

    const nodeInfo = new Map()
    services.forEach((svc) => {
      const model = serviceModels.get(svc.name)
      top.setNode(svc.name, { width: model.width, height: model.height, type: 'service', data: svc, model })
      nodeInfo.set(svc.name, { type: 'service', data: svc, label: svc.name })
    })
    ;(graph.external_nodes || []).forEach((n) => addTopNode(top, nodeInfo, n.name, 'external', n, cleanLabel(n.name)))
    ;(graph.queue_nodes || []).forEach((n) => { const id = `queue:${n.id}`; if (visibleResourceIDs.has(id)) addTopNode(top, nodeInfo, id, 'queue', { ...n, shared: visibleResourceIDs.get(id) }, cleanLabel(n.name)) })
    ;(graph.database_nodes || []).forEach((n) => { const id = `db:${n.id}`; if (visibleResourceIDs.has(id)) addTopNode(top, nodeInfo, id, 'db', { ...n, shared: visibleResourceIDs.get(id) }, cleanLabel(n.name)) })
    ;(graph.scheduler_nodes || []).forEach((n) => { const id = `sched:${n.id}`; if (visibleResourceIDs.has(id)) addTopNode(top, nodeInfo, id, 'scheduler', { ...n, shared: visibleResourceIDs.get(id) }, cleanLabel(n.name)) })
    inferredShared.nodes.forEach((n) => addTopNode(top, nodeInfo, n.id, n.type, n, cleanLabel(n.name)))

    displayEdges.forEach((e, i) => {
      if (top.hasNode(e.from) && top.hasNode(e.to)) top.setEdge(e.from, e.to, { type: e.type, data: e }, `e${i}`)
    })

    dagre.layout(top)

    const previousLayoutKey = graphLayoutKeyRef.current
    const preserveUserView = userMovedRef.current && previousLayoutKey === layoutKey
    const rootG = svg.append('g')
    const zoom = d3.zoom().scaleExtent([0.08, 2.4]).on('zoom', (ev) => {
      transformRef.current = ev.transform
      if (!programmaticZoomRef.current && ev.sourceEvent) userMovedRef.current = true
      rootG.attr('transform', ev.transform)
    })
    svg.call(zoom)
    svg.on('click', () => selectThing(null))

    const defs = svg.append('defs')
    Object.entries(EDGE_COLORS).forEach(([type, color]) => {
      defs.append('marker').attr('id', `arr-${type}`).attr('viewBox', '0 -5 10 10')
        .attr('refX', 9).attr('refY', 0).attr('markerWidth', 7).attr('markerHeight', 7).attr('orient', 'auto')
        .append('path').attr('d', 'M0,-4L10,0L0,4').attr('fill', color)
    })

    const topPositions = new Map()
    top.nodes().forEach((id) => topPositions.set(id, top.node(id)))

    const servicePorts = new Map()
    serviceModels.forEach((model, name) => {
      const n = top.node(name)
      if (n) servicePorts.set(name, computeServicePorts(model, n.x, n.y))
    })

    const frameGroup = rootG.append('g').attr('class', 'team-frames')
    drawTeamFrames(frameGroup, services, serviceModels, topPositions)

    const edgeGroup = rootG.append('g').attr('class', 'instance-edges')
    visualEdges(displayEdges).forEach((edge) => {
      const from = endpointFor(edge.from, edge, 'source', topPositions, servicePorts, serviceNames)
      const to = endpointFor(edge.to, edge, 'target', topPositions, servicePorts, serviceNames)
      if (!from || !to) return
      const line = d3.line().x((p) => p.x).y((p) => p.y).curve(d3.curveBasis)
      edgeGroup.append('path')
        .attr('class', `edge type-${edge.type || ''}`)
        .attr('d', line(pointsForCurve(from, to)))
        .attr('data-from', edge.from)
        .attr('data-to', edge.to)
        .attr('data-count', edge.count || 1)
        .style('stroke-width', Math.min(4, 1.5 + Math.log2(edge.count || 1) * 0.55))
        .attr('marker-end', `url(#arr-${edge.type || 'http'})`)
        .on('click', (ev) => { ev.stopPropagation(); selectThing({ kind: 'edge', data: edge }) })
    })

    const nodeGroup = rootG.append('g').attr('class', 'instance-nodes')
    top.nodes().forEach((id) => {
      const n = top.node(id)
      if (!n) return
      if (n.type === 'service') {
        drawServiceNode(nodeGroup, n.model, n.x, n.y, onSelect, selectThing)
      } else {
        drawResourceNode(nodeGroup, id, n, onSelect, selectThing)
      }
    })

    function selectThing(sel) {
      rootG.selectAll('[data-select-id]').classed('selected', false).classed('hl-dimmed', false)
      rootG.selectAll('.edge').classed('hl-active', false).classed('hl-dimmed', false)
      if (!sel) {
        onSelect && onSelect(null)
        return
      }
      const selectedID = sel.id || (sel.data && (sel.data.name || sel.data.id)) || sel.kind
      rootG.selectAll(`[data-select-id="${cssEscape(selectedID)}"]`).classed('selected', true)
      if (sel.kind !== 'edge' && selectedID) {
        rootG.selectAll('.edge').each(function () {
          const el = d3.select(this)
          const active = el.attr('data-from') === selectedID || el.attr('data-to') === selectedID
          el.classed(active ? 'hl-active' : 'hl-dimmed', true)
        })
      }
      onSelect && onSelect(sel)
    }

    graphLayoutKeyRef.current = layoutKey
    programmaticZoomRef.current = true
    if (preserveUserView) {
      svg.call(zoom.transform, transformRef.current)
    } else {
      const graphW = top.graph().width || 1000
      const graphH = top.graph().height || 700
      const pad = 60
      const fitScale = Math.min((W - pad * 2) / graphW, (H - pad * 2) / graphH, 1.05)
      const scale = Math.min(Math.max(fitScale, 0.12), 1.05)
      const tx = (W - graphW * scale) / 2
      const ty = (H - graphH * scale) / 2
      transformRef.current = d3.zoomIdentity.translate(tx, ty).scale(scale)
      userMovedRef.current = false
      svg.call(zoom.transform, transformRef.current)
    }
    programmaticZoomRef.current = false

    return () => {
      svg.on('.zoom', null)
    }
  }, [renderKey])

  return <svg ref={svgRef} class="graph-svg-full graph-svg-instance" />
}

function graphLayoutKey(graph) {
  if (!graph) return ''
  const services = (graph.services || []).map((s) => s.name).sort()
  const edges = (graph.edges || []).map((e) => `${e.from}>${e.to}:${e.type || ''}:${e.label || ''}`).sort()
  const resources = [
    ...(graph.external_nodes || []).map((n) => `ext:${n.name}`),
    ...(graph.queue_nodes || []).map((n) => `queue:${n.id || n.name}`),
    ...(graph.database_nodes || []).map((n) => `db:${n.id || n.name}`),
    ...(graph.scheduler_nodes || []).map((n) => `sched:${n.id || n.name}`),
  ].sort()
  return JSON.stringify({ run: graph.run_id || '', services, edges, resources })
}

function graphRenderKey(graph) {
  if (!graph) return ''
  const services = (graph.services || []).map((s) => [
    s.name,
    s.team || '',
    s.diffmind_freshness || '',
    s.repo_metrics?.total_loc || 0,
    s.repo_metrics?.languages?.[0]?.language || '',
  ]).sort()
  return JSON.stringify({ layout: graphLayoutKey(graph), services })
}

function addTopNode(top, nodeInfo, id, type, data, label) {
  const shared = data && data.shared
  const w = shared ? Math.max(230, Math.min(360, label.length * 8 + 116)) : Math.max(170, Math.min(290, label.length * 8 + 72))
  const h = shared ? 112 : type === 'scheduler' ? 64 : 86
  top.setNode(id, { width: w, height: h, type, data, label })
  nodeInfo.set(id, { type, data, label })
}

function computeServicePorts(model, cx, cy) {
  const hull = model.hull
  const ports = {
    hullIn: { x: cx + hull.x - hull.width / 2, y: cy + hull.y },
    hullOut: { x: cx + hull.x + hull.width / 2, y: cy + hull.y },
    hullTop: { x: cx + hull.x, y: cy + hull.y - hull.height / 2 },
    groups: {},
    items: [],
  }
  model.objectives.forEach((group) => assignGroupPorts(ports, group, cx + group.x, cy + group.y, 'objective'))
  model.left.forEach((group) => assignGroupPorts(ports, group, cx + group.x, cy + group.y, 'exposure'))
  model.right.forEach((group) => assignGroupPorts(ports, group, cx + group.x, cy + group.y, 'dependency'))
  return ports
}

function assignGroupPorts(ports, group, cx, cy, side) {
  const portX = side === 'exposure' ? cx + group.width / 2 : side === 'dependency' ? cx - group.width / 2 : cx
  const portY = side === 'objective' ? cy + group.height / 2 : cy
  ports.groups[group.key] = { x: portX, y: portY, side, group }
}

function endpointFor(id, edge, role, topPositions, servicePorts, serviceNames) {
  if (serviceNames.has(id)) return serviceAnchor(id, edge, role, servicePorts)
  const n = topPositions.get(id)
  if (!n) return null
  return { x: n.x, y: n.y }
}

function serviceAnchor(serviceName, edge, role, servicePorts) {
  const ports = servicePorts.get(serviceName)
  if (!ports) return null
  const groupKey = groupForEdge(edge, role)
  if (ports.groups[groupKey]) return ports.groups[groupKey]
  return role === 'source' ? ports.hullOut : ports.hullIn
}

function drawServiceNode(parent, model, cx, cy, onSelect, selectThing) {
  const ports = computeServicePorts(model, cx, cy)
  const g = parent.append('g')
    .attr('class', 'service-system')
    .attr('data-select-id', model.service.name)
    .on('click', (ev) => {
      ev.stopPropagation()
      selectThing({ kind: 'service', data: model.service, id: model.service.name })
    })

  drawServiceBoundary(g, model, cx, cy)
  model.objectives.forEach((group) => drawGroup(g, group, cx + group.x, cy + group.y, 'objective', ports, selectThing))
  model.left.forEach((group) => drawGroup(g, group, cx + group.x, cy + group.y, 'exposure', ports, selectThing))
  model.right.forEach((group) => drawGroup(g, group, cx + group.x, cy + group.y, 'dependency', ports, selectThing))

  drawHull(g, model, cx + model.hull.x, cy + model.hull.y)

  g.append('circle').attr('class', 'service-port exposure-port').attr('cx', ports.hullIn.x).attr('cy', ports.hullIn.y).attr('r', 13)
  g.append('circle').attr('class', 'service-port dependency-port').attr('cx', ports.hullOut.x).attr('cy', ports.hullOut.y).attr('r', 13)
  g.append('circle').attr('class', 'service-port objective-port').attr('cx', ports.hullTop.x).attr('cy', ports.hullTop.y).attr('r', 13)

  return g
}

function drawServiceBoundary(g, model, cx, cy) {
  const x = cx - model.width / 2 + 20
  const y = cy - model.height / 2 + 20
  const w = model.width - 40
  const h = model.height - 40
  g.append('rect')
    .attr('class', 'service-boundary')
    .attr('x', x)
    .attr('y', y)
    .attr('width', w)
    .attr('height', h)
    .attr('rx', 18)
    .append('title').text(`${model.service.name} service boundary`)
  drawText(g, 'EXPOSURES', x + 28, y + 30, 'service-lane-label', 'start')
  drawText(g, 'DEPENDENCIES', x + w - 28, y + 30, 'service-lane-label', 'end')
}

function drawHull(g, model, cx, cy) {
  const { service, hull } = model
  const left = cx - hull.width / 2
  const right = cx + hull.width / 2
  const top = cy - hull.height / 2
  const bottom = cy + hull.height / 2
  const nose = left - 52
  const d = [
    `M ${left} ${top}`,
    `C ${left + 38} ${top - 20}, ${right - 42} ${top - 18}, ${right} ${top + 32}`,
    `C ${right + 34} ${cy - 5}, ${right + 34} ${cy + 5}, ${right} ${bottom - 32}`,
    `C ${right - 42} ${bottom + 18}, ${left + 38} ${bottom + 20}, ${left} ${bottom}`,
    `L ${nose} ${cy}`,
    'Z',
  ].join(' ')
  g.append('path').attr('class', 'service-hull').attr('d', d)
  g.append('path').attr('class', 'service-hull-line').attr('d', `M ${left + 6} ${top + 28} C ${left + 52} ${top + 2}, ${cx - 56} ${top + 18}, ${cx - 16} ${top + 22}`)
  g.append('path').attr('class', 'service-hull-line').attr('d', `M ${left + 6} ${bottom - 28} C ${left + 52} ${bottom - 2}, ${cx - 56} ${bottom - 18}, ${cx - 16} ${bottom - 22}`)
  drawText(g, shortLabel(service.name, 28), cx, cy - 10, 'service-name')
  drawText(g, 'facts grouped by extracted instances', cx, cy + 23, 'service-caption')
  const counts = [
    (service.http_routes || []).length && `${(service.http_routes || []).length} routes`,
    (service.dependencies || []).length && `${(service.dependencies || []).length} deps`,
    (service.connections || []).length && `${(service.connections || []).length} flows`,
  ].filter(Boolean).join(' · ')
  if (counts) drawText(g, counts, cx, cy + 52, 'service-counts')
  drawServiceBadges(g, service, cx, cy + 78)
}

function drawServiceBadges(g, service, cx, y) {
  const metrics = service.repo_metrics || {}
  const lang = metrics.languages && metrics.languages[0] ? metrics.languages[0].language : ''
  const loc = metrics.total_loc ? `${Math.round(metrics.total_loc / 100) / 10}k LOC` : ''
  const badges = [
    service.team || 'default',
    [lang, loc].filter(Boolean).join(' · '),
    service.diffmind_freshness || '',
  ].filter(Boolean)
  const totalW = badges.reduce((sum, b) => sum + Math.max(54, b.length * 7 + 22), 0) + Math.max(0, badges.length - 1) * 6
  let x = cx - totalW / 2
  badges.forEach((badge) => {
    const w = Math.max(54, badge.length * 7 + 22)
    const cls = badge === 'stale' ? 'service-badge stale' : badge === 'fresh' ? 'service-badge fresh' : 'service-badge'
    g.append('rect').attr('class', cls).attr('x', x).attr('y', y - 13).attr('width', w).attr('height', 22).attr('rx', 11)
    drawText(g, shortLabel(badge, 18), x + w / 2, y + 3, 'service-badge-text')
    x += w + 6
  })
}

function drawTeamFrames(parent, services, serviceModels, topPositions) {
  const teams = new Map()
  services.forEach((svc) => {
    const pos = topPositions.get(svc.name)
    const model = serviceModels.get(svc.name)
    if (!pos || !model) return
    const team = svc.team || 'default'
    const item = teams.get(team) || { name: team, minX: Infinity, minY: Infinity, maxX: -Infinity, maxY: -Infinity, count: 0 }
    item.minX = Math.min(item.minX, pos.x - model.width / 2 - 54)
    item.maxX = Math.max(item.maxX, pos.x + model.width / 2 + 54)
    item.minY = Math.min(item.minY, pos.y - model.height / 2 - 60)
    item.maxY = Math.max(item.maxY, pos.y + model.height / 2 + 60)
    item.count += 1
    teams.set(team, item)
  })
  Array.from(teams.values()).forEach((team, i) => {
    if (!Number.isFinite(team.minX)) return
    const g = parent.append('g').attr('class', 'team-frame')
    const x = team.minX
    const y = team.minY
    const w = team.maxX - team.minX
    const h = team.maxY - team.minY
    g.append('rect').attr('class', `team-frame-bg tone-${i % 5}`).attr('x', x).attr('y', y).attr('width', w).attr('height', h).attr('rx', 24)
    g.append('text').attr('class', 'team-frame-label').attr('x', x + 24).attr('y', y + 34).text(`${team.name} · ${team.count}`)
  })
}

function drawGroup(g, group, cx, cy, kind, ports, selectThing) {
  const colors = GROUP_COLORS[kind]
  const gg = g.append('g')
    .attr('class', `objective-group ${kind}-group`)
    .attr('data-select-id', `${group.key}:${group.title}`)
    .on('click', (ev) => {
      ev.stopPropagation()
      selectThing({ kind: 'group', id: `${group.key}:${group.title}`, data: { name: group.title, kind: group.key, count: group.items.length, items: group.items } })
    })
  const x = cx - group.width / 2
  const y = cy - group.height / 2
  gg.append('rect').attr('class', 'group-card-bg').attr('x', x).attr('y', y).attr('width', group.width).attr('height', group.height).attr('rx', 10)
    .attr('fill', colors.fill).attr('stroke', colors.stroke)
    .append('title').text(`${group.title}: ${group.items.length} extracted instance${group.items.length === 1 ? '' : 's'}`)
  gg.append('rect').attr('class', 'group-card-accent').attr('x', x).attr('y', y).attr('width', 5).attr('height', group.height).attr('rx', 3).attr('fill', colors.stroke)
  drawText(gg, group.title, x + 18, y + 27, 'group-title', 'start')
  drawText(gg, `${group.items.length} instance${group.items.length === 1 ? '' : 's'}`, x + group.width - 18, y + 27, 'group-count', 'end')
  drawText(gg, group.subtitle, x + 18, y + 49, 'group-subtitle', 'start')

  const chipW = (group.width - 48) / 2
  group.items.forEach((item, i) => drawFactChip(gg, group, item, x + 18 + (i % 2) * (chipW + 12), y + 66 + Math.floor(i / 2) * (CHIP_H + CHIP_ROW_GAP), chipW, colors, selectThing))

  const port = ports.groups[group.key]
  if (port) gg.append('circle').attr('class', `${kind}-port group-port`).attr('cx', port.x).attr('cy', port.y).attr('r', 11)
}

function drawFactChip(g, group, item, x, y, width, colors, selectThing) {
  const chip = g.append('g')
    .attr('class', 'instance-fact fact-chip')
    .attr('data-select-id', `${group.key}:${item.key}`)
    .on('click', (ev) => {
      ev.stopPropagation()
      selectThing({ kind: 'fact', id: `${group.key}:${item.key}`, data: { name: item.label, kind: group.key, count: item.items.length, items: item.items } })
    })
  chip.append('rect').attr('x', x).attr('y', y).attr('width', width).attr('height', 23).attr('rx', 6)
    .attr('fill', '#111827').attr('stroke', colors.inner)
    .append('title').text(`${item.label}${item.sublabel ? `: ${item.sublabel}` : ''}`)
  const badge = cleanLabel(item.badge || '')
  const badgeW = badge ? Math.max(30, Math.min(58, badge.length * 6 + 12)) : 0
  if (badge) {
    chip.append('rect').attr('class', 'fact-badge').attr('x', x + 5).attr('y', y + 5).attr('width', badgeW).attr('height', 13).attr('rx', 4).attr('fill', colors.inner)
    drawText(chip, badge, x + 5 + badgeW / 2, y + 15, 'fact-badge-text')
  } else {
    chip.append('circle').attr('cx', x + 11).attr('cy', y + 11.5).attr('r', 3.5).attr('fill', colors.inner)
  }
  drawText(chip, shortLabel(item.label, Math.max(8, Math.floor((width - badgeW - 20) / 8))), x + 10 + badgeW + 8, y + 15.5, 'fact-title', 'start')
}

function drawResourceNode(parent, id, n, onSelect, selectThing) {
  const colors = NODE_KINDS[n.type] || NODE_KINDS.external
  const shared = n.data && n.data.shared
  const g = parent.append('g')
    .attr('class', `resource-node resource-${n.type}${shared ? ' shared-resource' : ''}`)
    .attr('data-select-id', id)
    .on('click', (ev) => {
      ev.stopPropagation()
      selectThing({ kind: n.type, data: n.data, id })
    })
  g.append('ellipse').attr('cx', n.x).attr('cy', n.y).attr('rx', n.width / 2).attr('ry', n.height / 2)
    .attr('fill', colors.fill).attr('stroke', colors.stroke)
    .append('title').text(`${n.label || n.data?.name || id}${shared ? ` shared by ${shared.serviceCount} services` : ''}`)
  const badge = resourceBadge(n)
  const badgeW = Math.max(34, Math.min(74, badge.length * 6 + 14))
  g.append('rect').attr('class', 'resource-kind-badge').attr('x', n.x - badgeW / 2).attr('y', n.y - 36).attr('width', badgeW).attr('height', 17).attr('rx', 5).attr('fill', colors.stroke)
  drawText(g, badge, n.x, n.y - 23, 'resource-kind-text')
  drawText(g, shortLabel(n.label || n.data?.name || id, shared ? 28 : 24), n.x, n.y + 2, 'resource-title')
  drawText(g, shared ? `shared by ${shared.serviceCount} services` : resourceSubtitle(n), n.x, n.y + 25, 'resource-subtitle')
}

function resourceSubtitle(n) {
  if (n.type === 'db') return cleanLabel(n.data?.kind || 'database')
  if (n.type === 'queue') return cleanLabel(n.data?.kind || 'queue')
  if (n.type === 'scheduler') return cleanLabel(n.data?.schedule || 'scheduler')
  return cleanLabel(n.data?.kind || 'external')
}

function resourceBadge(n) {
  if (n.type === 'db') return sourceBadge(n.data?.kind || 'DB', 'DB')
  if (n.type === 'queue') return sourceBadge(n.data?.kind || 'QUEUE', 'QUEUE')
  if (n.type === 'scheduler') return 'CRON'
  return sourceBadge(n.data?.kind || 'API', 'API')
}

function cssEscape(s) {
  if (window.CSS && window.CSS.escape) return window.CSS.escape(s)
  return String(s).replace(/["\\]/g, '\\$&')
}
