import { useEffect, useRef } from 'preact/hooks'
import cytoscape from 'cytoscape'
import { effect } from '@preact/signals'
import { jobs, stages, selection, runMeta } from '../lib/store.js'

// LiveGraph renders the run as a DAG. Stage nodes are persistent; job
// nodes are added/updated as events arrive. Click → selects a node, which
// the DetailDrawer subscribes to.
export function LiveGraph() {
  const ref = useRef(null)

  useEffect(() => {
    const cy = cytoscape({
      container: ref.current,
      style: STYLE,
      layout: { name: 'preset' },
      wheelSensitivity: 0.2,
      minZoom: 0.3,
      maxZoom: 2.5,
    })

    seedStages(cy)

    cy.on('tap', 'node', (ev) => {
      const node = ev.target
      const id = node.id()
      const data = node.data()
      if (data.kind === 'stage') {
        selection.value = { type: 'stage', id }
      } else {
        selection.value = { type: 'job', id }
      }
    })

    const stop = effect(() => {
      // re-render whenever jobs or stages change.
      const j = jobs.value
      const st = stages.value
      runMeta.value // subscribe so layout reruns on new run
      syncGraph(cy, st, j)
    })

    const ro = new ResizeObserver(() => cy.resize())
    if (ref.current) ro.observe(ref.current)

    return () => {
      stop()
      ro.disconnect()
      cy.destroy()
    }
  }, [])

  return (
    <div style="position: absolute; inset: 0; display: flex; flex-direction: column;">
      <div style="flex: 1 1 auto; position: relative; min-height: 0;">
        <div ref={ref} id="cy" />
        <div class="graph-overlay">
          <span><span class="swatch" style="background: var(--pending)" /> pending</span>
          <span><span class="swatch" style="background: var(--running)" /> running</span>
          <span><span class="swatch" style="background: var(--success)" /> success</span>
          <span><span class="swatch" style="background: var(--rescued)" /> rescued</span>
          <span><span class="swatch" style="background: var(--error)" /> failed</span>
        </div>
      </div>
    </div>
  )
}

const STAGES = ['repo_facts', 'discovery', 'reexamination', 'detail', 'connections', 'reconcile']

function seedStages(cy) {
  const xStep = 240
  STAGES.forEach((name, i) => {
    cy.add({
      data: { id: 'stage:' + name, label: name, kind: 'stage', stage: name, status: 'pending' },
      position: { x: 80 + i * xStep, y: 80 },
      classes: 'stage status-pending',
    })
    if (i > 0) {
      cy.add({
        data: { id: `e:${STAGES[i-1]}->${name}`, source: 'stage:' + STAGES[i-1], target: 'stage:' + name, kind: 'stage-edge' },
        classes: 'stage-edge',
      })
    }
  })
}

function syncGraph(cy, stagesMap, jobsMap) {
  cy.batch(() => {
    // Update stage statuses.
    for (const [id, st] of stagesMap) {
      const node = cy.getElementById('stage:' + id)
      if (node && node.length) {
        node.removeClass('status-pending status-running status-success status-failed status-cancelled status-skipped')
        node.addClass('status-' + (st.status || 'pending'))
        node.data('progress', st.percent || 0)
        node.data('total', st.total || 0)
        node.data('done', st.done || 0)
      }
    }

    // Add/update job nodes.
    const seenJobIDs = new Set()
    const stageBuckets = new Map()
    for (const job of jobsMap.values()) {
      const stage = job.stage || 'unknown'
      if (!stageBuckets.has(stage)) stageBuckets.set(stage, [])
      stageBuckets.get(stage).push(job)
    }

    const stageX = {}
    STAGES.forEach((name, i) => { stageX[name] = 80 + i * 240 })

    for (const [stage, list] of stageBuckets) {
      list.forEach((job, idx) => {
        const id = 'job:' + job.id
        seenJobIDs.add(id)
        const status = job.status || 'pending'
        let node = cy.getElementById(id)
        const label = jobLabel(job)
        if (node.length === 0) {
          cy.add({
            data: {
              id,
              label,
              kind: 'job',
              stage,
              jobID: job.id,
              status,
            },
            position: { x: stageX[stage] || 80, y: 180 + idx * 36 },
            classes: 'job status-' + status,
          })
          // Edge from stage parent to job (or from parent job if known).
          const sourceID = job.parentId ? ('job:' + job.parentId) : ('stage:' + stage)
          if (cy.getElementById(sourceID).length) {
            cy.add({
              data: { id: 'edge:' + sourceID + '->' + id, source: sourceID, target: id, kind: 'job-edge' },
              classes: 'job-edge',
            })
          }
        } else {
          node.data('label', label)
          node.removeClass('status-pending status-running status-success status-failed status-cancelled status-skipped')
          node.addClass('status-' + status)
        }
      })
    }

    // Remove orphan job nodes (e.g. on reset).
    cy.nodes('[kind = "job"]').forEach((n) => {
      if (!seenJobIDs.has(n.id())) {
        n.remove()
      }
    })
  })

  // Apply a deterministic layout.
  layoutFor(cy)
}

