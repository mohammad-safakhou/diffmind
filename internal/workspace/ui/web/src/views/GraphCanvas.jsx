import { useEffect, useRef, useState } from 'preact/hooks'
import * as d3 from 'd3'
import dagre from 'dagre'

const GROUP_COLORS = {
  exposure: { fill: '#0e2b28', stroke: '#22c997', inner: '#45d6cb', port: '#22c997' },
  dependency: { fill: '#301f11', stroke: '#f5943a', inner: '#d9954f', port: '#f5943a' },
  objective: { fill: '#131d38', stroke: '#7291ff', inner: '#8aa0e8', port: '#7291ff' },
}

const EDGE_COLORS = {
  http: '#3b9eff',
  rpc: '#9b7cf9',
  workflow: '#38bdf8',
  queue_publish: '#22c997',
  queue_consume: '#22c997',
  database: '#f5943a',
  cache: '#ef5455',
  object_storage: '#f59e0b',
  scheduler: '#f0c040',
}

const NODE_KINDS = {
  external: { fill: '#16141e', stroke: '#9b7cf9', text: '#d9cdfd' },
  queue: { fill: '#0e1a16', stroke: '#22c997', text: '#b9f5df' },
  db: { fill: '#1a1510', stroke: '#f5943a', text: '#ffe0bd' },
  cache: { fill: '#211217', stroke: '#ef5455', text: '#ffd2d2' },
  object_storage: { fill: '#211a0d', stroke: '#f59e0b', text: '#ffe6b0' },
  scheduler: { fill: '#151118', stroke: '#f0c040', text: '#f7e7a6' },
  workflow: { fill: '#0b1f2e', stroke: '#38bdf8', text: '#c6f3ff' },
}

const GROUP_GAP = 18
const SERVICE_PAD_X = 34
const SERVICE_PAD_TOP = 44
const SERVICE_PAD_BOTTOM = 34
const ENTRY_COL_W = 560
const FLOW_COL_W = 620
const DEP_COL_W = 560
const WORKSPACE_COLUMN_GAP = 22
const WORKSPACE_HEADER_H = 118
const WORKSPACE_BODY_H = 760
const WORKSPACE_SECTION_HEADER_H = 48
const WORKSPACE_SECTION_VIEWPORT_H = 330
const WORKSPACE_SECTION_FOOT_H = 24
const WORKSPACE_SECTION_GAP = 18
const WORKSPACE_SECTION_INNER_GAP = 12
const WORKSPACE_ROW_H = 44
const WORKSPACE_GROUP_HEAD_H = 56
const WORKSPACE_GROUP_FOOT_H = 28
const COLLAPSED_ROWS = 0
const EXPANDED_ROW_LIMIT = 5
const FLOW_TRACE_LIMIT = 4
const COMPACT_SERVICE_W = 300
const COMPACT_SERVICE_H = 116
const LARGE_GRAPH_SERVICE_THRESHOLD = 70
const TEAM_GRID_COLS = 3
const TEAM_BLOCK_GAP_X = 520
const TEAM_BLOCK_GAP_Y = 360
const TEAM_SERVICE_GAP_X = 120
const TEAM_SERVICE_GAP_Y = 70
const TEAM_RESOURCE_GAP_X = 54
const TEAM_RESOURCE_GAP_Y = 24
const TEAM_ATTACH_GAP_X = 54
const TEAM_ATTACH_GAP_Y = 18
const TEAM_LEFT_ATTACH_W = 310
const TEAM_RIGHT_ATTACH_W = 430

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

function metadataDetail(d, key) {
  const meta = d && d.metadata
  const details = meta && meta.details
  const value = details && details[key]
  return value === undefined || value === null ? '' : String(value)
}

function itemName(item) {
  return first(item?.name, item?.summary, 'instance')
}

function classifyDependency(dep) {
  const d = detailsOf(dep)
  const hay = `${dep?.name || ''} ${dep?.summary || ''} ${Object.keys(d).join(' ')} ${Object.values(d).join(' ')}`.toLowerCase()
  if (hay.includes('workflow') || hay.includes('camunda') || hay.includes('external_task') || hay.includes('external-task') || hay.includes('orchestrator')) return 'workflow_orchestration'
  if (hay.includes('cache') || hay.includes('redis') || hay.includes('key_pattern')) return 'cache_operations'
  if (hay.includes('database') || hay.includes('table') || hay.includes('entity') || hay.includes('postgres') || hay.includes('dynamo') || hay.includes('sql')) return 'db_operations'
  if (hay.includes('queue') || hay.includes('topic') || hay.includes('kafka') || hay.includes('sqs') || hay.includes('sns') || hay.includes('publish')) return 'event_outbound'
  if (hay.includes('grpc') || hay.includes('rpc') || hay.includes('protobuf') || hay.includes('proto')) return 'rpc_outbound'
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
  if (value.includes('grpc')) return 'gRPC'
  if (value.includes('rpc')) return 'RPC'
  if (value.includes('http')) return 'HTTP'
  return cleanLabel(raw || fallback).slice(0, 10).toUpperCase()
}

function itemBadge(item, fallback = '') {
  const d = detailsOf(item)
  return sourceBadge(first(d.database_type, d.cache_type, d.platform, d.kind, d.type, d.source, d.method, item?.operation_kind), fallback)
}

function objectID(item) {
  const d = detailsOf(item)
  return first(item?.id, d.id, metadataDetail(d, 'id'), item?.name)
}

function objectKind(item) {
  const d = detailsOf(item)
  return first(item?.kind, d.kind, metadataDetail(d, 'kind'), item?.type, d.type)
}

function objectPath(item) {
  const d = detailsOf(item)
  return first(d.path, d.route, d.endpoint, item?.name)
}

function routePrefix(path) {
  const value = cleanLabel(path)
  if (!value || !value.startsWith('/')) return '/other'
  const parts = value.split('/').filter(Boolean)
  return parts.length ? `/${parts[0]}` : '/'
}

function groupKeyPart(value) {
  return normalizeKey(value).replace(/\s+/g, '_') || 'other'
}

function makeRows(items, build, connections, role) {
  return (items || []).map((item, index) => {
    const d = detailsOf(item)
    const id = objectID(item)
    const row = build(item, d) || {}
    return {
      key: first(id, item?.name, `row-${index}`),
      objectID: id,
      label: first(row.label, item?.name, id, 'object'),
      badge: first(row.badge, itemBadge(item, 'OBJ')),
      sublabel: first(row.sublabel, item?.summary),
      meta: first(row.meta, objectKind(item)),
      items: [item],
      traceCount: countObjectConnections(id, item?.name, connections, role),
      matchKeys: objectMatchKeys(item),
    }
  }).sort((a, b) => {
    const ak = `${a.badge}|${a.label}`
    const bk = `${b.badge}|${b.label}`
    return ak.localeCompare(bk)
  })
}

function objectMatchKeys(item) {
  const keys = new Set([normalizeKey(objectID(item)), normalizeKey(item?.name), normalizeKey(item?.summary)])
  const d = detailsOf(item)
  Object.values(d).forEach((value) => {
    if (typeof value === 'string') {
      keys.add(normalizeKey(value))
      keys.add(normalizeKey(hostFromURL(value)))
    }
    if (Array.isArray(value)) value.forEach((v) => keys.add(normalizeKey(String(v))))
  })
  return Array.from(keys).filter(Boolean)
}

function countObjectConnections(id, name, connections, role) {
  const key = normalizeKey(id || name)
  if (!key) return 0
  return (connections || []).filter((conn) => {
    const candidates = role === 'dependency'
      ? [conn.to_id, conn.to_name, conn.to]
      : [conn.from_id, conn.entrypoint_id, conn.from_name, conn.from]
    return candidates.some((v) => {
      const normalized = normalizeKey(v)
      return normalized && (normalized === key || normalized.includes(key) || key.includes(normalized))
    })
  }).length
}

function splitRowsIntoGroups(rows, groupBy, titleFor, subtitleFor) {
  const byGroup = new Map()
  rows.forEach((row) => {
    const groupValue = groupBy(row) || 'other'
    const key = groupKeyPart(groupValue)
    if (!byGroup.has(key)) {
      byGroup.set(key, { key, groupValue, rows: [] })
    }
    byGroup.get(key).rows.push(row)
  })
  return Array.from(byGroup.values()).sort((a, b) => a.groupValue.localeCompare(b.groupValue)).map((group) => ({
    key: group.key,
    title: titleFor(group.groupValue, group.rows),
    subtitle: subtitleFor(group.groupValue, group.rows),
    items: group.rows,
  }))
}

function clamp(n, min, max) {
  return Math.min(max, Math.max(min, n))
}

function measureGroup(group, expandedGroups, groupScrollOffsets, serviceName) {
  const stateKey = groupStateKey(serviceName, group.key)
  const expanded = expandedGroups.has(stateKey)
  const visibleCount = expanded ? Math.min(group.items.length, EXPANDED_ROW_LIMIT) : Math.min(group.items.length, COLLAPSED_ROWS)
  const maxScroll = Math.max(0, group.items.length - visibleCount)
  const scrollOffset = expanded ? clamp(Number(groupScrollOffsets.get(stateKey) || 0), 0, maxScroll) : 0
  const hiddenBefore = scrollOffset
  const hiddenAfter = Math.max(0, group.items.length - scrollOffset - visibleCount)
  const hidden = Math.max(0, group.items.length - visibleCount)
  return {
    ...group,
    expanded,
    visibleCount,
    scrollOffset,
    hiddenBefore,
    hiddenAfter,
    hidden,
    stateKey,
    width: group.side === 'dependency' ? DEP_COL_W : ENTRY_COL_W,
    height: WORKSPACE_GROUP_HEAD_H + visibleCount * WORKSPACE_ROW_H + (hidden ? WORKSPACE_GROUP_FOOT_H : 10),
  }
}

function measureGroups(groups, expandedGroups, groupScrollOffsets, serviceName) {
  return groups.map((group) => measureGroup(group, expandedGroups, groupScrollOffsets, serviceName))
}

function stackHeight(items, gap = GROUP_GAP) {
  if (!items.length) return 0
  return items.reduce((sum, item) => sum + item.height, 0) + (items.length - 1) * gap
}

function maxGroupWidth(groups) {
  return groups.reduce((max, group) => Math.max(max, group.width), 0)
}

function sectionTitle(lane, side) {
  const titles = {
    http_inbound: 'Inbound HTTP',
    rpc_inbound: 'Inbound RPC',
    event_inbound: 'Event consumers',
    scheduled_jobs: 'Scheduled jobs',
    webhooks: 'Webhooks',
    cli_commands: 'Commands',
    http_outbound: 'Outbound HTTP',
    rpc_outbound: 'Outbound RPC',
    db_operations: 'Database operations',
    cache_operations: 'Cache operations',
    event_outbound: 'Event publishers',
    other_dependencies: 'Other dependencies',
  }
  return titles[lane] || (side === 'dependency' ? 'Dependencies' : 'Entrypoints')
}

function sectionSubtitle(lane, groupCount, itemCount) {
  const labels = {
    http_inbound: 'routes grouped by path prefix',
    rpc_inbound: 'gRPC/protobuf services and methods',
    event_inbound: 'queues, topics, and stream sources',
    scheduled_jobs: 'time-based service entrypoints',
    webhooks: 'external callbacks',
    cli_commands: 'manual and automation commands',
    http_outbound: 'targets grouped by host/service',
    rpc_outbound: 'gRPC/protobuf targets grouped by service',
    db_operations: 'tables/resources with query operations',
    cache_operations: 'cache instances and key spaces',
    event_outbound: 'publish targets grouped by queue/topic',
    other_dependencies: 'uncategorized downstream facts',
  }
  return `${groupCount} group${groupCount === 1 ? '' : 's'} · ${itemCount} object${itemCount === 1 ? '' : 's'} · ${labels[lane] || 'semantic groups'}`
}

function makeSections(groups, side, serviceName, sectionScrollOffsets) {
  const byLane = new Map()
  groups.forEach((group) => {
    const lane = group.lane || group.key
    if (!byLane.has(lane)) byLane.set(lane, [])
    byLane.get(lane).push(group)
  })
  return Array.from(byLane.entries()).map(([lane, laneGroups]) => {
    const contentH = stackHeight(laneGroups, WORKSPACE_SECTION_INNER_GAP)
    const viewportH = Math.min(contentH, WORKSPACE_SECTION_VIEWPORT_H)
    const hidden = Math.max(0, contentH - viewportH)
    const stateKey = groupStateKey(serviceName, `section:${lane}:${side}`)
    const scrollOffset = clamp(Number(sectionScrollOffsets.get(stateKey) || 0), 0, hidden)
    const itemCount = laneGroups.reduce((sum, group) => sum + group.items.length, 0)
    return {
      key: `${side}_${lane}`,
      lane,
      side,
      stateKey,
      title: sectionTitle(lane, side),
      subtitle: sectionSubtitle(lane, laneGroups.length, itemCount),
      groups: laneGroups,
      itemCount,
      groupCount: laneGroups.length,
      scrollOffset,
      hiddenBefore: scrollOffset,
      hiddenAfter: Math.max(0, hidden - scrollOffset),
      contentH,
      viewportH,
      width: side === 'dependency' ? DEP_COL_W : ENTRY_COL_W,
      height: WORKSPACE_SECTION_HEADER_H + viewportH + (hidden ? WORKSPACE_SECTION_FOOT_H : 12),
    }
  })
}

