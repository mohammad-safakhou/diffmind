import { navigate } from '../lib/router.js'
import { knowledgeRows } from '../lib/knowledge.js'

export function graphDetailTitle(sel) {
  const d = sel?.data || {}
  if (sel?.kind === 'edge') return `${d.from} -> ${d.to}`
  return d.name || sel?.id || sel?.kind || 'Details'
}

export function GraphDetailBody({ sel, pid, rid }) {
  const d = sel?.data || {}
  if (sel?.kind === 'service') return <ServiceDetail s={d} pid={pid} rid={rid} />
  if (sel?.kind === 'edge') return <EdgeDetail e={d} />
  if (sel?.kind === 'group' || sel?.kind === 'fact') return <GroupedFactDetail d={d} />
  if (sel?.kind === 'queue') return <ResourceDetail d={d} rows={[['Name', d.name], ['Kind', d.kind], ['FIFO', d.fifo ? 'yes' : 'no']]} />
  if (sel?.kind === 'db') return <ResourceDetail d={d} rows={[['Name', d.name], ['Kind', d.kind], ['Platform', d.platform || d.kind], ['Host', d.host || '-'], ['Tables', d.tables?.length || 0], ['Operations', d.operation_count || 0]]} />
  if (sel?.kind === 'cache') return <ResourceDetail d={d} rows={[['Name', d.name], ['Kind', d.kind || 'cache'], ['Platform', d.platform || '-'], ['Keyspaces', d.tables?.length || 0], ['Operations', d.operation_count || 0]]} />
  if (sel?.kind === 'object_storage') return <ResourceDetail d={d} rows={[['Name', d.name], ['Kind', d.kind || 'object_storage'], ['Platform', d.platform || 's3'], ['Buckets/objects', d.tables?.length || 0], ['Operations', d.operation_count || 0]]} />
  if (sel?.kind === 'workflow') return <ResourceDetail d={d} rows={[['Name', d.name], ['Kind', d.kind || 'workflow'], ['Platform', d.platform || '-'], ['Operations', d.operation_count || 0]]} />
  if (sel?.kind === 'scheduler') return <KV rows={[['Job', d.name], ['Service', d.service], ['Schedule', d.schedule || '-'], ['Profile', d.profile || '-']]} />
  return <KV rows={[['Name', d.name], ['Kind', d.kind || 'external']]} />
}

function ServiceDetail({ s, pid, rid }) {
  const list = (title, items) => {
    const arr = items || []
    if (!arr.length) return null
    return (
      <div class="detail-sec">
        <h4>{title} <span class="muted">({arr.length})</span></h4>
        {arr.slice(0, 80).map((it, i) => <ObjectCard key={i} item={typeof it === 'string' ? { name: it } : it} />)}
        {arr.length > 80 && <p class="muted small">Showing first 80 extracted objects. Use graph groups to inspect more focused slices.</p>}
      </div>
    )
  }
  return (
    <div>
      <KV rows={[
        ['Service', s.name],
        ['Team', s.team || 'default'],
        ['Repo', s.repo_path || '-'],
        ['Freshness', s.diffmind_freshness || 'unknown'],
      ]} />
      {list('HTTP inbound', s.http_routes)}
      {list('RPC inbound', s.rpc_endpoints)}
      {list('Queue consumers', s.queue_consumers)}
      {list('Scheduled jobs', s.scheduled_jobs)}
      {list('CLI commands', s.cli_commands)}
      {list('Dependencies', s.dependencies)}
      {s.connections && s.connections.length > 0 && (
        <div class="detail-sec">
          <h4>Object traces <span class="muted">({s.connections.length})</span></h4>
          {s.connections.slice(0, 120).map((c, i) => <TraceCard key={i} trace={c} service={s.name} pid={pid} rid={rid} />)}
          {s.connections.length > 120 && <p class="muted small">Showing first 120 traces.</p>}
        </div>
      )}
    </div>
  )
}

