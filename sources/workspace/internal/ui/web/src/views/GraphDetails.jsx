export function graphDetailTitle(sel) {
  const d = sel?.data || {}
  if (sel?.kind === 'edge') return `${d.from} -> ${d.to}`
  return d.name || sel?.id || sel?.kind || 'Details'
}

export function GraphDetailBody({ sel }) {
  const d = sel?.data || {}
  if (sel?.kind === 'service') return <ServiceDetail s={d} />
  if (sel?.kind === 'edge') return <EdgeDetail e={d} />
  if (sel?.kind === 'group' || sel?.kind === 'fact') return <GroupedFactDetail d={d} />
  if (sel?.kind === 'queue') return <ResourceDetail d={d} rows={[['Name', d.name], ['Kind', d.kind], ['FIFO', d.fifo ? 'yes' : 'no']]} />
  if (sel?.kind === 'db') return <ResourceDetail d={d} rows={[['Name', d.name], ['Kind', d.kind], ['Host', d.host || '-']]} />
  if (sel?.kind === 'scheduler') return <KV rows={[['Job', d.name], ['Service', d.service], ['Schedule', d.schedule || '-'], ['Profile', d.profile || '-']]} />
  return <KV rows={[['Name', d.name], ['Kind', d.kind || 'external']]} />
}

function ServiceDetail({ s }) {
  const list = (title, items) => {
    const arr = items || []
    if (!arr.length) return null
    return (
      <div class="detail-sec">
        <h4>{title} <span class="muted">({arr.length})</span></h4>
        <ul class="detail-list">
          {arr.map((it, i) => (
            <li key={i}>
              <button class="detail-link" type="button">
                {typeof it === 'string' ? it : (it.name || it.summary || '-')}
              </button>
            </li>
          ))}
        </ul>
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
      {list('Queue consumers', s.queue_consumers)}
      {list('Scheduled jobs', s.scheduled_jobs)}
      {list('CLI commands', s.cli_commands)}
      {list('Dependencies', s.dependencies)}
      {s.connections && s.connections.length > 0 && (
        <div class="detail-sec">
          <h4>Object traces <span class="muted">({s.connections.length})</span></h4>
          <ul class="detail-list">
            {s.connections.map((c, i) => <li key={i}><code>{c.from_name}</code>{' -> '}<code>{c.to_name}</code> {c.summary && <span class="muted small">{c.summary}</span>}</li>)}
          </ul>
        </div>
      )}
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
      {d.facts?.length > 0 && (
        <div class="detail-sec">
          <h4>Extracted facts</h4>
          {d.facts.slice(0, 40).map((f, i) => (
            <ObjectCard key={i} item={{ ...(f.dep || {}), service: f.service }} />
          ))}
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
      {rawItems.length > 0 && (
        <div class="detail-sec">
          <h4>Instances</h4>
          {rawItems.map((item, i) => <ObjectCard key={i} item={item} />)}
        </div>
      )}
    </div>
  )
}

function ObjectCard({ item }) {
  const details = item?.details || {}
  const rows = compactRows([
    ['Name', item?.name],
    ['Service', item?.service],
    ['Kind', details.kind],
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
    ['Confidence', details.confidence],
    ['Origin', details.origin],
  ])
  return (
    <div class="object-card">
      <KV rows={rows.length ? rows : [['Name', item?.name || '-']]} compact />
      {item?.summary && <p class="object-summary">{item.summary}</p>}
      <DetailJSON title="Inputs" value={details.inputs} />
      <DetailJSON title="Responses" value={details.responses} />
      <DetailJSON title="Target" value={details.target} />
      <DetailJSON title="Query" value={details.query} />
      <DetailJSON title="ORM" value={details.orm} />
      <DetailJSON title="Columns" value={details.columns} />
      <DetailRefs title="Observations" refs={details.observations} />
      <DetailRefs title="Evidence refs" refs={details.evidence_refs} />
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