function layoutServiceModel(model) {
  const leftW = ENTRY_COL_W
  const centerW = FLOW_COL_W
  const rightW = DEP_COL_W
  const leftStackH = stackHeight(model.leftSections, WORKSPACE_SECTION_GAP)
  const rightStackH = stackHeight(model.rightSections, WORKSPACE_SECTION_GAP)
  const bodyH = Math.max(WORKSPACE_BODY_H, leftStackH, rightStackH)
  const bodyW = leftW + centerW + rightW + WORKSPACE_COLUMN_GAP * 2

  model.workspace = {
    x: 0,
    y: 0,
    width: bodyW,
    height: bodyH + SERVICE_PAD_TOP + SERVICE_PAD_BOTTOM,
  }
  model.width = model.workspace.width + SERVICE_PAD_X * 2
  model.height = model.workspace.height
  model.boundary = {
    x: -model.width / 2,
    y: -model.height / 2,
    width: model.width,
    height: model.height,
  }

  const contentTop = -bodyH / 2
  const leftX = -bodyW / 2 + leftW / 2
  const centerX = leftX + leftW / 2 + WORKSPACE_COLUMN_GAP + centerW / 2
  const rightX = bodyW / 2 - rightW / 2
  layoutSections(model.leftSections, leftX, contentTop)
  layoutSections(model.rightSections, rightX, contentTop)
  model.center = {
    x: centerX,
    y: 0,
    width: centerW,
    height: bodyH,
  }
  return model
}

function layoutSections(sections, x, top) {
  let y = top
  sections.forEach((section) => {
    section.x = x
    section.y = y + section.height / 2
    const contentTop = section.y - section.height / 2 + WORKSPACE_SECTION_HEADER_H - section.scrollOffset
    let gy = contentTop
    section.groups.forEach((group) => {
      group.x = x
      group.y = gy + group.height / 2
      gy += group.height + WORKSPACE_SECTION_INNER_GAP
    })
    y += section.height + WORKSPACE_SECTION_GAP
  })
}

function recenterServiceModel(model) {
  const boxes = [
    {
      x: model.hull.x - model.hull.width / 2,
      y: model.hull.y - model.hull.height / 2,
      width: model.hull.width,
      height: model.hull.height,
    },
    ...model.left.map(groupBox),
    ...model.right.map(groupBox),
    ...model.objectives.map(groupBox),
  ]
  const minX = Math.min(...boxes.map((b) => b.x))
  const minY = Math.min(...boxes.map((b) => b.y))
  const maxX = Math.max(...boxes.map((b) => b.x + b.width))
  const maxY = Math.max(...boxes.map((b) => b.y + b.height))
  const centerX = (minX + maxX) / 2
  const centerY = (minY + maxY) / 2
  const shift = (obj) => {
    obj.x -= centerX
    obj.y -= centerY
  }
  shift(model.hull)
  model.left.forEach(shift)
  model.right.forEach(shift)
  model.objectives.forEach(shift)

  const contentW = maxX - minX
  const contentH = maxY - minY
  model.width = contentW + SERVICE_PAD_X * 2
  model.height = contentH + SERVICE_PAD_TOP + SERVICE_PAD_BOTTOM
  model.boundary = {
    x: -model.width / 2,
    y: -contentH / 2 - SERVICE_PAD_TOP,
    width: model.width,
    height: model.height,
  }
}

function groupBox(group) {
  return {
    x: group.x - group.width / 2,
    y: group.y - group.height / 2,
    width: group.width,
    height: group.height,
  }
}

function groupStateKey(serviceName, groupKey) {
  return `${serviceName}::${groupKey}`
}

function buildServiceModel(service, graphEdges, options = {}) {
  const connections = service.connections || []
  const deps = service.dependencies || []
  const depByClass = deps.reduce((acc, dep) => {
    const cls = classifyDependency(dep)
    ;(acc[cls] ||= []).push(dep)
    return acc
  }, {})

  const httpRows = makeRows(service.http_routes, (it, d) => ({
    badge: sourceBadge(first(d.method, 'HTTP')),
    label: first(d.path, d.route, it.name),
    sublabel: first(d.handler, d.controller, requestResponseSummary(d)),
    meta: requestResponseSummary(d),
  }), connections, 'exposure')
  const left = [
    ...splitRowsIntoGroups(
      httpRows,
      (row) => routePrefix(row.label),
      (prefix) => `HTTP ${prefix}`,
      (_prefix, rows) => `${rows.length} route${rows.length === 1 ? '' : 's'} grouped by path prefix`,
    ).map((g) => ({ ...g, key: `http_${g.key}`, side: 'exposure', lane: 'http_inbound' })),
    {
      key: 'rpc_inbound',
      side: 'exposure',
      lane: 'rpc_inbound',
      title: 'Inbound RPC',
      subtitle: 'gRPC/protobuf methods',
      items: makeRows(service.rpc_endpoints, (it, d) => ({
        badge: sourceBadge(first(metadataDetail(d, 'protocol'), d.protocol, d.platform, 'RPC')),
        label: first(d.service && d.method ? `${d.service}/${d.method}` : '', d.instance, metadataDetail(d, 'instance'), it.name),
        sublabel: first(metadataDetail(d, 'service'), d.service, it.summary),
      }), connections, 'exposure'),
    },
    {
      key: 'event_inbound',
      side: 'exposure',
      lane: 'event_inbound',
      title: 'Event consumers',
      subtitle: 'queues and stream sources',
      items: makeRows(service.queue_consumers, (it, d) => ({
        badge: itemBadge(it, 'QUEUE'),
        label: first(d.queue, d.queue_name, d.destination, d.stream_arn, d.source, it.name),
        sublabel: first(d.kind, d.type, it.summary),
      }), connections, 'exposure'),
    },
    {
      key: 'scheduled_jobs',
      side: 'exposure',
      lane: 'scheduled_jobs',
      title: 'Scheduled jobs',
      subtitle: 'time-based entrypoints',
      items: makeRows(service.scheduled_jobs, (it, d) => ({
        badge: 'CRON',
        label: first(d.k8s_cronjob_name, d.schedule, d.cron, it.name),
        sublabel: first(d.schedule, d.cron, it.summary),
      }), connections, 'exposure'),
    },
    {
      key: 'webhooks',
      side: 'exposure',
      lane: 'webhooks',
      title: 'Webhooks',
      subtitle: 'external callbacks',
      items: makeRows(service.webhooks, (it, d) => ({
        badge: 'HOOK',
        label: first(d.provider, d.source, d.path, it.name),
        sublabel: it.summary,
      }), connections, 'exposure'),
    },
    {
      key: 'cli_commands',
      side: 'exposure',
      lane: 'cli_commands',
      title: 'Commands',
      subtitle: 'manual and automation commands',
      items: makeRows(service.cli_commands, (it, d) => ({
        badge: 'CLI',
        label: first(d.command, it.name),
        sublabel: it.summary,
      }), connections, 'exposure'),
    },
  ].filter((g) => g.items.length)

  const right = [
    {
      key: 'http_outbound',
      side: 'dependency',
      lane: 'http_outbound',
      title: 'Outbound HTTP',
      subtitle: 'targets grouped by host/service',
      items: makeRows(depByClass.http_outbound, (it, d) => ({
        badge: sourceBadge(first(d.method, 'HTTP')),
        label: first(d.target_service, hostFromURL(d.target_url), hostFromURL(d.url), d.host, it.name),
        sublabel: first(d.url_template, d.path, d.endpoint, it.summary),
        meta: first(d.method, d.path),
      }), connections, 'dependency'),
    },
    {
      key: 'rpc_outbound',
      side: 'dependency',
      lane: 'rpc_outbound',
      title: 'Outbound RPC',
      subtitle: 'gRPC/protobuf targets grouped by service',
      items: makeRows(depByClass.rpc_outbound, (it, d) => ({
        badge: sourceBadge(first(d.protocol, d.platform, 'RPC')),
        label: first(d.target_service, d.target_ref, d.service, d.service_name, it.name),
        sublabel: first(d.method, it.summary),
      }), connections, 'dependency'),
    },
    {
      key: 'workflow_orchestration',
      side: 'dependency',
      lane: 'workflow_orchestration',
      title: 'Workflow orchestration',
      subtitle: 'workflow engines, topics, and callbacks',
      items: makeRows(depByClass.workflow_orchestration, (it, d) => ({
        badge: sourceBadge(first(d.orchestrator, d.platform, 'WORKFLOW')),
        label: first(d.target_service, hostFromURL(d.url_template), d.workflow_engine, d.engine, d.orchestrator, it.name),
        sublabel: first(d.topic, d.process_key, d.invocation_mode, it.summary),
        meta: first(d.invocation_mode, d.topic),
      }), connections, 'dependency'),
    },
    {
      key: 'db_operations',
      side: 'dependency',
      lane: 'db_operations',
      title: 'Database operations',
      subtitle: 'tables/resources with query operations',
      items: makeRows(depByClass.db_operations, (it, d) => ({
        badge: itemBadge(it, 'DB'),
        label: first(d.table_or_entity, d.table, d.entity, d.resource_name, d.database_name, it.name),
        sublabel: operationLabel(it),
        meta: first(d.operation, d.access, d.database_name),
      }), connections, 'dependency'),
    },
    {
      key: 'cache_operations',
      side: 'dependency',
      lane: 'cache_operations',
      title: 'Cache operations',
      subtitle: 'cache instances and key spaces',
      items: makeRows(depByClass.cache_operations, (it, d) => ({
        badge: itemBadge(it, 'CACHE'),
        label: first(d.cache_name, d.resource_name, d.key_pattern, it.name),
        sublabel: operationLabel(it),
      }), connections, 'dependency'),
    },
    {
      key: 'event_outbound',
      side: 'dependency',
      lane: 'event_outbound',
      title: 'Event publishers',
      subtitle: 'publish targets grouped by queue/topic',
      items: makeRows(depByClass.event_outbound, (it, d) => ({
        badge: itemBadge(it, 'QUEUE'),
        label: first(d.queue, d.queue_name, d.destination, d.topic, it.name),
        sublabel: first(d.kind, d.type, it.summary),
      }), connections, 'dependency'),
    },
    {
      key: 'other_dependencies',
      side: 'dependency',
      lane: 'other_dependencies',
      title: 'Other dependencies',
      subtitle: 'uncategorized downstream facts',
      items: makeRows(depByClass.other_dependencies, (it) => ({ badge: 'DEP', label: it.name, sublabel: it.summary }), connections, 'dependency'),
    },
  ].filter((g) => g.items.length)

  const measuredLeft = measureGroups(left, options.expandedGroups || new Set(), options.groupScrollOffsets || new Map(), service.name)
  const measuredRight = measureGroups(right, options.expandedGroups || new Set(), options.groupScrollOffsets || new Map(), service.name)
  const flow = buildFlowView(service, options.selectedObjectID || '')

  return layoutServiceModel({
    service,
    left: measuredLeft,
    right: measuredRight,
    leftSections: makeSections(measuredLeft, 'exposure', service.name, options.sectionScrollOffsets || new Map()),
    rightSections: makeSections(measuredRight, 'dependency', service.name, options.sectionScrollOffsets || new Map()),
    flow,
    width: 0,
    height: 0,
    edges: graphEdges.filter((e) => e.from === service.name || e.to === service.name),
  })
}

function requestResponseSummary(d) {
  const inputs = d.inputs || {}
  const inputCount = countArray(inputs.path_params) + countArray(inputs.query_params) + countArray(inputs.headers) + (inputs.body ? 1 : 0)
  const responseCount = countArray(d.responses)
  return [inputCount ? `${inputCount} inputs` : '', responseCount ? `${responseCount} responses` : ''].filter(Boolean).join(' · ')
}

function countArray(value) {
  return Array.isArray(value) ? value.length : 0
}

function buildFlowView(service, selectedObjectID) {
  const connections = service.connections || []
  if (!selectedObjectID) {
    return { mode: 'idle', selection: null, traces: [] }
  }
  const selectedKey = normalizeKey(selectedObjectID)
  const traces = connections.filter((conn) => {
    const candidates = [conn.from_id, conn.entrypoint_id, conn.from_name, conn.to_id, conn.to_name]
    return candidates.some((v) => {
      const key = normalizeKey(v)
      return key && (key === selectedKey || key.includes(selectedKey) || selectedKey.includes(key))
    })
  })
  return {
    mode: 'selected',
    selection: {
      selectedObjectID,
      traces,
      fallback: !traces.length && connections.length > 0,
    },
    traces,
  }
}

