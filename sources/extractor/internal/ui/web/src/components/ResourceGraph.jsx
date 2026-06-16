import { useEffect, useMemo, useState } from 'preact/hooks'
import { Button } from './ui/index.js'

const TYPE_LABEL = {
  http_route: 'HTTP routes',
  webhook: 'Webhooks',
  scheduled_job: 'Scheduled jobs',
  queue_consumer: 'Queue consumers',
  db_operation: 'DB operations',
  queue_publish: 'Queue publishes',
  outbound_http: 'HTTP clients',
  outbound_rpc: 'RPC clients',
  cache_operation: 'Cache operations',
}

function typeLabel(type) {
  return TYPE_LABEL[type] || String(type || 'unknown').replaceAll('_', ' ')
}

function groupBy(items, fn) {
  const out = new Map()
  for (const item of items || []) {
    const key = fn(item)
    if (!out.has(key)) out.set(key, [])
    out.get(key).push(item)
  }
  return [...out.entries()]
}

export function ResourceGraph({ graph, onDraft, busy }) {
  const [selected, setSelected] = useState(null)
  const exposures = graph?.exposures || []
  const dependencies = graph?.dependencies || []
  const resources = graph?.resources || []
  const connections = graph?.connections || []
  const service = graph?.service || firstService(exposures, dependencies) || 'Service'

  const dependencyByID = useMemo(() => new Map(dependencies.map((d) => [d.id, d])), [dependencies])
  const exposureByID = useMemo(() => new Map(exposures.map((e) => [e.id, e])), [exposures])
  const depsByResource = useMemo(() => groupBy(dependencies, (d) => d.resource_id || 'unassigned'), [dependencies])
  const exposureGroups = useMemo(() => groupBy(exposures, (e) => e.type || 'exposure'), [exposures])

  const resourceStats = useMemo(() => {
    const stats = new Map()
    for (const [resourceID, deps] of depsByResource) {
      const depIDs = new Set(deps.map((d) => d.id))
      const expIDs = new Set()
      for (const c of connections) {
        if (depIDs.has(c.to_dependency_id)) expIDs.add(c.from_exposure_id)
      }
      stats.set(resourceID, { deps, expCount: expIDs.size })
    }
    return stats
  }, [depsByResource, connections])

  const selectedData = selected && resolveSelection(selected, {
    resources,
    dependencies,
    exposures,
    connections,
    dependencyByID,
    exposureByID,
    resourceStats,
  })

  return (
    <div class="rg-workspace">
      <div class="rg-toolbar">
        <div>
          <div class="repo-section-kicker">Graph workspace</div>
          <h2>Service-centered architecture</h2>
          <p>Resources are collapsed by shared instance. Click a cluster or node to edit, then review the YAML draft before applying.</p>
        </div>
      </div>

      <div class="rg-canvas">
        <div class="rg-side rg-left">
          <div class="rg-side-title">Inbound</div>
          {exposureGroups.map(([type, items]) => (
            <div class="rg-exposure-group" key={type}>
              <div class="rg-cluster-label">{typeLabel(type)} · {items.length}</div>
              {items.slice(0, 8).map((item) => (
                <button
                  class="rg-node-card"
                  type="button"
                  onClick={() => setSelected({ kind: 'exposure', id: item.id })}
                  key={item.id}
                >
                  <span>{item.name}</span>
                  <small>{connectionCount(connections, item.id, 'from')} outgoing</small>
                </button>
              ))}
              {items.length > 8 && <div class="rg-more">+{items.length - 8} more</div>}
            </div>
          ))}
        </div>

        <div class="rg-center">
          <div class="rg-service-circle">
            <span>Service</span>
            <strong>{service}</strong>
            <small>{exposures.length} inbound · {dependencies.length} operations</small>
          </div>
        </div>

        <div class="rg-side rg-right">
          <div class="rg-side-title">Outbound resources</div>
          {resources.map((resource) => {
            const stat = resourceStats.get(resource.id) || { deps: [], expCount: 0 }
            return (
              <button
                class={'rg-resource-cluster' + (resource.derived ? ' derived' : '')}
                type="button"
                onClick={() => setSelected({ kind: 'resource', id: resource.id })}
                key={resource.id}
              >
                <span class="rg-resource-kind">{resource.kind || 'resource'}</span>
                <strong>{resource.name}</strong>
                <small>{resource.platform || 'unknown'}{resource.instance ? ` · ${resource.instance}` : ''}</small>
                <div class="rg-cluster-metrics">
                  <span>{stat.deps.length} operations</span>
                  <span>{stat.expCount} inbound callers</span>
                </div>
                <div class="rg-top-ops">
                  {stat.deps.slice(0, 3).map((d) => <span key={d.id}>{d.name}</span>)}
                </div>
              </button>
            )
          })}
        </div>
      </div>

      {selectedData && (
        <GraphEditorPanel
          selection={selectedData}
          resources={resources}
          connections={connections}
          dependencyByID={dependencyByID}
          exposureByID={exposureByID}
          onSelect={setSelected}
          onClose={() => setSelected(null)}
          onDraft={onDraft}
          busy={busy}
        />
      )}
    </div>
  )
}