function jobLabel(job) {
  // Compact label: the entity name if available, else the last segment of
  // the job id.
  const p = job.payload || {}
  if (p.name) return truncate(p.name, 24)
  if (p.objective_id) return truncate(p.objective_id, 24)
  const tail = job.id.split('.').slice(-2).join('.')
  return truncate(tail, 24)
}

function truncate(s, n) {
  if (!s) return ''
  return s.length > n ? s.slice(0, n - 1) + '\u2026' : s
}

function layoutFor(cy) {
  cy.layout({
    name: 'preset',
    fit: false,
  }).run()

  // Compute simple column-by-stage layout: stages on top row at x = 80 + i*240,
  // jobs in their stage column stacked below, alphabetical for stability.
  const STAGE_X = {}
  STAGES.forEach((name, i) => { STAGE_X[name] = 80 + i * 240 })

  const grouped = new Map()
  cy.nodes('[kind = "job"]').forEach((n) => {
    const stage = n.data('stage') || 'unknown'
    if (!grouped.has(stage)) grouped.set(stage, [])
    grouped.get(stage).push(n)
  })
  for (const [stage, nodes] of grouped) {
    nodes.sort((a, b) => a.data('label').localeCompare(b.data('label')))
    nodes.forEach((n, idx) => {
      n.position({ x: STAGE_X[stage] ?? 80, y: 180 + idx * 36 })
    })
  }

  // Stages always at the same y.
  STAGES.forEach((name, i) => {
    const node = cy.getElementById('stage:' + name)
    if (node.length) node.position({ x: STAGE_X[name], y: 80 })
  })
}

const STYLE = [
  {
    selector: 'node[kind = "stage"]',
    style: {
      'shape': 'round-rectangle',
      'background-color': '#1c2746',
      'border-color': '#4f8cff',
      'border-width': 2,
      'label': 'data(label)',
      'color': '#e9eefa',
      'text-valign': 'center',
      'text-halign': 'center',
      'font-size': 12,
      'font-weight': 600,
      'width': 180,
      'height': 50,
    },
  },
  {
    selector: 'node[kind = "stage"].status-running',
    style: { 'border-color': '#4f8cff', 'border-width': 3, 'background-color': '#1d2c54' },
  },
  {
    selector: 'node[kind = "stage"].status-success',
    style: { 'border-color': '#22c55e', 'background-color': '#102a1c' },
  },
  {
    selector: 'node[kind = "stage"].status-failed',
    style: { 'border-color': '#ef4444', 'background-color': '#3a1418' },
  },
  {
    selector: 'node[kind = "stage"].status-skipped',
    style: { 'border-color': '#5b6a8c', 'background-color': '#1a2238' },
  },
  {
    selector: 'node[kind = "job"]',
    style: {
      'shape': 'round-rectangle',
      'background-color': '#1a2238',
      'border-color': '#3a4c7a',
      'border-width': 1,
      'label': 'data(label)',
      'color': '#e9eefa',
      'text-valign': 'center',
      'text-halign': 'center',
      'font-size': 10,
      'width': 140,
      'height': 26,
    },
  },
  {
    selector: 'node[kind = "job"].status-running',
    style: { 'border-color': '#4f8cff', 'background-color': '#1f2c52', 'border-width': 2 },
  },
  {
    selector: 'node[kind = "job"].status-success',
    style: { 'border-color': '#22c55e', 'background-color': '#0f2a1c' },
  },
  {
    selector: 'node[kind = "job"].status-failed',
    style: { 'border-color': '#ef4444', 'background-color': '#321015' },
  },
  {
    selector: 'node[kind = "job"].status-skipped',
    style: { 'border-color': '#5b6a8c', 'background-color': '#1a2238', 'opacity': 0.6 },
  },
  {
    selector: 'edge',
    style: {
      'width': 1,
      'line-color': '#2c3f72',
      'curve-style': 'bezier',
      'target-arrow-shape': 'triangle',
      'target-arrow-color': '#2c3f72',
    },
  },
  {
    selector: 'edge.stage-edge',
    style: { 'width': 2, 'line-color': '#3a4c7a', 'target-arrow-color': '#3a4c7a' },
  },
  {
    selector: 'node:selected',
    style: { 'border-color': '#22c55e', 'border-width': 3 },
  },
]