function groupForEdge(edge, role) {
  if (role === 'source') {
    if (edge.type === 'http') return 'http_outbound'
    if (edge.type === 'rpc') return 'rpc_outbound'
    if (edge.type === 'workflow') return 'workflow_orchestration'
    if (edge.type === 'database') return 'db_operations'
    if (edge.type === 'cache') return 'cache_operations'
    if (edge.type === 'queue_publish') return 'event_outbound'
    return 'other_dependencies'
  }
  if (edge.type === 'http') return 'http_inbound'
  if (edge.type === 'rpc') return 'rpc_inbound'
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
    if (!resourceID.startsWith('queue:') && !resourceID.startsWith('db:') && !resourceID.startsWith('resource:')) return
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

export function GraphCanvas({ graph, onSelect, detailLoaded = true, onRequestFullDetail }) {
  const svgRef = useRef(null)
  const transformRef = useRef(d3.zoomIdentity)
  const graphLayoutKeyRef = useRef('')
  const graphViewKeyRef = useRef('')
  const userMovedRef = useRef(false)
  const programmaticZoomRef = useRef(false)
  const [mode, setMode] = useState('overview')
  const [selectedObjectID, setSelectedObjectID] = useState('')
  const [expandedGroups, setExpandedGroups] = useState(() => new Set())
  const [groupScrollOffsets, setGroupScrollOffsets] = useState(() => new Map())
  const [sectionScrollOffsets, setSectionScrollOffsets] = useState(() => new Map())
  const [activeSelection, setActiveSelection] = useState(null)
  const [searchQuery, setSearchQuery] = useState('')
  const [teamFilter, setTeamFilter] = useState('')
  const [teamScope, setTeamScope] = useState('team')
  const renderKey = graphRenderKey(graph)
  const layoutKey = graphLayoutKey(graph)
  const expandedGroupsKey = Array.from(expandedGroups).sort().join('|')
  const groupScrollKey = Array.from(groupScrollOffsets.entries()).sort((a, b) => a[0].localeCompare(b[0])).map(([k, v]) => `${k}:${v}`).join('|')
  const sectionScrollKey = Array.from(sectionScrollOffsets.entries()).sort((a, b) => a[0].localeCompare(b[0])).map(([k, v]) => `${k}:${v}`).join('|')
  const changeMode = async (nextMode) => {
    if (nextMode === 'detail' && !detailLoaded && onRequestFullDetail) {
      try {
        await onRequestFullDetail()
      } catch {
        return
      }
    }
    setMode(nextMode)
  }
  const teamOptions = graphTeamOptions(graph)
  const runSearch = () => {
    const q = normalizeKey(searchQuery)
    if (!q) return
    const match = (graph?.services || []).find((svc) => normalizeKey(`${svc.name} ${svc.id || ''} ${svc.team || ''}`).includes(q))
    if (!match) return
    userMovedRef.current = false
    setTeamFilter(match.team || '')
    setTeamScope('connected')
    setActiveSelection({ kind: 'service', data: match, id: match.name })
    onSelect && onSelect({ kind: 'service', data: match, id: match.name })
  }

  useEffect(() => {
    if (!graph || !svgRef.current) return
    const svgEl = svgRef.current
    const svg = d3.select(svgEl)
    svg.selectAll('*').remove()

    const W = svgEl.clientWidth || 1200
    const H = svgEl.clientHeight || 800
    const view = graphScopedView(graph, teamFilter, teamScope)
    const services = view.services
    const edges = view.edges
    const largeGraph = services.length >= LARGE_GRAPH_SERVICE_THRESHOLD
    const clusteredGraph = largeGraph || Boolean(teamFilter)
    const serviceNames = new Set(services.map((s) => s.name))
    const visibleResourceIDs = sharedResourceMeta(graph, serviceNames)
    const displayEdges = edges
    const serviceModels = new Map(services.map((s) => [s.name, buildServiceModel(s, edges, { selectedObjectID, expandedGroups, groupScrollOffsets, sectionScrollOffsets })]))
    const top = new dagre.graphlib.Graph({ compound: false, multigraph: true })
    top.setGraph({ rankdir: 'LR', nodesep: 100, ranksep: 190, edgesep: 45, marginx: 90, marginy: 90 })
    top.setDefaultEdgeLabel(() => ({}))

    const nodeInfo = new Map()
    services.forEach((svc) => {
      const model = serviceModels.get(svc.name)
      top.setNode(svc.name, {
        width: COMPACT_SERVICE_W,
        height: COMPACT_SERVICE_H,
        type: 'service',
        data: svc,
        label: svc.name,
        model,
        expanded: false,
      })
      nodeInfo.set(svc.name, { type: 'service', data: svc, label: svc.name })
    })
    ;(view.external_nodes || []).forEach((n) => addTopNode(top, nodeInfo, n.name, 'external', n, cleanLabel(n.name)))
    const resources = normalizedGraphResources(view)
    if (resources.length) {
      resources.forEach((n) => {
        const id = resourceGraphID(n)
        addTopNode(top, nodeInfo, id, resourceNodeType(n), { ...n, shared: visibleResourceIDs.get(id) }, cleanLabel(n.name))
      })
    } else {
      ;(view.queue_nodes || []).forEach((n) => { const id = `queue:${n.id}`; addTopNode(top, nodeInfo, id, 'queue', { ...n, shared: visibleResourceIDs.get(id) }, cleanLabel(n.name)) })
      ;(view.database_nodes || []).forEach((n) => { const id = `db:${n.id}`; addTopNode(top, nodeInfo, id, 'db', { ...n, shared: visibleResourceIDs.get(id) }, cleanLabel(n.name)) })
      ;(view.scheduler_nodes || []).forEach((n) => { const id = `sched:${n.id}`; addTopNode(top, nodeInfo, id, 'scheduler', { ...n, shared: visibleResourceIDs.get(id) }, cleanLabel(n.name)) })
    }
    displayEdges.forEach((e, i) => {
      if (top.hasNode(e.from) && top.hasNode(e.to)) top.setEdge(e.from, e.to, { type: e.type, data: e }, `e${i}`)
    })

    const persistedLayout = layoutPositionMap(graph)
    if (clusteredGraph) {
      applyLargeGraphLayout(top, services, displayEdges, serviceNames)
    } else if (hasCompleteLayout(top, persistedLayout)) {
      applyPersistedLayout(top, persistedLayout)
    } else {
      dagre.layout(top)
    }
    if (mode === 'detail') {
      applyFullDetailServiceLayout(top, serviceModels, serviceNames)
    }

    const viewKey = `${layoutKey}|${clusteredGraph ? 'clustered' : 'dagre'}|${teamFilter}|${teamScope}`
    const previousLayoutKey = graphLayoutKeyRef.current
    const previousViewKey = graphViewKeyRef.current
    const preserveUserView = previousLayoutKey === layoutKey && previousViewKey === viewKey
    const rootG = svg.append('g')
    const zoom = d3.zoom().scaleExtent([0.035, 2.4]).on('zoom', (ev) => {
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
    services.forEach((svc) => {
      const name = svc.name
      const n = top.node(name)
      if (!n) return
      servicePorts.set(name, n.expanded ? computeServicePorts(n.model, n.x, n.y) : computeCompactServicePorts(n.x, n.y, n.width, n.height))
    })

    const frameGroup = rootG.append('g').attr('class', 'team-frames')
    drawTeamFrames(frameGroup, services, topPositions)

    const edgeGroup = rootG.append('g').attr('class', 'instance-edges')
    const renderedEdges = visualEdges(displayEdges)
    renderedEdges.forEach((edge) => {
      const from = endpointFor(edge.from, edge, 'source', topPositions, servicePorts, serviceNames)
      const to = endpointFor(edge.to, edge, 'target', topPositions, servicePorts, serviceNames)
      if (!from || !to) return
      const key = edgeKey(edge)
      const pts = pointsForCurve(from, to)
      const line = d3.line().x((p) => p.x).y((p) => p.y).curve(d3.curveBasis)
      edgeGroup.append('path')
        .attr('class', `edge type-${edge.type || ''}`)
        .attr('d', line(pts))
        .attr('data-from', edge.from)
        .attr('data-to', edge.to)
        .attr('data-type', edge.type || '')
        .attr('data-edge-key', key)
        .attr('data-count', edge.count || 1)
        .style('stroke-width', Math.min(4, 1.5 + Math.log2(edge.count || 1) * 0.55))
        .attr('marker-end', `url(#arr-${edge.type || 'http'})`)
        .on('click', (ev) => { ev.stopPropagation(); selectThing({ kind: 'edge', data: edge }) })
      const mid = pts[Math.floor(pts.length / 2)]
      edgeGroup.append('text')
        .attr('class', 'edge-connection-label')
        .attr('x', mid.x)
        .attr('y', mid.y - 9)
        .attr('text-anchor', 'middle')
        .attr('data-edge-key', key)
        .text(edgeLabelText(edge))
    })

    const nodeGroup = rootG.append('g').attr('class', 'instance-nodes')
    top.nodes().forEach((id) => {
      const n = top.node(id)
      if (!n) return
      if (n.type === 'service') {
        if (n.expanded) drawServiceNode(nodeGroup, n.model, n.x, n.y, onSelect, selectThing, scrollGroupRows, scrollSection)
        else drawCompactServiceNode(nodeGroup, n, selectThing)
      } else {
        drawResourceNode(nodeGroup, id, n, onSelect, selectThing)
      }
    })

    drawSelectedConnectionSummary(rootG.append('g').attr('class', 'selected-connections-overlay'), activeSelection, renderedEdges, topPositions, serviceNames, selectThing)

    function selectThing(sel) {
      setActiveSelection(sel)
      if (!sel) {
        setSelectedObjectID('')
      }
      if (sel?.kind === 'service') {
        setSelectedObjectID('')
      }
      if (sel?.kind === 'group' && sel.data?.service) {
        setExpandedGroups((prev) => {
          const next = new Set(prev)
          const key = groupStateKey(sel.data.service, sel.data.groupKey || sel.data.kind || sel.id)
          if (next.has(key)) next.delete(key)
          else next.add(key)
          return next
        })
      }
      if (sel?.kind === 'fact' && sel.data?.service) {
        const nextObjectID = sel.data.objectID || objectID(sel.data.items?.[0]) || sel.data.name || ''
        if (nextObjectID) setSelectedObjectID(nextObjectID)
      }
      applyVisualSelection(sel)
      onSelect && onSelect(sel)
    }

    function scrollGroupRows(stateKey, delta) {
      if (!stateKey || !delta) return
      setGroupScrollOffsets((prev) => {
        const next = new Map(prev)
        const current = Number(next.get(stateKey) || 0)
        next.set(stateKey, Math.max(0, current + delta))
        return next
      })
    }

    function scrollSection(stateKey, delta) {
      if (!stateKey || !delta) return
      setSectionScrollOffsets((prev) => {
        const next = new Map(prev)
        const current = Number(next.get(stateKey) || 0)
        next.set(stateKey, Math.max(0, current + delta))
        return next
      })
    }

    function applyVisualSelection(sel) {
      rootG.selectAll('[data-select-id]').classed('selected', false).classed('hl-dimmed', false).classed('flow-active', false)
      rootG.selectAll('.edge').classed('hl-active', false).classed('hl-dimmed', false)
      rootG.selectAll('.edge-connection-label').classed('hl-active', false)
      if (!sel) {
        return
      }
      const selectedID = sel.id || (sel.data && (sel.data.name || sel.data.id)) || sel.kind
      rootG.selectAll(`[data-select-id="${cssEscape(selectedID)}"]`).classed('selected', true)
      const flowKeys = selectionMatchKeys(sel)
      if (sel.kind === 'edge') {
        const selectedEdgeKey = edgeKey(sel.data || {})
        rootG.selectAll('.edge').each(function () {
          const el = d3.select(this)
          const active = el.attr('data-edge-key') === selectedEdgeKey
          if (active) {
            flowKeys.add(normalizeKey(el.attr('data-from')))
            flowKeys.add(normalizeKey(el.attr('data-to')))
          }
          el.classed(active ? 'hl-active' : 'hl-dimmed', true)
        })
        rootG.selectAll(`.edge-connection-label[data-edge-key="${cssEscape(selectedEdgeKey)}"]`).classed('hl-active', true)
      } else if (selectedID) {
        const impact = sel.kind === 'section' || sel.kind === 'group' || sel.kind === 'fact' ? impactEdgeSet(sel, displayEdges, serviceNames) : null
        rootG.selectAll('.edge').each(function () {
          const el = d3.select(this)
          const key = `${el.attr('data-from')}|${el.attr('data-to')}|${el.attr('data-type') || ''}`
          const active = impact ? impact.has(key) : el.attr('data-from') === selectedID || el.attr('data-to') === selectedID
          if (active) {
            flowKeys.add(normalizeKey(el.attr('data-from')))
            flowKeys.add(normalizeKey(el.attr('data-to')))
          }
          el.classed(active ? 'hl-active' : 'hl-dimmed', true)
          rootG.selectAll(`.edge-connection-label[data-edge-key="${cssEscape(key)}"]`).classed('hl-active', active)
        })
      }
      rootG.selectAll('.fact-chip,.objective-group,.objective-section,.service-system,.resource-node').each(function () {
        const el = d3.select(this)
        const hay = normalizeKey(`${el.attr('data-select-id') || ''} ${el.attr('data-match-keys') || ''}`)
        const active = Array.from(flowKeys).some((key) => key && hay.includes(key))
        el.classed('flow-active', active)
      })
    }

    applyVisualSelection(activeSelection)

    graphLayoutKeyRef.current = layoutKey
    graphViewKeyRef.current = viewKey
    programmaticZoomRef.current = true
    if (preserveUserView) {
      svg.call(zoom.transform, transformRef.current)
    } else {
      const graphW = top.graph().width || 1000
      const graphH = top.graph().height || 700
      const pad = 60
      const overlayTopPad = 320
      const availableW = Math.max(240, W - pad * 2)
      const availableH = Math.max(240, H - overlayTopPad - pad)
      const fitScale = Math.min(availableW / graphW, availableH / graphH, 1.05)
      const minScale = mode === 'detail' ? 0.04 : 0.12
      const scale = Math.min(Math.max(fitScale, minScale), 1.05)
      const tx = (W - graphW * scale) / 2
      const ty = overlayTopPad + (H - overlayTopPad - graphH * scale) / 2
      transformRef.current = d3.zoomIdentity.translate(tx, ty).scale(scale)
      userMovedRef.current = false
      svg.call(zoom.transform, transformRef.current)
    }
    programmaticZoomRef.current = false

    return () => {
      svg.on('.zoom', null)
    }
  }, [renderKey, mode, activeSelection, selectedObjectID, expandedGroupsKey, groupScrollKey, sectionScrollKey, teamFilter, teamScope])

  return (
    <div class="graph-canvas-shell">
      <div class="graph-mode-toolbar" aria-label="Graph detail mode">
        <button type="button" class={mode === 'overview' ? 'active' : ''} onClick={() => changeMode('overview')}>Overview</button>
        <button type="button" class={mode === 'detail' ? 'active' : ''} onClick={() => changeMode('detail')}>Full detail</button>
        <span class="graph-toolbar-divider" />
        <input
          class="graph-search"
          value={searchQuery}
          placeholder="Search service"
          onInput={(e) => setSearchQuery(e.currentTarget.value)}
          onKeyDown={(e) => { if (e.key === 'Enter') runSearch() }}
        />
        <button type="button" onClick={runSearch}>Focus</button>
        <select class="graph-team-select" value={teamFilter} onInput={(e) => setTeamFilter(e.currentTarget.value)}>
          <option value="">All teams</option>
          {teamOptions.map((team) => <option key={team} value={team}>{team}</option>)}
        </select>
        <select class="graph-scope-select" value={teamScope} disabled={!teamFilter} onInput={(e) => setTeamScope(e.currentTarget.value)}>
          <option value="team">Team only</option>
          <option value="connected">Team + connected</option>
        </select>
      </div>
      <svg ref={svgRef} class="graph-svg-full graph-svg-instance" />
    </div>
  )
}

function graphLayoutKey(graph) {
  if (!graph) return ''
  const services = (graph.services || []).map((s) => s.name).sort()
  const edges = (graph.edges || []).map((e) => `${e.from}>${e.to}:${e.type || ''}:${e.label || ''}`).sort()
  const resources = [
    ...(graph.resource_nodes || []).map((n) => resourceGraphID(n)),
    ...(graph.external_nodes || []).map((n) => `ext:${n.name}`),
    ...(graph.queue_nodes || []).map((n) => `queue:${n.id || n.name}`),
    ...(graph.database_nodes || []).map((n) => `db:${n.id || n.name}`),
    ...(graph.scheduler_nodes || []).map((n) => `sched:${n.id || n.name}`),
  ].sort()
  return JSON.stringify({
    run: graph.run_id || '',
    layout: graph.layout?.algorithm || '',
    seed: graph.layout?.seed || '',
    services,
    edges,
    resources,
  })
}

function normalizedGraphResources(graph) {
  return (graph?.resource_nodes || []).filter((n) => resourceGraphID(n))
}

function resourceGraphID(n) {
  return cleanLabel(n?.graph_id || (n?.id ? `resource:${n.id}` : ''))
}

function resourceNodeType(n) {
  const kind = cleanLabel(n?.kind || '').toLowerCase()
  const platform = cleanLabel(n?.platform || '').toLowerCase()
  if (kind === 'database') return 'db'
  if (kind === 'cache' || platform.includes('redis') || platform.includes('memcache')) return 'cache'
  if (kind === 'object_storage' || platform.includes('s3')) return 'object_storage'
  if (kind === 'queue_topic_stream' || kind === 'queue' || platform.includes('sqs') || platform.includes('sns') || platform.includes('kafka') || platform.includes('stream')) return 'queue'
  if (kind === 'scheduler') return 'scheduler'
  if (kind === 'workflow') return 'workflow'
  return 'external'
}

function graphTeamOptions(graph) {
  return Array.from(new Set((graph?.services || []).map((svc) => svc.team || 'default'))).sort((a, b) => a.localeCompare(b))
}

function graphScopedView(graph, teamFilter, teamScope) {
  if (!graph || !teamFilter) return graph || {}
  const allServices = graph.services || []
  const allEdges = graph.edges || []
  const allServiceNames = new Set(allServices.map((svc) => svc.name))
  const teamServices = new Set(allServices.filter((svc) => (svc.team || 'default') === teamFilter).map((svc) => svc.name))
  const visibleServices = new Set(teamServices)
  if (teamScope === 'connected') {
    allEdges.forEach((edge) => {
      const fromTeam = teamServices.has(edge.from)
      const toTeam = teamServices.has(edge.to)
      if (fromTeam && allServiceNames.has(edge.to)) visibleServices.add(edge.to)
      if (toTeam && allServiceNames.has(edge.from)) visibleServices.add(edge.from)
    })
  }
  const visibleEdges = allEdges.filter((edge) => {
    const fromSvc = allServiceNames.has(edge.from)
    const toSvc = allServiceNames.has(edge.to)
    if (fromSvc && toSvc) {
      if (teamScope === 'connected') return visibleServices.has(edge.from) && visibleServices.has(edge.to) && (teamServices.has(edge.from) || teamServices.has(edge.to))
      return teamServices.has(edge.from) && teamServices.has(edge.to)
    }
    return visibleServices.has(edge.from) || visibleServices.has(edge.to)
  })
  const visibleNodeIDs = new Set(visibleServices)
  visibleEdges.forEach((edge) => {
    visibleNodeIDs.add(edge.from)
    visibleNodeIDs.add(edge.to)
  })
  const hasNode = (id) => visibleNodeIDs.has(id)
  return {
    ...graph,
    services: allServices.filter((svc) => visibleServices.has(svc.name)),
    edges: visibleEdges,
    resource_nodes: (graph.resource_nodes || []).filter((n) => hasNode(resourceGraphID(n))),
    external_nodes: (graph.external_nodes || []).filter((n) => hasNode(n.name)),
    queue_nodes: (graph.queue_nodes || []).filter((n) => hasNode(`queue:${n.id}`)),
    database_nodes: (graph.database_nodes || []).filter((n) => hasNode(`db:${n.id}`)),
    scheduler_nodes: (graph.scheduler_nodes || []).filter((n) => hasNode(`sched:${n.id}`)),
  }
}

function graphRenderKey(graph) {
  if (!graph) return ''
  const services = (graph.services || []).map((s) => [
    s.name,
    s.team || '',
    s.diffmind_freshness || '',
    s.repo_metrics?.total_loc || 0,
    primarySourceLanguage(s.repo_metrics?.languages || []) || '',
  ]).sort()
  return JSON.stringify({ layout: graphLayoutKey(graph), services })
}

function layoutPositionMap(graph) {
  const nodes = graph?.layout?.nodes || []
  const out = new Map()
  nodes.forEach((n) => {
    if (n && n.id) out.set(n.id, n)
  })
  return out
}

function hasCompleteLayout(top, positions) {
  if (!positions || positions.size === 0) return false
  return top.nodes().every((id) => {
    const node = top.node(id)
    const pos = positions.get(id)
    if (!node || !pos) return false
    const staleWidth = Math.abs((Number(pos.width) || node.width) - node.width) > Math.max(80, node.width * 0.6)
    const staleHeight = Math.abs((Number(pos.height) || node.height) - node.height) > Math.max(60, node.height * 0.6)
    return !staleWidth && !staleHeight
  })
}

function applyPersistedLayout(top, positions) {
  let maxX = 0
  let maxY = 0
  top.nodes().forEach((id) => {
    const node = top.node(id)
    const pos = positions.get(id)
    if (!node || !pos) return
    node.x = Number(pos.x) || 0
    node.y = Number(pos.y) || 0
    maxX = Math.max(maxX, node.x + node.width / 2 + 120)
    maxY = Math.max(maxY, node.y + node.height / 2 + 120)
  })
  top.setGraph({ ...top.graph(), width: maxX || 1000, height: maxY || 700 })
}

function applyLargeGraphLayout(top, services, edges, serviceNames) {
  const serviceTeam = new Map()
  const serviceDegree = new Map()
  const teams = new Map()
  services.forEach((svc) => {
    const team = svc.team || 'default'
    serviceTeam.set(svc.name, team)
    serviceDegree.set(svc.name, 0)
    if (!teams.has(team)) teams.set(team, { name: team, services: [], degree: 0 })
    teams.get(team).services.push(svc.name)
  })

  const teamLinks = new Map()
  const serviceAffinity = new Map()
  const resourceTeamWeights = new Map()
  const resourceServiceLinks = new Map()
  const resourceEdgeTypes = new Map()
  edges.forEach((edge) => {
    const fromService = serviceNames.has(edge.from)
    const toService = serviceNames.has(edge.to)
    const weight = Math.max(1, Number(edge.count || 1))
    if (fromService) serviceDegree.set(edge.from, (serviceDegree.get(edge.from) || 0) + weight)
    if (toService) serviceDegree.set(edge.to, (serviceDegree.get(edge.to) || 0) + weight)
    if (fromService && toService) {
      const a = serviceTeam.get(edge.from)
      const b = serviceTeam.get(edge.to)
      if (a && b && a !== b) addTeamLink(teamLinks, a, b, weight)
      addPairWeight(serviceAffinity, edge.from, edge.to, weight)
      if (a && teams.has(a)) teams.get(a).degree += weight
      if (b && teams.has(b)) teams.get(b).degree += weight
      return
    }
    const resource = fromService && !toService ? edge.to : toService && !fromService ? edge.from : ''
    const team = fromService ? serviceTeam.get(edge.from) : toService ? serviceTeam.get(edge.to) : ''
    if (resource && team) {
      if (!resourceTeamWeights.has(resource)) resourceTeamWeights.set(resource, new Map())
      const weights = resourceTeamWeights.get(resource)
      weights.set(team, (weights.get(team) || 0) + weight)
      if (!resourceServiceLinks.has(resource)) resourceServiceLinks.set(resource, new Set())
      if (fromService) resourceServiceLinks.get(resource).add(edge.from)
      if (toService) resourceServiceLinks.get(resource).add(edge.to)
      if (!resourceEdgeTypes.has(resource)) resourceEdgeTypes.set(resource, new Set())
      resourceEdgeTypes.get(resource).add(edge.type || '')
      if (teams.has(team)) teams.get(team).degree += weight
    }
  })
  resourceServiceLinks.forEach((linkedSet) => {
    const linked = Array.from(linkedSet).filter((id) => serviceNames.has(id)).sort()
    for (let i = 0; i < linked.length; i += 1) {
      for (let j = i + 1; j < linked.length; j += 1) {
        addPairWeight(serviceAffinity, linked[i], linked[j], 2)
      }
    }
  })

  const resourcesByTeam = new Map()
  top.nodes().forEach((id) => {
    if (serviceNames.has(id)) return
    const team = primaryResourceTeam(id, resourceTeamWeights) || 'shared'
    const node = top.node(id)
    const linkedServices = Array.from(resourceServiceLinks.get(id) || [])
    const linkedTeams = new Set(linkedServices.map((svc) => serviceTeam.get(svc)).filter(Boolean))
    const edgeTypes = resourceEdgeTypes.get(id) || new Set()
    if (node && linkedServices.length === 1) node.ownerService = linkedServices[0]
    if (node) {
      node.linkedServices = linkedServices.sort()
      node.attachmentSide = attachmentSideForNode(id, node.type, edgeTypes)
    }
    if (node && linkedTeams.size === 1) node.teamFrame = Array.from(linkedTeams)[0]
    if (!resourcesByTeam.has(team)) resourcesByTeam.set(team, [])
    resourcesByTeam.get(team).push(id)
  })
  resourcesByTeam.forEach((ids) => ids.sort())
  if (resourcesByTeam.has('shared') && !teams.has('shared')) {
    teams.set('shared', { name: 'shared', services: [], degree: 0 })
  }

  const teamOrder = orderTeamsForLayout(Array.from(teams.values()), teamLinks)
  const blocks = teamOrder.map((team) => buildTeamLayoutBlock(top, team, resourcesByTeam.get(team.name) || [], serviceDegree, serviceAffinity))
  let cursorX = 180
  let cursorY = 150
  let rowH = 0
  let col = 0
  let maxX = 0
  let maxY = 0
  blocks.forEach((block) => {
    if (col >= TEAM_GRID_COLS) {
      cursorX = 180
      cursorY += rowH + TEAM_BLOCK_GAP_Y
      rowH = 0
      col = 0
    }
    placeTeamLayoutBlock(top, block, cursorX, cursorY)
    const actual = teamBlockBounds(top, block, 110)
    maxX = Math.max(maxX, actual.right)
    maxY = Math.max(maxY, actual.bottom)
    cursorX = actual.right + TEAM_BLOCK_GAP_X
    rowH = Math.max(rowH, actual.bottom - cursorY)
    col += 1
  })
  const bounds = graphNodeBounds(top)
  top.setGraph({ ...top.graph(), width: Math.max(maxX, bounds.maxX) + 240, height: Math.max(maxY, bounds.maxY) + 220 })
}

function addTeamLink(links, a, b, weight) {
  const key = a < b ? `${a}|${b}` : `${b}|${a}`
  links.set(key, (links.get(key) || 0) + weight)
}

function addPairWeight(links, a, b, weight) {
  if (!a || !b || a === b) return
  const key = a < b ? `${a}|${b}` : `${b}|${a}`
  links.set(key, (links.get(key) || 0) + weight)
}

function pairWeight(links, a, b) {
  if (!a || !b || a === b) return 0
  const key = a < b ? `${a}|${b}` : `${b}|${a}`
  return links.get(key) || 0
}

function teamLinkWeight(links, a, b) {
  if (!a || !b || a === b) return 0
  const key = a < b ? `${a}|${b}` : `${b}|${a}`
  return links.get(key) || 0
}

function primaryResourceTeam(resourceID, resourceTeamWeights) {
  const weights = resourceTeamWeights.get(resourceID)
  if (!weights || !weights.size) return ''
  return Array.from(weights.entries()).sort((a, b) => {
    if (b[1] !== a[1]) return b[1] - a[1]
    return a[0].localeCompare(b[0])
  })[0][0]
}

function attachmentSideForNode(id, type, edgeTypes) {
  if (type === 'scheduler' || id.startsWith('sched:')) return 'left'
  if (edgeTypes && (edgeTypes.has('scheduler') || edgeTypes.has('queue_consume'))) return 'left'
  return 'right'
}

function orderTeamsForLayout(teams, links) {
  const remaining = teams.slice().sort((a, b) => {
    if ((b.degree || 0) !== (a.degree || 0)) return (b.degree || 0) - (a.degree || 0)
    if (b.services.length !== a.services.length) return b.services.length - a.services.length
    return a.name.localeCompare(b.name)
  })
  const out = []
  while (remaining.length) {
    if (!out.length) {
      out.push(remaining.shift())
      continue
    }
    const placed = new Set(out.map((team) => team.name))
    let bestIndex = 0
    let bestScore = -1
    remaining.forEach((team, i) => {
      let score = 0
      placed.forEach((name) => { score += teamLinkWeight(links, team.name, name) })
      score += teamLinkWeight(links, team.name, out[out.length - 1].name) * 2
      score += (team.degree || 0) * 0.001
      if (score > bestScore || (score === bestScore && team.name < remaining[bestIndex].name)) {
        bestScore = score
        bestIndex = i
      }
    })
    out.push(remaining.splice(bestIndex, 1)[0])
  }
  return out
}

function orderServicesForTeam(ids, serviceDegree, serviceAffinity) {
  const remaining = ids.slice().sort((a, b) => {
    const ad = serviceDegree.get(a) || 0
    const bd = serviceDegree.get(b) || 0
    if (bd !== ad) return bd - ad
    return a.localeCompare(b)
  })
  const out = []
  while (remaining.length) {
    if (!out.length) {
      out.push(remaining.shift())
      continue
    }
    let bestIndex = 0
    let bestScore = -1
    remaining.forEach((candidate, index) => {
      const adjacency = out.reduce((sum, placed) => sum + pairWeight(serviceAffinity, candidate, placed), 0)
      const recent = pairWeight(serviceAffinity, candidate, out[out.length - 1]) * 2
      const score = adjacency + recent + (serviceDegree.get(candidate) || 0) * 0.001
      if (score > bestScore || (score === bestScore && candidate.localeCompare(remaining[bestIndex]) < 0)) {
        bestScore = score
        bestIndex = index
      }
    })
    out.push(remaining.splice(bestIndex, 1)[0])
  }
  return out
}

function buildTeamLayoutBlock(top, team, resources, serviceDegree, serviceAffinity) {
  const orderedServiceIDs = orderServicesForTeam(team.services, serviceDegree, serviceAffinity)
  const serviceIDs = orderedServiceIDs.slice().sort((a, b) => {
    const an = top.node(a)
    const bn = top.node(b)
    if (Boolean(bn?.expanded) !== Boolean(an?.expanded)) return bn?.expanded ? 1 : -1
    return orderedServiceIDs.indexOf(a) - orderedServiceIDs.indexOf(b)
  })
  const expandedIDs = serviceIDs.filter((id) => top.node(id)?.expanded)
  const expandedID = expandedIDs[0] || ''
  const compactIDs = expandedIDs.length ? serviceIDs.filter((id) => !expandedIDs.includes(id)) : serviceIDs
  const resourceIDs = resources.slice().sort((a, b) => {
    const ao = top.node(a)?.ownerService || ''
    const bo = top.node(b)?.ownerService || ''
    if (ao !== bo) return ao.localeCompare(bo)
    return a.localeCompare(b)
  })
  const compactCols = compactIDs.length ? clamp(Math.ceil(Math.sqrt(compactIDs.length * 1.25)), 2, 6) : 0
  const compactRows = compactCols ? Math.ceil(compactIDs.length / compactCols) : 0
  const compactCellW = COMPACT_SERVICE_W + TEAM_RIGHT_ATTACH_W + TEAM_SERVICE_GAP_X
  const compactCellH = COMPACT_SERVICE_H + TEAM_SERVICE_GAP_Y
  const expandedNodes = expandedIDs.map((id) => top.node(id)).filter(Boolean)
  const expandedW = expandedNodes.length ? Math.max(...expandedNodes.map((node) => node.width + TEAM_RIGHT_ATTACH_W + TEAM_SERVICE_GAP_X)) : 0
  const expandedH = expandedNodes.length ? expandedNodes.reduce((sum, node) => sum + node.height + TEAM_SERVICE_GAP_Y, 0) : 0
  const compactW = compactCols * compactCellW
  const compactH = compactRows * compactCellH
  const serviceGridW = expandedNodes.length ? Math.max(expandedW, compactW, COMPACT_SERVICE_W + 120) : Math.max(COMPACT_SERVICE_W + 120, compactW)
  const serviceGridH = expandedNodes.length ? Math.max(expandedH + compactH, COMPACT_SERVICE_H + 120) : Math.max(COMPACT_SERVICE_H + 120, compactH)
  const unattachedCount = resourceIDs.filter((id) => !(top.node(id)?.linkedServices || []).length).length
  const unattachedRows = Math.ceil(unattachedCount / 2)
  const unattachedH = unattachedRows * (118 + TEAM_RESOURCE_GAP_Y)
  const width = TEAM_LEFT_ATTACH_W + serviceGridW + 180
  const height = Math.max(serviceGridH, unattachedH, 220) + 150
  return {
    team,
    expandedID,
    expandedIDs,
    compactIDs,
    resourceIDs,
    compactCols,
    compactCellW,
    compactCellH,
    serviceGridW,
    width,
    height,
  }
}

function placeTeamLayoutBlock(top, block, x, y) {
  const headerPad = 78
  let compactX = x + TEAM_LEFT_ATTACH_W + 40
  let compactY = y + headerPad
  const expandedIDs = block.expandedIDs || []
  if (expandedIDs.length) {
    let cursorY = y + headerPad
    expandedIDs.forEach((id) => {
      const node = top.node(id)
      if (!node) return
      node.x = x + TEAM_LEFT_ATTACH_W + 40 + node.width / 2
      node.y = cursorY + node.height / 2
      cursorY += node.height + TEAM_SERVICE_GAP_Y
    })
    compactY = cursorY
  }
  block.compactIDs.forEach((id, i) => {
    const node = top.node(id)
    if (!node) return
    const col = block.compactCols ? i % block.compactCols : 0
    const row = block.compactCols ? Math.floor(i / block.compactCols) : 0
    node.x = compactX + col * block.compactCellW + node.width / 2
    node.y = compactY + row * block.compactCellH + node.height / 2
  })
  placeAttachedResources(top, block, x, y + headerPad)
  resolveTeamCollisions(top, block)
}

function placeAttachedResources(top, block, blockX, contentY) {
  const serviceIDs = new Set([...(block.expandedIDs || []), block.expandedID, ...block.compactIDs].filter(Boolean))
  const services = Array.from(serviceIDs).map((id) => ({ id, node: top.node(id) })).filter((item) => item.node)
  const serviceByID = new Map(services.map((item) => [item.id, item.node]))
  const ownerSlots = new Map()
  const unattached = []

  block.resourceIDs.forEach((id) => {
    const node = top.node(id)
    if (!node) return
    const linked = (node.linkedServices || []).filter((svc) => serviceByID.has(svc))
    if (!linked.length) {
      unattached.push(id)
      return
    }
    if (linked.length === 1) {
      placeSingleOwnerResource(node, linked[0], serviceByID.get(linked[0]), ownerSlots)
      return
    }
    placeSharedResource(node, linked.map((svc) => serviceByID.get(svc)).filter(Boolean))
  })

  const fallbackX = blockX + TEAM_LEFT_ATTACH_W + block.serviceGridW + 40
  unattached.sort().forEach((id, i) => {
    const node = top.node(id)
    if (!node) return
    const col = i % 2
    const row = Math.floor(i / 2)
    node.x = fallbackX + col * (320 + TEAM_RESOURCE_GAP_X) + node.width / 2
    node.y = contentY + row * (118 + TEAM_RESOURCE_GAP_Y) + node.height / 2
  })
}

function placeSingleOwnerResource(resourceNode, ownerID, ownerNode, ownerSlots) {
  const side = resourceNode.attachmentSide === 'left' ? 'left' : 'right'
  const key = `${ownerID}:${side}`
  const slot = ownerSlots.get(key) || 0
  ownerSlots.set(key, slot + 1)
  const offset = stackSlotOffset(slot, resourceNode.height + TEAM_ATTACH_GAP_Y)
  const sign = side === 'left' ? -1 : 1
  resourceNode.x = ownerNode.x + sign * (ownerNode.width / 2 + TEAM_ATTACH_GAP_X + resourceNode.width / 2)
  resourceNode.y = ownerNode.y + offset
}

function placeSharedResource(resourceNode, linkedNodes) {
  if (!linkedNodes.length) return
  const side = resourceNode.attachmentSide === 'left' ? 'left' : 'right'
  const minX = Math.min(...linkedNodes.map((node) => node.x - node.width / 2))
  const maxX = Math.max(...linkedNodes.map((node) => node.x + node.width / 2))
  const avgX = linkedNodes.reduce((sum, node) => sum + node.x, 0) / linkedNodes.length
  const avgY = linkedNodes.reduce((sum, node) => sum + node.y, 0) / linkedNodes.length
  const spreadX = maxX - minX
  if (side === 'left') {
    resourceNode.x = minX - TEAM_ATTACH_GAP_X - resourceNode.width / 2
  } else if (spreadX > resourceNode.width + 160) {
    resourceNode.x = avgX
  } else {
    resourceNode.x = maxX + TEAM_ATTACH_GAP_X + resourceNode.width / 2
  }
  resourceNode.y = avgY
}

function stackSlotOffset(slot, step) {
  if (slot === 0) return 0
  const ring = Math.ceil(slot / 2)
  return (slot % 2 === 1 ? 1 : -1) * ring * step
}

function resolveTeamCollisions(top, block) {
  const serviceIDs = new Set([...(block.expandedIDs || []), block.expandedID, ...block.compactIDs].filter(Boolean))
  const fixedBoxes = Array.from(serviceIDs)
    .map((id) => ({ id, node: top.node(id) }))
    .filter((item) => item.node)
    .map((item) => nodeBox(item.node, 26))
  const placed = fixedBoxes.slice()
  block.resourceIDs.slice().sort((a, b) => {
    const an = top.node(a)
    const bn = top.node(b)
    if ((an?.attachmentSide || '') !== (bn?.attachmentSide || '')) return (an?.attachmentSide || '').localeCompare(bn?.attachmentSide || '')
    if ((an?.linkedServices || []).join('|') !== (bn?.linkedServices || []).join('|')) return (an?.linkedServices || []).join('|').localeCompare((bn?.linkedServices || []).join('|'))
    if ((an?.y || 0) !== (bn?.y || 0)) return (an?.y || 0) - (bn?.y || 0)
    return a.localeCompare(b)
  }).forEach((id) => {
    const node = top.node(id)
    if (!node) return
    let guard = 0
    while (guard < 80) {
      const box = nodeBox(node, 18)
      const hit = placed.find((candidate) => boxesOverlap(box, candidate))
      if (!hit) break
      node.y = hit.bottom + node.height / 2 + TEAM_ATTACH_GAP_Y
      guard += 1
    }
    placed.push(nodeBox(node, 18))
  })
}

function applyFullDetailServiceLayout(top, serviceModels, serviceNames) {
  const overviewBounds = graphFullBounds(top)
  let widthRatio = 1
  let heightRatio = 1
  serviceNames.forEach((id) => {
    const node = top.node(id)
    const model = serviceModels.get(id)
    if (!node || !model) return
    node.width = model.width
    node.height = model.height
    node.model = model
    node.expanded = true
    widthRatio = Math.max(widthRatio, model.width / COMPACT_SERVICE_W)
    heightRatio = Math.max(heightRatio, model.height / COMPACT_SERVICE_H)
  })

  const scaleX = clamp(widthRatio + 0.45, 1, 10)
  const scaleY = clamp(heightRatio + 0.45, 1, 10)
  scaleGraphAroundOverviewOrigin(top, scaleX, scaleY, overviewBounds)
  updateGraphBoundsFromNodes(top)
}

function scaleGraphAroundOverviewOrigin(top, scaleX, scaleY, overviewBounds) {
  if (scaleX === 1 && scaleY === 1) return
  const bounds = overviewBounds || graphFullBounds(top)
  const originX = Number.isFinite(bounds.minX) ? bounds.minX : 0
  const originY = Number.isFinite(bounds.minY) ? bounds.minY : 0
  top.nodes().forEach((id) => {
    const node = top.node(id)
    if (!node) return
    node.x = originX + (node.x - originX) * scaleX
    node.y = originY + (node.y - originY) * scaleY
  })
}

function updateGraphBoundsFromNodes(top) {
  const bounds = graphFullBounds(top)
  if (!Number.isFinite(bounds.minX)) {
    top.setGraph({ ...top.graph(), width: 1000, height: 700 })
    return
  }
  top.setGraph({
    ...top.graph(),
    width: Math.max(1000, bounds.maxX + 240),
    height: Math.max(700, bounds.maxY + 220),
  })
}

function nodeBox(node, pad = 0) {
  return {
    left: node.x - node.width / 2 - pad,
    right: node.x + node.width / 2 + pad,
    top: node.y - node.height / 2 - pad,
    bottom: node.y + node.height / 2 + pad,
  }
}

function boxesOverlap(a, b) {
  return a.left < b.right && a.right > b.left && a.top < b.bottom && a.bottom > b.top
}

function graphNodeBounds(top) {
  const bounds = { maxX: 0, maxY: 0 }
  top.nodes().forEach((id) => {
    const node = top.node(id)
    if (!node) return
    bounds.maxX = Math.max(bounds.maxX, node.x + node.width / 2)
    bounds.maxY = Math.max(bounds.maxY, node.y + node.height / 2)
  })
  return bounds
}

function graphFullBounds(top) {
  const bounds = { minX: Infinity, minY: Infinity, maxX: -Infinity, maxY: -Infinity }
  top.nodes().forEach((id) => {
    const node = top.node(id)
    if (!node) return
    const box = nodeBox(node, 0)
    bounds.minX = Math.min(bounds.minX, box.left)
    bounds.minY = Math.min(bounds.minY, box.top)
    bounds.maxX = Math.max(bounds.maxX, box.right)
    bounds.maxY = Math.max(bounds.maxY, box.bottom)
  })
  return bounds
}

function teamBlockBounds(top, block, pad = 0) {
  const ids = [...(block.expandedIDs || []), block.expandedID, ...block.compactIDs, ...block.resourceIDs].filter(Boolean)
  const initial = {
    left: Infinity,
    top: Infinity,
    right: -Infinity,
    bottom: -Infinity,
  }
  const bounds = ids.reduce((acc, id) => {
    const node = top.node(id)
    if (!node) return acc
    const box = nodeBox(node, pad)
    acc.left = Math.min(acc.left, box.left)
    acc.top = Math.min(acc.top, box.top)
    acc.right = Math.max(acc.right, box.right)
    acc.bottom = Math.max(acc.bottom, box.bottom)
    return acc
  }, initial)
  if (!Number.isFinite(bounds.left)) {
    return { left: 0, top: 0, right: block.width, bottom: block.height }
  }
  return bounds
}

function addTopNode(top, nodeInfo, id, type, data, label) {
  const shared = data && data.shared
  const tableCount = Array.isArray(data?.tables) ? data.tables.length : 0
  const opCount = Number(data?.operation_count || 0)
  const resourceDetail = tableCount ? `${tableCount} tables ${opCount || ''}` : ''
  const widthBasis = Math.max(label.length, resourceDetail.length)
  const w = shared ? Math.max(230, Math.min(380, widthBasis * 8 + 132)) : Math.max(180, Math.min(330, widthBasis * 8 + 82))
  const h = shared ? 112 : type === 'db' && tableCount ? 98 : type === 'scheduler' ? 64 : 86
  top.setNode(id, { width: w, height: h, type, data, label })
  nodeInfo.set(id, { type, data, label })
}

function computeServicePorts(model, cx, cy) {
  const boundary = model.boundary || { x: -model.width / 2, y: -model.height / 2, width: model.width, height: model.height }
  const ports = {
    hullIn: { x: cx + boundary.x, y: cy },
    hullOut: { x: cx + boundary.x + boundary.width, y: cy },
    hullTop: { x: cx, y: cy + boundary.y },
    groups: {},
    items: [],
  }
  ;(model.leftSections || []).forEach((section) => assignSectionPort(ports, section, cx + section.x, cy + section.y, 'exposure'))
  ;(model.rightSections || []).forEach((section) => assignSectionPort(ports, section, cx + section.x, cy + section.y, 'dependency'))
  model.left.forEach((group) => assignGroupPorts(ports, group, cx + group.x, cy + group.y, 'exposure'))
  model.right.forEach((group) => assignGroupPorts(ports, group, cx + group.x, cy + group.y, 'dependency'))
  return ports
}

function computeCompactServicePorts(cx, cy, width, height) {
  return {
    hullIn: { x: cx - width / 2, y: cy },
    hullOut: { x: cx + width / 2, y: cy },
    hullTop: { x: cx, y: cy - height / 2 },
    groups: {},
    items: [],
  }
}

function assignGroupPorts(ports, group, cx, cy, side) {
  const portX = side === 'exposure' ? cx + group.width / 2 : side === 'dependency' ? cx - group.width / 2 : cx
  const portY = side === 'objective' ? cy + group.height / 2 : cy
  const port = { x: portX, y: portY, side, group }
  ports.groups[group.key] = port
  if (group.lane && !ports.groups[group.lane]) ports.groups[group.lane] = port
}

function assignSectionPort(ports, section, cx, cy, side) {
  const portX = side === 'exposure' ? cx + section.width / 2 : cx - section.width / 2
  const port = { x: portX, y: cy, side, section }
  ports.groups[section.key] = port
  if (section.lane) ports.groups[section.lane] = port
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

function impactEdgeSet(sel, edges, serviceNames = new Set()) {
  const out = new Set()
  const data = sel.data || {}
  const service = data.service || ''
  const kind = data.kind || ''
  const keys = new Set([normalizeKey(data.name), normalizeKey(data.kind)])
  ;(data.items || []).forEach((item) => {
    if (Array.isArray(item.items)) {
      item.items.forEach((raw) => addImpactKeys(keys, raw))
    } else {
      addImpactKeys(keys, item)
    }
  })
  edges.forEach((edge) => {
    if (!service) return
    const fromService = edge.from === service
    const toService = edge.to === service
    if (kind === 'http_inbound' || kind === 'rpc_inbound' || kind === 'event_inbound' || kind === 'scheduled_jobs' || kind === 'cli_commands' || kind === 'webhooks') {
      const traced = traceImpactEdgeSet(data, edges, service)
      if (traced.size) {
        traced.forEach((key) => out.add(key))
        expandKnownServiceImpact(out, edges, serviceNames)
      } else if (fromService || toService) {
        out.add(edgeKey(edge))
        expandKnownServiceImpact(out, edges, serviceNames)
      }
      return
    }
    if (!fromService && !toService) return
    const hay = edgeHaystack(edge)
    for (const key of keys) {
      if (key && hay.includes(key)) {
        out.add(edgeKey(edge))
        return
      }
    }
    if (kind === 'http_outbound' && edge.type === 'http' && fromService) out.add(edgeKey(edge))
    if (kind === 'rpc_outbound' && edge.type === 'rpc' && fromService) out.add(edgeKey(edge))
    if (kind === 'db_operations' && edge.type === 'database' && fromService) out.add(edgeKey(edge))
    if (kind === 'cache_operations' && edge.type === 'cache' && fromService) out.add(edgeKey(edge))
    if (kind === 'event_outbound' && edge.type === 'queue_publish' && fromService) out.add(edgeKey(edge))
  })
  return out
}

function selectionMatchKeys(sel) {
  const keys = new Set()
  const data = sel?.data || {}
  ;[sel?.id, data.name, data.kind, data.service, data.objectID].forEach((value) => keys.add(normalizeKey(value)))
  if (sel?.kind === 'edge') {
    ;[data.from, data.to, data.from_id, data.to_id, data.from_name, data.to_name, data.label, data.type].forEach((value) => keys.add(normalizeKey(value)))
  }
  ;(data.items || []).forEach((item) => {
    if (Array.isArray(item.items)) item.items.forEach((raw) => addImpactKeys(keys, raw))
    else addImpactKeys(keys, item)
  })
  ;(data.connections || []).forEach((conn) => {
    ;[
      conn.from_id,
      conn.from_name,
      conn.from_type,
      conn.to_id,
      conn.to_name,
      conn.to_type,
      conn.entrypoint_id,
      conn.summary,
    ].forEach((value) => keys.add(normalizeKey(value)))
  })
  return keys
}

function expandKnownServiceImpact(selected, edges, serviceNames) {
  if (!selected.size || !serviceNames.size) return
  const outgoing = new Map()
  edges.forEach((edge) => {
    if (!outgoing.has(edge.from)) outgoing.set(edge.from, [])
    outgoing.get(edge.from).push(edge)
  })
  const queue = []
  selected.forEach((key) => {
    const edge = edges.find((candidate) => edgeKey(candidate) === key)
    if (edge && serviceNames.has(edge.to)) queue.push(edge.to)
  })
  const seenServices = new Set(queue)
  while (queue.length) {
    const svc = queue.shift()
    ;(outgoing.get(svc) || []).forEach((edge) => {
      selected.add(edgeKey(edge))
      if (serviceNames.has(edge.to) && !seenServices.has(edge.to)) {
        seenServices.add(edge.to)
        queue.push(edge.to)
      }
    })
  }
}

function traceImpactEdgeSet(data, edges, service) {
  const out = new Set()
  const selectedKeys = new Set([normalizeKey(data.name)])
  ;(data.items || []).forEach((item) => {
    if (Array.isArray(item.items)) item.items.forEach((raw) => addImpactKeys(selectedKeys, raw))
    else addImpactKeys(selectedKeys, item)
  })
  const targetKeys = new Set()
  ;(data.connections || []).forEach((conn) => {
    const from = normalizeKey(conn.from_name || conn.from || conn.summary)
    const hit = Array.from(selectedKeys).some((key) => key && (from.includes(key) || key.includes(from)))
    if (!hit) return
    targetKeys.add(normalizeKey(conn.to_name || conn.to))
    targetKeys.add(normalizeKey(conn.summary))
  })
  if (!targetKeys.size) return out
  edges.forEach((edge) => {
    if (edge.from !== service && edge.to !== service) return
    const hay = edgeHaystack(edge)
    for (const key of targetKeys) {
      if (key && hay.includes(key)) {
        out.add(edgeKey(edge))
        return
      }
    }
  })
  return out
}

function edgeKey(edge) {
  return `${edge.from}|${edge.to}|${edge.type || ''}`
}

function edgeLabelText(edge) {
  const type = edgeTypeLabel(edge.type)
  const label = cleanLabel(edge.label || '')
  const count = Number(edge.count || 0)
  if (label && normalizeKey(label) !== normalizeKey(type)) return `${type} · ${shortLabel(label, 26)}`
  if (count > 1 && type) return `${type} · ${count}`
  return shortLabel(type || 'connection', 34)
}

function edgeTypeLabel(type) {
  const value = cleanLabel(type || '').toLowerCase()
  if (value === 'http') return 'HTTP'
  if (value === 'rpc') return 'RPC'
  if (value === 'workflow') return 'WORKFLOW'
  if (value === 'database') return 'DB'
  if (value === 'cache') return 'CACHE'
  if (value === 'queue_publish') return 'QUEUE OUT'
  if (value === 'queue_consume') return 'QUEUE IN'
  if (value === 'scheduler') return 'SCHEDULE'
  return cleanLabel(type || 'LINK').replace(/_/g, ' ').toUpperCase()
}

function selectedServiceName(sel) {
  if (!sel || sel.kind !== 'service') return ''
  return cleanLabel(sel.id || sel.data?.name || sel.data?.id || '')
}

function displayConnectionName(id) {
  return shortLabel(cleanLabel(id).replace(/^(db|queue|sched):/, ''), 34)
}

function connectionSummaryMeta(edge) {
  const count = Number(edge.count || 0)
  const detailCount = Array.isArray(edge.details) ? edge.details.length : 0
  const label = cleanLabel(edge.label || '')
  if (detailCount > 1) return `${detailCount} operations`
  if (count > 1) return `${count} links`
  if (label && normalizeKey(label) !== normalizeKey(edge.type || '')) return shortLabel(label, 36)
  return '1 link'
}

function drawSelectedConnectionSummary(parent, selection, edges, topPositions, serviceNames, selectThing) {
  const service = selectedServiceName(selection)
  if (!service || !serviceNames.has(service)) return
  const node = topPositions.get(service)
  if (!node) return
  const rows = (edges || [])
    .filter((edge) => edge.from === service || edge.to === service)
    .sort((a, b) => {
      const da = a.from === service ? 0 : 1
      const db = b.from === service ? 0 : 1
      if (da !== db) return da - db
      return edgeTypeLabel(a.type).localeCompare(edgeTypeLabel(b.type)) || displayConnectionName(a.from === service ? a.to : a.from).localeCompare(displayConnectionName(b.from === service ? b.to : b.from))
    })
  if (!rows.length) return

  const maxRowsPerColumn = 18
  const columnWidth = 430
  const columnGap = 14
  const columnCount = Math.max(1, Math.ceil(rows.length / maxRowsPerColumn))
  const width = columnCount * columnWidth + (columnCount - 1) * columnGap
  const rowH = 32
  const rowsPerColumn = Math.min(rows.length, maxRowsPerColumn)
  const height = 64 + rowsPerColumn * rowH + 10
  const x = node.x + node.width / 2 + 34
  const y = node.y - height / 2
  const g = parent.append('g').attr('class', 'connection-summary-card')
  g.append('rect').attr('x', x).attr('y', y).attr('width', width).attr('height', height).attr('rx', 10)
  drawText(g, 'Direct connections', x + 18, y + 24, 'connection-summary-title', 'start')
  drawText(g, service, x + width - 18, y + 24, 'connection-summary-service', 'end')
  drawText(g, 'Click a row to inspect operations and evidence', x + 18, y + 43, 'connection-summary-subtitle', 'start')

  rows.forEach((edge, index) => {
    const col = Math.floor(index / maxRowsPerColumn)
    const colX = x + col * (columnWidth + columnGap)
    const rowIndex = index % maxRowsPerColumn
    const outgoing = edge.from === service
    const other = outgoing ? edge.to : edge.from
    const rowY = y + 58 + rowIndex * rowH
    const row = g.append('g')
      .attr('class', 'connection-summary-row')
      .attr('tabindex', 0)
      .style('cursor', 'pointer')
      .on('click', (ev) => {
        ev.stopPropagation()
        selectThing?.({ kind: 'edge', data: edge, id: edgeKey(edge) })
      })
      .on('keydown', (ev) => {
        if (ev.key !== 'Enter' && ev.key !== ' ') return
        ev.preventDefault()
        selectThing?.({ kind: 'edge', data: edge, id: edgeKey(edge) })
      })
    row.append('title').text(`${edgeTypeLabel(edge.type)} ${outgoing ? 'from' : 'to'} ${service}: ${displayConnectionName(other)}. Click to inspect.`)
    row.append('rect').attr('x', colX + 12).attr('y', rowY).attr('width', columnWidth - 24).attr('height', rowH - 6).attr('rx', 6)
    row.append('rect').attr('class', `connection-summary-badge ${edge.type || ''}`).attr('x', colX + 20).attr('y', rowY + 6).attr('width', 72).attr('height', 16).attr('rx', 4)
    drawText(row, edgeTypeLabel(edge.type), colX + 56, rowY + 18, 'connection-summary-badge-text')
    drawText(row, outgoing ? 'OUT ->' : '<- IN', colX + 106, rowY + 18, `connection-summary-direction ${outgoing ? 'out' : 'in'}`, 'start')
    drawText(row, displayConnectionName(other), colX + 164, rowY + 18, 'connection-summary-target', 'start')
    drawText(row, connectionSummaryMeta(edge), colX + columnWidth - 24, rowY + 18, 'connection-summary-meta', 'end')
  })
}

function edgeHaystack(edge) {
  const parts = [edge.from, edge.to, edge.type, edge.label]
  ;(edge.details || []).forEach((detail) => {
    parts.push(detail?.name, detail?.summary)
    const d = detailsOf(detail)
    Object.values(d).forEach((value) => {
      if (typeof value === 'string') parts.push(value, hostFromURL(value))
      if (Array.isArray(value)) parts.push(...value.map((v) => String(v)))
    })
  })
  return normalizeKey(parts.filter(Boolean).join(' '))
}

function addImpactKeys(keys, item) {
  keys.add(normalizeKey(item?.name))
  keys.add(normalizeKey(item?.summary))
  const d = detailsOf(item)
  Object.values(d).forEach((value) => {
    if (typeof value === 'string') {
      keys.add(normalizeKey(value))
      keys.add(normalizeKey(hostFromURL(value)))
    }
    if (Array.isArray(value)) value.forEach((v) => keys.add(normalizeKey(String(v))))
  })
}

function drawCompactServiceNode(parent, n, selectThing) {
  const service = n.data || {}
  const x = n.x - n.width / 2
  const y = n.y - n.height / 2
  const componentType = normalizeKey(service.component_type || '').replace(/\s+/g, '-')
  const g = parent.append('g')
    .attr('class', `service-system service-compact${componentType ? ` component-${componentType}` : ''}`)
    .attr('data-select-id', service.name)
    .attr('data-match-keys', normalizeKey(`${service.name} ${service.id || ''}`))
    .on('click', (ev) => {
      ev.stopPropagation()
      selectThing({ kind: 'service', data: service, id: service.name })
    })

  g.append('rect')
    .attr('class', 'compact-service-card')
    .attr('x', x)
    .attr('y', y)
    .attr('width', n.width)
    .attr('height', n.height)
    .attr('rx', 12)
    .append('title').text(`${service.name} service. Select to inspect endpoints, dependencies, evidence, and flows.`)

  drawText(g, shortLabel(service.name, 31), n.x, y + 32, 'compact-service-name')
  drawText(g, serviceCompactSubtitle(service), n.x, y + 56, 'compact-service-subtitle')
  drawText(g, serviceCompactCounts(service), n.x, y + 83, 'compact-service-counts')

  const badges = compactServiceBadges(service)
  const totalW = badges.reduce((sum, b) => sum + Math.max(48, b.length * 7 + 20), 0) + Math.max(0, badges.length - 1) * 6
  let bx = n.x - totalW / 2
  badges.forEach((badge) => {
    const bw = Math.max(48, badge.length * 7 + 20)
    g.append('rect').attr('class', 'compact-service-badge').attr('x', bx).attr('y', y + 94).attr('width', bw).attr('height', 18).attr('rx', 9)
    drawText(g, shortLabel(badge, 18), bx + bw / 2, y + 107, 'compact-service-badge-text')
    bx += bw + 6
  })

  g.append('circle').attr('class', 'service-port exposure-port').attr('cx', x).attr('cy', n.y).attr('r', 8)
  g.append('circle').attr('class', 'service-port dependency-port').attr('cx', x + n.width).attr('cy', n.y).attr('r', 8)
  return g
}

function serviceCompactSubtitle(service) {
  const metrics = service.repo_metrics || {}
  const lang = primarySourceLanguage(metrics.languages || [])
  return [service.component_type, service.team || 'default', lang, service.diffmind_freshness || ''].filter(Boolean).join(' · ')
}

function serviceCompactCounts(service) {
  const routes = Number(service.entrypoint_count || 0) || (service.http_routes || []).length + (service.rpc_endpoints || []).length
  const rpc = 0
  const deps = Number(service.downstream_count || 0) || (service.dependencies || []).length
  const traces = Number(service.trace_count || 0) || (service.connections || []).length
  return `${routes + rpc} entrypoints · ${deps} downstream · ${traces} traces`
}

function compactServiceBadges(service) {
  const metrics = service.repo_metrics || {}
  const loc = metrics.total_loc ? `${Math.round(metrics.total_loc / 100) / 10}k LOC` : ''
  return [service.component_type, service.domain, service.criticality, loc].filter(Boolean).slice(0, 3)
}

function drawServiceNode(parent, model, cx, cy, onSelect, selectThing, scrollGroupRows, scrollSection) {
  const ports = computeServicePorts(model, cx, cy)
  const g = parent.append('g')
    .attr('class', 'service-system service-workspace')
    .attr('data-select-id', model.service.name)
    .attr('data-match-keys', normalizeKey(`${model.service.name} ${model.service.id || ''}`))
    .on('click', (ev) => {
      ev.stopPropagation()
      selectThing({ kind: 'service', data: model.service, id: model.service.name })
    })

  drawServiceBoundary(g, model, cx, cy)
  const connections = model.service.connections || []
  ;(model.leftSections || []).forEach((section) => drawSection(g, section, cx, cy, 'exposure', ports, selectThing, model.service.name, connections, scrollGroupRows, scrollSection))
  ;(model.rightSections || []).forEach((section) => drawSection(g, section, cx, cy, 'dependency', ports, selectThing, model.service.name, connections, scrollGroupRows, scrollSection))
  drawFlowPanel(g, model, cx + model.center.x, cy + model.center.y, selectThing)

  g.append('circle').attr('class', 'service-port exposure-port').attr('cx', ports.hullIn.x).attr('cy', ports.hullIn.y).attr('r', 10)
  g.append('circle').attr('class', 'service-port dependency-port').attr('cx', ports.hullOut.x).attr('cy', ports.hullOut.y).attr('r', 10)

  return g
}

function drawSection(g, section, serviceCX, serviceCY, kind, ports, selectThing, serviceName, connections, scrollGroupRows, scrollSection) {
  const colors = GROUP_COLORS[kind]
  const cx = serviceCX + section.x
  const cy = serviceCY + section.y
  const x = cx - section.width / 2
  const y = cy - section.height / 2
  const sg = g.append('g')
    .attr('class', `objective-section ${kind}-section`)
    .attr('data-select-id', `${serviceName}:${section.key}`)
    .attr('data-match-keys', [section.key, section.lane, section.title, ...section.groups.flatMap((group) => [group.key, group.title])].map(normalizeKey).join('|'))
    .on('click', (ev) => {
      ev.stopPropagation()
      selectThing({ kind: 'section', id: `${serviceName}:${section.key}`, data: { name: section.title, kind: section.lane, service: serviceName, count: section.itemCount, items: section.groups.flatMap((group) => group.items), connections } })
    })
    .on('wheel', (ev) => {
      if (!section.hiddenBefore && !section.hiddenAfter) return
      ev.preventDefault()
      ev.stopPropagation()
      scrollSection && scrollSection(section.stateKey, ev.deltaY > 0 ? 72 : -72)
    })

  sg.append('rect')
    .attr('class', 'section-card-bg')
    .attr('x', x)
    .attr('y', y)
    .attr('width', section.width)
    .attr('height', section.height)
    .attr('rx', 12)
    .attr('fill', colors.fill)
    .attr('stroke', colors.stroke)
    .append('title').text(`${section.title}: ${section.groupCount} groups, ${section.itemCount} objects`)
  sg.append('rect').attr('class', 'section-card-accent').attr('x', x).attr('y', y).attr('width', 6).attr('height', section.height).attr('rx', 3).attr('fill', colors.stroke)
  drawText(sg, section.title, x + 20, y + 25, 'section-title', 'start')
  drawText(sg, `${section.itemCount} object${section.itemCount === 1 ? '' : 's'}`, x + section.width - 18, y + 25, 'section-count', 'end')
  drawText(sg, section.subtitle, x + 20, y + 43, 'section-subtitle', 'start')

  const clipID = `clip-${groupKeyPart(serviceName)}-${groupKeyPart(section.key)}`
  const clip = sg.append('clipPath').attr('id', clipID)
  clip.append('rect')
    .attr('x', x + 12)
    .attr('y', y + WORKSPACE_SECTION_HEADER_H)
    .attr('width', section.width - 24)
    .attr('height', section.viewportH)
    .attr('rx', 8)

  const content = sg.append('g').attr('clip-path', `url(#${clipID})`)
  section.groups.forEach((group) => drawGroup(content, group, serviceCX + group.x, serviceCY + group.y, kind, ports, selectThing, serviceName, connections, scrollGroupRows))

  if (section.hiddenBefore || section.hiddenAfter) {
    const msg = `${section.hiddenBefore ? `↑ ${Math.ceil(section.hiddenBefore)}px ` : ''}${section.hiddenAfter ? `↓ ${Math.ceil(section.hiddenAfter)}px` : ''}`.trim()
    drawText(sg, msg || 'scroll', x + section.width / 2, y + section.height - 9, 'section-more')
  }

  const port = ports.groups[section.lane] || ports.groups[section.key]
  if (port) sg.append('circle').attr('class', `${kind}-port group-port`).attr('cx', port.x).attr('cy', port.y).attr('r', 11)
}

function drawServiceBoundary(g, model, cx, cy) {
  const boundary = model.boundary || { x: -model.width / 2, y: -model.height / 2, width: model.width, height: model.height }
  const x = cx + boundary.x
  const y = cy + boundary.y
  const w = boundary.width
  const h = boundary.height
  g.append('rect')
    .attr('class', 'service-boundary')
    .attr('x', x)
    .attr('y', y)
    .attr('width', w)
    .attr('height', h)
    .attr('rx', 18)
    .append('title').text(`${model.service.name} service boundary`)
  drawText(g, 'ENTRYPOINTS', x + 28, y + 30, 'service-lane-label', 'start')
  drawText(g, 'FLOW', x + w / 2, y + 30, 'service-lane-label', 'middle')
  drawText(g, 'DEPENDENCIES', x + w - 28, y + 30, 'service-lane-label', 'end')
  const leftSep = x + SERVICE_PAD_X + ENTRY_COL_W + WORKSPACE_COLUMN_GAP / 2
  const rightSep = x + w - SERVICE_PAD_X - DEP_COL_W - WORKSPACE_COLUMN_GAP / 2
  g.append('line').attr('class', 'service-column-separator').attr('x1', leftSep).attr('x2', leftSep).attr('y1', y + 50).attr('y2', y + h - 24)
  g.append('line').attr('class', 'service-column-separator').attr('x1', rightSep).attr('x2', rightSep).attr('y1', y + 50).attr('y2', y + h - 24)
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
  drawText(g, 'extracted service context', cx, cy + 23, 'service-caption')
  const counts = [
    (Number(service.entrypoint_count || 0) || ((service.http_routes || []).length + (service.rpc_endpoints || []).length)) && `${Number(service.entrypoint_count || 0) || ((service.http_routes || []).length + (service.rpc_endpoints || []).length)} entrypoints`,
    (Number(service.downstream_count || 0) || (service.dependencies || []).length) && `${Number(service.downstream_count || 0) || (service.dependencies || []).length} downstream`,
    (Number(service.trace_count || 0) || (service.connections || []).length) && `${Number(service.trace_count || 0) || (service.connections || []).length} traces`,
  ].filter(Boolean).join(' · ')
  if (counts) drawText(g, counts, cx, cy + 52, 'service-counts')
  drawServiceBadges(g, service, cx, cy + 78)
}

function drawServiceBadges(g, service, cx, y) {
  const metrics = service.repo_metrics || {}
  const lang = primarySourceLanguage(metrics.languages || [])
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

function drawTeamFrames(parent, services, topPositions) {
  const teams = new Map()
  const serviceByName = new Map((services || []).map((svc) => [svc.name, svc]))
  const extendTeam = (teamName, pos, isResource = false) => {
    if (!pos) return
    const team = teamName || 'default'
    const item = teams.get(team) || { name: team, minX: Infinity, minY: Infinity, maxX: -Infinity, maxY: -Infinity, count: 0, resources: 0, loc: 0, languages: new Map() }
    const w = pos.width || COMPACT_SERVICE_W
    const h = pos.height || COMPACT_SERVICE_H
    item.minX = Math.min(item.minX, pos.x - w / 2 - 54)
    item.maxX = Math.max(item.maxX, pos.x + w / 2 + 54)
    item.minY = Math.min(item.minY, pos.y - h / 2 - 82)
    item.maxY = Math.max(item.maxY, pos.y + h / 2 + 60)
    if (isResource) item.resources += 1
    else {
      item.count += 1
      addServiceSummaryToTeam(item, serviceByName.get(pos.data?.name || pos.label || ''))
    }
    teams.set(team, item)
  }
  services.forEach((svc) => {
    const pos = topPositions.get(svc.name)
    if (!pos) return
    extendTeam(svc.team || 'default', pos, false)
  })
  topPositions.forEach((pos) => {
    if (pos.type === 'service' || !pos.teamFrame) return
    extendTeam(pos.teamFrame, pos, true)
  })
  Array.from(teams.values()).forEach((team, i) => {
    if (!Number.isFinite(team.minX)) return
    const g = parent.append('g').attr('class', 'team-frame')
    const x = team.minX
    const y = team.minY
    const w = team.maxX - team.minX
    const h = team.maxY - team.minY
    g.append('rect').attr('class', `team-frame-bg tone-${i % 5}`).attr('x', x).attr('y', y).attr('width', w).attr('height', h).attr('rx', 24)
    const resourceText = team.resources ? ` · ${team.resources} resources` : ''
    g.append('text').attr('class', 'team-frame-label').attr('x', x + 24).attr('y', y + 34).text(`${team.name} · ${team.count} services${resourceText}`)
    g.append('text').attr('class', 'team-frame-summary').attr('x', x + 24).attr('y', y + 55).text(teamFrameSummary(team))
  })
}

function addServiceSummaryToTeam(team, service) {
  if (!service) return
  const metrics = service.repo_metrics || {}
  team.loc += Number(metrics.total_loc || 0)
  ;(metrics.languages || []).forEach((lang) => {
    const name = cleanLabel(lang.language || lang.name || '')
    if (!name) return
    const weight = languageDisplayWeight(name, Number(lang.loc || lang.lines || lang.total_loc || 1))
    if (weight <= 0) return
    team.languages.set(name, (team.languages.get(name) || 0) + weight)
  })
}

function teamFrameSummary(team) {
  const langs = Array.from(team.languages.entries()).sort((a, b) => {
    if (b[1] !== a[1]) return b[1] - a[1]
    return a[0].localeCompare(b[0])
  }).slice(0, 3).map(([name]) => name)
  return [langs.length ? langs.join(', ') : 'unknown language', team.loc ? formatLOC(team.loc) : 'unknown LOC'].join(' · ')
}

function formatLOC(loc) {
  const n = Number(loc || 0)
  if (!n) return ''
  if (n >= 1000000) return `${Math.round(n / 100000) / 10}M LOC`
  if (n >= 1000) return `${Math.round(n / 100) / 10}k LOC`
  return `${n} LOC`
}

function primarySourceLanguage(languages) {
  const ranked = (languages || [])
    .map((lang) => ({
      name: cleanLabel(lang.language || lang.name || ''),
      weight: languageDisplayWeight(lang.language || lang.name || '', Number(lang.loc || lang.lines || lang.total_loc || 0)),
    }))
    .filter((lang) => lang.name && lang.weight > 0)
    .sort((a, b) => {
      if (b.weight !== a.weight) return b.weight - a.weight
      return a.name.localeCompare(b.name)
    })
  if (ranked.length) return ranked[0].name
  const fallback = (languages || []).find((lang) => cleanLabel(lang.language || lang.name || ''))
  return fallback ? cleanLabel(fallback.language || fallback.name || '') : ''
}

function languageDisplayWeight(language, loc) {
  const name = cleanLabel(language).toLowerCase()
  const sourcePenalty = new Set(['json', 'yaml', 'yml', 'xml', 'toml', 'markdown', 'md', 'proto', 'sql'])
  if (!name) return 0
  if (sourcePenalty.has(name)) return Number(loc || 0) * 0.12
  return Number(loc || 0)
}

function drawFlowPanel(g, model, cx, cy, selectThing) {
  const service = model.service
  const panelW = model.center.width
  const panelH = model.center.height
  const x = cx - panelW / 2
  const y = cy - panelH / 2
  const panel = g.append('g').attr('class', 'flow-panel')
  panel.append('rect').attr('class', 'flow-panel-bg').attr('x', x).attr('y', y).attr('width', panelW).attr('height', panelH).attr('rx', 14)
  drawText(panel, shortLabel(service.name, 30), cx, y + 34, 'flow-service-name')
  drawText(panel, serviceCompactSubtitle(service) || 'service context', cx, y + 57, 'flow-service-subtitle')
  drawServiceBadges(panel, service, cx, y + 88)

  const flow = model.flow || { mode: 'idle', traces: [] }
  const startY = y + WORKSPACE_HEADER_H
  panel.append('line').attr('class', 'flow-panel-divider').attr('x1', x + 20).attr('x2', x + panelW - 20).attr('y1', startY - 16).attr('y2', startY - 16)
  if (flow.mode === 'idle') {
    drawFlowIdle(panel, service, x, startY, panelW)
  } else {
    drawSelectedFlow(panel, flow, x, startY, panelW, panelH - WORKSPACE_HEADER_H - 18, selectThing, service.name)
  }
}

function drawFlowIdle(g, service, x, y, width) {
  const stats = [
    ['Entry', (service.http_routes || []).length + (service.rpc_endpoints || []).length],
    ['Deps', (service.dependencies || []).length],
    ['Traces', (service.connections || []).length],
  ]
  let sx = x + 28
  stats.forEach(([label, value]) => {
    g.append('rect').attr('class', 'flow-stat').attr('x', sx).attr('y', y).attr('width', 112).attr('height', 58).attr('rx', 10)
    drawText(g, String(value || 0), sx + 56, y + 25, 'flow-stat-value')
    drawText(g, label, sx + 56, y + 45, 'flow-stat-label')
    sx += 124
  })
  drawText(g, 'Select a row from the left or right columns to show that object sequence here.', x + width / 2, y + 96, 'flow-empty-text')
  drawText(g, 'The selected objective stays focused until another objective is selected.', x + width / 2, y + 120, 'flow-empty-text')
}

function drawSelectedFlow(g, flow, x, y, width, height, selectThing, serviceName) {
  const selection = flow.selection
  const maxY = y + height
  drawText(g, 'Selected objective flow', x + 24, y + 6, 'flow-section-title', 'start')
  if (!selection) {
    drawText(g, 'No extracted sequence yet. Request/response and evidence are still available in the inspector.', x + width / 2, y + 72, 'flow-empty-text')
    return
  }
  let cursorY = y + 28
  drawText(g, shortLabel(selection.selectedObjectID, 54), x + 24, cursorY, 'flow-selected-object', 'start')
  cursorY += 16
  if (selection.fallback) {
    drawText(g, 'No exact trace matched this object yet.', x + width - 24, cursorY - 16, 'flow-empty-text', 'end')
  }
  const traces = (selection.fallback ? [] : selection.traces).slice(0, FLOW_TRACE_LIMIT)
  if (!traces.length) {
    drawText(g, 'No extracted sequence yet. Details remain available in the inspector.', x + 34, cursorY + 30, 'flow-empty-text', 'start')
    return
  }
  traces.forEach((trace) => {
    if (cursorY > maxY - 96) return
    drawSequenceTrace(g, trace, x + 24, cursorY, width - 48, selectThing, serviceName)
    cursorY += 112
  })
  const hidden = selection.traces.length - traces.length
  if (hidden > 0) {
    drawText(g, `+ ${hidden} more trace${hidden === 1 ? '' : 's'} available in the inspector data`, x + width / 2, Math.min(cursorY - 8, maxY - 8), 'flow-more')
  }
}

function drawTracePill(g, trace, x, y, width) {
  g.append('rect').attr('class', 'flow-trace-pill').attr('x', x).attr('y', y).attr('width', width).attr('height', 38).attr('rx', 8)
  drawText(g, first(trace.from_name, trace.from_id, 'entrypoint'), x + 14, y + 24, 'flow-trace-from', 'start')
  drawText(g, '→', x + width / 2, y + 24, 'flow-arrow')
  drawText(g, first(trace.to_name, trace.to_id, 'dependency'), x + width - 14, y + 24, 'flow-trace-to', 'end')
}

function drawSequenceTrace(g, trace, x, y, width, selectThing, serviceName) {
  const reach = first(trace.reachability, 'must')
  g.append('rect').attr('class', 'flow-sequence-card').attr('x', x).attr('y', y).attr('width', width).attr('height', 96).attr('rx', 10)
  const nodeY = y + 48
  const leftX = x + 34
  const rightX = x + width - 34
  const midX = x + width / 2
  g.append('circle').attr('class', 'flow-node entry').attr('cx', leftX).attr('cy', nodeY).attr('r', 8)
  g.append('circle').attr('class', 'flow-node action').attr('cx', rightX).attr('cy', nodeY).attr('r', 8)
  g.append('path').attr('class', `flow-seq-line reach-${reach}`).attr('d', `M ${leftX + 10} ${nodeY} C ${midX - 42} ${nodeY}, ${midX + 42} ${nodeY}, ${rightX - 10} ${nodeY}`)
  drawText(g, first(trace.from_name, trace.from_id), leftX, y + 22, 'flow-node-label', 'start')
  drawText(g, first(trace.from_type, trace.entrypoint_id), leftX, y + 38, 'flow-node-meta', 'start')
  drawText(g, first(trace.to_name, trace.to_id), rightX, y + 22, 'flow-node-label', 'end')
  drawText(g, first(trace.to_type, trace.to_id), rightX, y + 38, 'flow-node-meta', 'end')
  const cond = conditionLabel(trace.condition)
  drawText(g, cond || reach, midX, y + 43, cond ? 'flow-condition' : 'flow-reachability')
  const data = dataDependencyLabel(trace.data_dependencies)
  if (data) drawText(g, data, midX, y + 76, 'flow-data-label')
}

function conditionLabel(condition) {
  if (!condition) return ''
  return first(condition.summary, condition.expression, condition.kind)
}

function dataDependencyLabel(data) {
  if (!Array.isArray(data) || !data.length) return ''
  const firstDep = data[0]
  const from = first(firstDep.from?.expression, firstDep.from?.object_ref)
  const to = first(firstDep.to?.expression, firstDep.to?.object_ref)
  if (!from && !to) return `${data.length} data dependencies`
  return `${from || 'input'} → ${to || 'target'}`
}

function drawGroup(g, group, cx, cy, kind, ports, selectThing, serviceName, connections, scrollGroupRows) {
  const colors = GROUP_COLORS[kind]
  const gg = g.append('g')
    .attr('class', `objective-group ${kind}-group`)
    .attr('data-select-id', `${serviceName}:${group.key}:${group.title}`)
    .attr('data-match-keys', [group.key, group.title, group.lane, ...group.items.flatMap((item) => item.matchKeys || [])].map(normalizeKey).join('|'))
    .on('click', (ev) => {
      ev.stopPropagation()
      selectThing({ kind: 'group', id: `${serviceName}:${group.key}:${group.title}`, data: { name: group.title, kind: group.lane || group.key, groupKey: group.key, service: serviceName, count: group.items.length, items: group.items, connections } })
    })
    .on('wheel', (ev) => {
      if (!group.expanded || group.items.length <= group.visibleCount) return
      ev.preventDefault()
      ev.stopPropagation()
      const delta = ev.deltaY > 0 ? 1 : -1
      scrollGroupRows && scrollGroupRows(group.stateKey, delta)
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

  group.items.slice(group.scrollOffset, group.scrollOffset + group.visibleCount).forEach((item, i) => drawFactChip(gg, group, item, x + 18, y + 62 + i * WORKSPACE_ROW_H, group.width - 36, colors, selectThing, serviceName, connections))
  if (group.hidden) {
    const message = group.expanded
      ? `${group.hiddenBefore ? `↑ ${group.hiddenBefore} ` : ''}${group.hiddenAfter ? `↓ ${group.hiddenAfter}` : ''}`.trim() || 'showing all'
      : `open ${group.hidden} item${group.hidden === 1 ? '' : 's'}`
    drawText(gg, message, x + group.width / 2, y + group.height - 12, 'group-more')
  }

  const port = ports.groups[group.key]
  if (port) gg.append('circle').attr('class', `${kind}-port group-port`).attr('cx', port.x).attr('cy', port.y).attr('r', 11)
}

function drawFactChip(g, group, item, x, y, width, colors, selectThing, serviceName, connections) {
  const chip = g.append('g')
    .attr('class', 'instance-fact fact-chip')
    .attr('data-select-id', `${serviceName}:${group.key}:${item.key}`)
    .attr('data-match-keys', item.matchKeys.join('|'))
    .on('click', (ev) => {
      ev.stopPropagation()
      selectThing({ kind: 'fact', id: `${serviceName}:${group.key}:${item.key}`, data: { name: item.label, kind: group.key, service: serviceName, objectID: item.objectID, count: item.items.length, items: item.items, connections } })
    })
  chip.append('rect').attr('x', x).attr('y', y).attr('width', width).attr('height', 38).attr('rx', 6)
    .attr('fill', '#111827').attr('stroke', colors.inner)
    .append('title').text(`${item.label}${item.sublabel ? `: ${item.sublabel}` : ''}`)
  const badge = cleanLabel(item.badge || '')
  const badgeW = badge ? Math.max(30, Math.min(58, badge.length * 6 + 12)) : 0
  if (badge) {
    chip.append('rect').attr('class', 'fact-badge').attr('x', x + 5).attr('y', y + 7).attr('width', badgeW).attr('height', 14).attr('rx', 4).attr('fill', colors.inner)
    drawText(chip, badge, x + 5 + badgeW / 2, y + 18, 'fact-badge-text')
  } else {
    chip.append('circle').attr('cx', x + 11).attr('cy', y + 14).attr('r', 3.5).attr('fill', colors.inner)
  }
  const traceW = item.traceCount ? 34 : 0
  drawText(chip, shortLabel(item.label, 46), x + 10 + badgeW + 8, y + 17, 'fact-title', 'start')
  const metaLine = [item.meta, item.sublabel].filter(Boolean).join(' · ')
  if (metaLine) drawText(chip, shortLabel(metaLine, 58), x + 10 + badgeW + 8, y + 32, 'fact-meta', 'start')
  if (item.traceCount) {
    chip.append('rect').attr('class', 'fact-trace-badge').attr('x', x + width - 35).attr('y', y + 7).attr('width', 28).attr('height', 14).attr('rx', 4)
    drawText(chip, String(item.traceCount), x + width - 21, y + 18, 'fact-trace-text')
  }
}

function drawResourceNode(parent, id, n, onSelect, selectThing) {
  const colors = NODE_KINDS[n.type] || NODE_KINDS.external
  const shared = n.data && n.data.shared
  const g = parent.append('g')
    .attr('class', `resource-node resource-${n.type}${shared ? ' shared-resource' : ''}`)
    .attr('data-select-id', id)
    .attr('data-match-keys', normalizeKey(`${id} ${n.label || ''} ${n.data?.name || ''} ${n.data?.kind || ''}`))
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
  const footer = resourceFooter(n)
  if (footer) drawText(g, footer, n.x, n.y + 43, 'resource-footer')
}

function resourceSubtitle(n) {
  if (n.type === 'cache') {
    const opCount = Number(n.data?.operation_count || 0)
    return [cleanLabel(n.data?.platform || 'cache'), opCount ? `${opCount} operations` : ''].filter(Boolean).join(' · ')
  }
  if (n.type === 'object_storage') {
    const opCount = Number(n.data?.operation_count || 0)
    return [cleanLabel(n.data?.platform || 'object storage'), opCount ? `${opCount} operations` : ''].filter(Boolean).join(' · ')
  }
  if (n.type === 'db') {
    const tableCount = Array.isArray(n.data?.tables) ? n.data.tables.length : 0
    const opCount = Number(n.data?.operation_count || 0)
    if (tableCount && opCount) return `${cleanLabel(n.data?.kind || 'database')} · ${tableCount} tables`
    return cleanLabel(n.data?.kind || 'database')
  }
  if (n.type === 'queue') return cleanLabel(n.data?.kind || 'queue')
  if (n.type === 'scheduler') return cleanLabel(n.data?.schedule || 'scheduler')
  return cleanLabel(n.data?.kind || 'external')
}

function resourceFooter(n) {
  if (n.type !== 'db' && n.type !== 'cache' && n.type !== 'object_storage') return ''
  const opCount = Number(n.data?.operation_count || 0)
  if (!opCount) return ''
  return `${opCount} operation${opCount === 1 ? '' : 's'}`
}

function resourceBadge(n) {
  if (n.type === 'db') return sourceBadge(n.data?.platform || n.data?.kind || 'DB', 'DB')
  if (n.type === 'cache') return sourceBadge(n.data?.platform || 'CACHE', 'CACHE')
  if (n.type === 'object_storage') return sourceBadge(n.data?.platform || 'S3', 'S3')
  if (n.type === 'queue') return sourceBadge(n.data?.kind || 'QUEUE', 'QUEUE')
  if (n.type === 'scheduler') return 'CRON'
  return sourceBadge(n.data?.kind || 'API', 'API')
}

function cssEscape(s) {
  if (window.CSS && window.CSS.escape) return window.CSS.escape(s)
  return String(s).replace(/["\\]/g, '\\$&')
}