function GraphEditorPanel({ selection, resources, connections, dependencyByID, exposureByID, onSelect, onClose, onDraft, busy }) {
  if (selection.kind === 'resource') {
    return <ResourceEditor selection={selection} onSelect={onSelect} onClose={onClose} onDraft={onDraft} busy={busy} />
  }
  if (selection.kind === 'dependency') {
    return <DependencyEditor selection={selection} resources={resources} onClose={onClose} onDraft={onDraft} busy={busy} />
  }
  return (
    <ExposureEditor
      selection={selection}
      connections={connections}
      dependencyByID={dependencyByID}
      exposureByID={exposureByID}
      onClose={onClose}
      onDraft={onDraft}
      busy={busy}
    />
  )
}

function ResourceEditor({ selection, onSelect, onClose, onDraft, busy }) {
  const r = selection.resource
  const [draft, setDraft] = useState(resourceDraft(r))
  useEffect(() => setDraft(resourceDraft(r)), [r.id])
  return (
    <aside class="rg-editor">
      <PanelHead title={r.name} kicker={r.derived ? 'Derived resource' : 'Resource'} onClose={onClose} />
      {r.derived && <div class="banner warn">Applying changes will promote this derived cluster into top-level YAML resources.</div>}
      <Field label="Name" value={draft.name} onInput={(name) => setDraft({ ...draft, name })} />
      <Field label="Kind" value={draft.kind} onInput={(kind) => setDraft({ ...draft, kind })} />
      <Field label="Platform" value={draft.platform} onInput={(platform) => setDraft({ ...draft, platform })} />
      <Field label="Instance" value={draft.instance} onInput={(instance) => setDraft({ ...draft, instance })} />
      <Field label="Summary" textarea value={draft.summary} onInput={(summary) => setDraft({ ...draft, summary })} />
      <div class="rg-editor-actions">
        <Button onClick={() => onDraft({ resources: [{ id: r.id, ...draft }] })} disabled={!!busy}>{busy ? 'Drafting…' : 'Preview draft'}</Button>
      </div>
      <div class="rg-editor-list">
        <h3>Operations</h3>
        {selection.dependencies.map((d) => (
          <button type="button" class="rg-list-row" onClick={() => onSelect({ kind: 'dependency', id: d.id })} key={d.id}>
            <span>{d.name}</span>
            <small>{typeLabel(d.type)}</small>
          </button>
        ))}
      </div>
    </aside>
  )
}

function DependencyEditor({ selection, resources, onClose, onDraft, busy }) {
  const d = selection.dependency
  const [draft, setDraft] = useState(entityDraft(d))
  useEffect(() => setDraft(entityDraft(d)), [d.id])
  return (
    <aside class="rg-editor">
      <PanelHead title={d.name} kicker="Dependency operation" onClose={onClose} />
      <Field label="Name" value={draft.name} onInput={(name) => setDraft({ ...draft, name })} />
      <Field label="Summary" textarea value={draft.summary} onInput={(summary) => setDraft({ ...draft, summary })} />
      <label class="ui-field">
        <span class="ui-field-label">Resource</span>
        <select value={draft.resource} onChange={(e) => setDraft({ ...draft, resource: e.target.value })}>
          {resources.map((r) => <option value={r.id} key={r.id}>{r.name}</option>)}
        </select>
      </label>
      <DetailsEditor details={draft.details} onChange={(details) => setDraft({ ...draft, details })} />
      <div class="rg-editor-actions">
        <Button onClick={() => onDraft({ dependencies: [{ id: d.id, ...draft }] })} disabled={!!busy}>{busy ? 'Drafting…' : 'Preview draft'}</Button>
      </div>
    </aside>
  )
}