function TraceCard({ trace, service, pid, rid }) {
  const objectID = trace.flow_id || trace.entrypoint_id || trace.from_id
  const canTrace = pid && rid && service && objectID
  return (
    <div class="object-card trace-card">
      <KV rows={compactRows([
        ['From', trace.from_name || trace.from],
        ['To', trace.to_name || trace.to],
        ['Kind', trace.kind || trace.type],
        ['Reachability', trace.reachability],
        ['Confidence', trace.confidence],
      ])} compact />
      {trace.summary && <p class="object-summary">{trace.summary}</p>}
      {canTrace && (
        <button class="btn ghost tiny trace-link-btn" onClick={() => navigate(`/projects/${encodeURIComponent(pid)}/runs/${encodeURIComponent(rid)}/trace?service=${encodeURIComponent(service)}&object_id=${encodeURIComponent(objectID)}`)}>
          Trace full flow →
        </button>
      )}
      <DetailJSON title="Condition" value={trace.condition} />
      <DetailJSON title="Data dependencies" value={trace.data_dependencies} />
      <DetailJSON title="Side effects" value={trace.side_effects} />
      <DetailJSON title="Flow nodes" value={trace.nodes} />
      <DetailJSON title="Flow edges" value={trace.edges} />
    </div>
  )
}