function ExposureEditor({ selection, connections, dependencyByID, onClose, onDraft, busy }) {
  const e = selection.exposure
  const [draft, setDraft] = useState(entityDraft(e))
  useEffect(() => setDraft(entityDraft(e)), [e.id])
  const outgoing = connections.filter((c) => c.from_exposure_id === e.id)
  return (
    <aside class="rg-editor">
      <PanelHead title={e.name} kicker="Inbound exposure" onClose={onClose} />
      <Field label="Name" value={draft.name} onInput={(name) => setDraft({ ...draft, name })} />
      <Field label="Summary" textarea value={draft.summary} onInput={(summary) => setDraft({ ...draft, summary })} />
      <DetailsEditor details={draft.details} onChange={(details) => setDraft({ ...draft, details })} />
      <div class="rg-editor-actions">
        <Button onClick={() => onDraft({ exposures: [{ id: e.id, ...draft }] })} disabled={!!busy}>{busy ? 'Drafting…' : 'Preview draft'}</Button>
      </div>
      <div class="rg-editor-list">
        <h3>Connections</h3>
        {outgoing.map((c) => (
          <ConnectionRow
            conn={c}
            dependency={dependencyByID.get(c.to_dependency_id)}
            onDraft={onDraft}
            busy={busy}
            key={`${c.from_exposure_id}:${c.to_dependency_id}`}
          />
        ))}
      </div>
    </aside>
  )
}

function ConnectionRow({ conn, dependency, onDraft, busy }) {
  const [open, setOpen] = useState(false)
  const [condition, setCondition] = useState(conn.condition?.expression || '')
  const [summary, setSummary] = useState(conn.summary || '')
  return (
    <div class="rg-conn-edit">
      <button type="button" class="rg-list-row" onClick={() => setOpen(!open)}>
        <span>{dependency?.name || conn.to_dependency_id}</span>
        <small>{conn.condition?.kind || 'unconditional'}</small>
      </button>
      {open && (
        <div class="rg-conn-form">
          <Field label="Condition" value={condition} onInput={setCondition} />
          <Field label="Summary" value={summary} onInput={setSummary} />
          <Button
            size="tiny"
            onClick={() => onDraft({ connections: [{ from_id: conn.from_exposure_id, to_id: conn.to_dependency_id, condition, summary }] })}
            disabled={!!busy}
          >
            Preview connection draft
          </Button>
        </div>
      )}
    </div>
  )
}

function PanelHead({ kicker, title, onClose }) {
  return (
    <div class="rg-editor-head">
      <div>
        <div class="repo-section-kicker">{kicker}</div>
        <h2>{title}</h2>
      </div>
      <Button variant="secondary" size="tiny" onClick={onClose}>Close</Button>
    </div>
  )
}

function Field({ label, value, onInput, textarea = false }) {
  return (
    <label class="ui-field">
      <span class="ui-field-label">{label}</span>
      {textarea
        ? <textarea value={value || ''} onInput={(e) => onInput(e.target.value)} />
        : <input value={value || ''} onInput={(e) => onInput(e.target.value)} />}
    </label>
  )
}

function DetailsEditor({ details, onChange }) {
  const [raw, setRaw] = useState(JSON.stringify(details || {}, null, 2))
  useEffect(() => setRaw(JSON.stringify(details || {}, null, 2)), [details])
  const update = (next) => {
    setRaw(next)
    try {
      const parsed = JSON.parse(next || '{}')
      onChange(parsed)
    } catch {}
  }
  return (
    <label class="ui-field">
      <span class="ui-field-label">Details JSON</span>
      <textarea class="rg-details-json" value={raw} onInput={(e) => update(e.target.value)} />
    </label>
  )
}

function resolveSelection(selected, ctx) {
  if (selected.kind === 'resource') {
    const resource = ctx.resources.find((r) => r.id === selected.id)
    if (!resource) return null
    const dependencies = ctx.resourceStats.get(resource.id)?.deps || []
    return {
      kind: 'resource',
      resource,
      dependencies,
    }
  }
  if (selected.kind === 'dependency') {
    const dependency = ctx.dependencyByID.get(selected.id)
    return dependency ? { kind: 'dependency', dependency } : null
  }
  const exposure = ctx.exposureByID.get(selected.id)
  return exposure ? { kind: 'exposure', exposure } : null
}

function resourceDraft(r) {
  return {
    kind: r.kind || 'resource',
    platform: r.platform || '',
    name: r.name || '',
    instance: r.instance || '',
    summary: r.summary || '',
  }
}

function entityDraft(e) {
  return {
    name: e.name || '',
    summary: e.summary || '',
    resource: e.resource_id || e.details?.resource || '',
    details: e.details || {},
  }
}

function connectionCount(connections, id, side) {
  return connections.filter((c) => side === 'from' ? c.from_exposure_id === id : c.to_dependency_id === id).length
}

function firstService(exposures, dependencies) {
  return [...(exposures || []), ...(dependencies || [])].find((x) => x.service)?.service || ''
}