function EdgeDetail({ e }) {
  const details = (e.details || []).filter(Boolean)
  return (
    <div>
      <KV rows={[['From', e.from], ['To', e.to], ['Type', e.type], ['Label', e.label || '-'], ['Facts', details.length]]} />
      {details.length > 0 && (
        <div class="detail-sec">
          <h4>Linked objects</h4>
          {details.map((item, i) => <ObjectCard key={i} item={item} />)}
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
      {shared?.services?.length > 0 && (
        <div class="detail-sec">
          <h4>Services</h4>
          <ul class="detail-list">{shared.services.map((s) => <li key={s}><code>{s}</code></li>)}</ul>
        </div>
      )}
      {d.services?.length > 0 && (
        <div class="detail-sec">
          <h4>Connected services</h4>
          <ul class="detail-list">{d.services.map((svc) => <li key={svc.name || svc}><code>{svc.name || svc}</code></li>)}</ul>
        </div>
      )}
      {d.facts?.length > 0 && (
        <div class="detail-sec">
          <h4>Extracted facts</h4>
          {d.facts.slice(0, 40).map((f, i) => (
            <ObjectCard key={i} item={{ ...(f.dep || {}), service: f.service }} />
          ))}
        </div>
      )}
      {d.tables?.length > 0 && (
        <div class="detail-sec">
          <h4>Tables and operations <span class="muted">({d.tables.length})</span></h4>
          {d.tables.map((table) => <TableCard key={table.name} table={table} />)}
        </div>
      )}
    </div>
  )
}

function TableCard({ table }) {
  const ops = table.operations || []
  return (
    <div class="object-card">
      <KV rows={[['Table', table.name], ['Kind', table.kind || '-'], ['Operations', ops.length]]} compact />
      {ops.length > 0 && (
        <div class="detail-sec tight">
          {ops.map((item, i) => <ObjectCard key={i} item={item} />)}
        </div>
      )}
    </div>
  )
}

function GroupedFactDetail({ d }) {
  const groups = d.items || []
  const rawItems = []
  groups.forEach((item) => {
    if (Array.isArray(item.items)) rawItems.push(...item.items)
    else rawItems.push(item)
  })
  return (
    <div>
      <KV rows={[['Name', d.name], ['Group', d.kind], ['Extracted instances', d.count || rawItems.length || 0]]} />
      <TraceViewDetail trace={d.trace_view} />
      {rawItems.length > 0 && (
        <div class="detail-sec">
          <h4>Instances</h4>
          {rawItems.map((item, i) => <ObjectCard key={i} item={item} />)}
        </div>
      )}
    </div>
  )
}

function TraceViewDetail({ trace }) {
  if (!trace) {
    return (
      <div class="detail-sec trace-detail-empty">
        <h4>Trace and data dependencies</h4>
        <p class="muted small">Select an exact objective row to load its extracted trace.</p>
      </div>
    )
  }
  const deps = trace.data_dependencies || []
  const segments = trace.segments || []
  const continuations = trace.continuations || []
  const relatedEdges = trace.related_edges || []
  return (
    <div class="detail-sec trace-detail">
      <h4>Trace and data dependencies</h4>
      <KV rows={compactRows([
        ['Status', trace.status],
        ['Service', trace.service],
        ['Object', trace.object_id],
        ['Segments', segments.length],
        ['Data dependencies', deps.length],
        ['Continuations', continuations.length],
      ])} compact />
      {trace.quality?.length > 0 && (
        <div class="trace-quality">
          {trace.quality.map((q) => <span key={q}>{q}</span>)}
        </div>
      )}
      {deps.length > 0 ? (
        <div class="trace-data-deps">
          {deps.map((dep, i) => <DataDependencyCard dep={dep} key={dep.id || i} />)}
        </div>
      ) : (
        <p class="muted small">No field-level data dependency was extracted for this objective in the selected DiffMind run.</p>
      )}
      <DetailJSON title="Trace segments" value={segments} />
      <DetailJSON title="Continuations" value={continuations} />
      <DetailJSON title="Related graph edges" value={relatedEdges} />
    </div>
  )
}

function DataDependencyCard({ dep }) {
  const from = dep?.from || {}
  const to = dep?.to || {}
  return (
    <div class="object-card data-dependency-card">
      <KV rows={compactRows([
        ['Kind', dep.kind],
        ['Confidence', dep.confidence],
        ['From object', from.object_ref],
        ['From field', from.expression],
        ['To object', to.object_ref],
        ['To field', to.expression],
      ])} compact />
      <DetailJSON title="Transforms" value={dep.transforms} />
      <DetailJSON title="Sanitization" value={dep.sanitization} />
    </div>
  )
}

function ObjectCard({ item }) {
  const details = item?.details || {}
  const rows = compactRows([
    ['ID', item?.id || details.id],
    ['Name', item?.name],
    ['Service', item?.service],
    ['Kind', item?.kind || details.kind],
    ['Method', details.method],
    ['Path', details.path],
    ['URL template', details.url_template],
    ['Platform', details.platform || details.engine],
    ['Operation', details.operation],
    ['Access', details.access],
    ['Database', details.database || details.database_name],
    ['Table(s)', arrayOrString(details.target?.tables) || details.table || details.table_or_entity],
    ['Queue/topic', details.queue || details.topic || details.destination],
    ['Cache', details.cache || details.cache_name || details.target?.cache],
    ['Confidence', details.confidence ?? details.detection_confidence],
    ['Origin', details.origin],
    ...knowledgeRows(details),
  ])
  return (
    <div class="object-card">
      <KV rows={rows.length ? rows : [['Name', item?.name || '-']]} compact />
      {item?.summary && <p class="object-summary">{item.summary}</p>}
      <DetailJSON title="Inputs" value={details.inputs} />
      <DetailJSON title="Responses" value={details.responses} />
      <DetailJSON title="Target" value={details.target} />
      <DetailJSON title="Query" value={details.query} />
      <DetailJSON title="Columns" value={details.columns} />
      <DetailJSON title="ORM" value={details.orm} />
      <DetailRefs title="Observations" refs={details.observations} />
      <DetailRefs title="Evidence refs" refs={details.evidence_refs} />
      <DetailJSON title="Source evidence" value={details.evidence} />
    </div>
  )
}

function DetailJSON({ title, value }) {
  if (value == null || value === '' || (Array.isArray(value) && value.length === 0)) return null
  return (
    <details class="object-json">
      <summary>{title}</summary>
      <pre>{formatValue(value)}</pre>
    </details>
  )
}

function DetailRefs({ title, refs }) {
  if (!Array.isArray(refs) || refs.length === 0) return null
  return (
    <div class="object-refs">
      <span>{title}</span>
      {refs.map((ref) => <code key={ref}>{ref}</code>)}
    </div>
  )
}

function KV({ rows, compact = false }) {
  return (
    <div class={compact ? 'kv-list compact' : 'kv-list'}>
      {rows.map(([k, v]) => <div class="kv-row" key={k}><span>{k}</span><code>{formatInline(v)}</code></div>)}
    </div>
  )
}

function compactRows(rows) {
  return rows.filter(([, v]) => v !== undefined && v !== null && String(v).trim() !== '')
}

function arrayOrString(value) {
  if (Array.isArray(value)) return value.join(', ')
  return value
}

function formatInline(value) {
  if (value === undefined || value === null || value === '') return '-'
  if (Array.isArray(value)) return value.join(', ')
  if (typeof value === 'object') return JSON.stringify(value)
  return String(value)
}

function formatValue(value) {
  if (typeof value === 'string') return value
  return JSON.stringify(value, null, 2)
}
