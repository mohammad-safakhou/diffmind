import { useEffect, useRef } from 'preact/hooks'
import cytoscape from 'cytoscape'
import { effect } from '@preact/signals'
import { jobs, stages, selection, runMeta } from '../lib/store.js'

// LiveGraph renders the run as a DAG. Stage nodes are persistent; job
// nodes are added/updated as events arrive. Click → selects a node, which
// the DetailDrawer subscribes to.
//
// Each node's label is two lines: the entity/stage name on top, and a
// live duration timer below (in HH:MM:SS / MM:SS / SSs format). The
// timer is recomputed every second via a 1s ticker.
export function LiveGraph() {
  const ref = useRef(null)
  // The cy instance is created once. We expose it on a ref so the
  // ticker effect can re-label nodes without resetting the whole graph.
  const cyRef = useRef(null)

  useEffect(() => {
    const cy = cytoscape({
      container: ref.current,
      style: STYLE,
      layout: { name: 'preset' },
      wheelSensitivity: 0.2,
      minZoom: 0.3,
      maxZoom: 2.5,
    })
    cyRef.current = cy

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

    const ro = new ResizeObserver(() => {
      cy.resize()
      layoutFor(cy)
    })
    if (ref.current) ro.observe(ref.current)

    // 1-second ticker for the live duration labels. We don't need a
    // full re-sync, just re-compute the label for every node whose
    // status is "running". For finished nodes the label was set once
    // at completion and never needs to change.
    const ticker = setInterval(() => {
      cy.nodes().forEach((n) => {
        // Only running nodes need their timer ticked. Pending nodes
        // have no start timestamp yet, so their duration would render
        // as empty either way. Finished nodes are frozen by sync.
        if (!n.hasClass('status-running')) return
        if (n.data('kind') === 'stage') {
          n.data('label', stageLabel(n.data('stage'), stageById(stages.value, n.data('stage'))))
        } else {
          n.data('label', jobLabelLive(jobById(jobs.value, n.data('jobID'))))
        }
      })
    }, 1000)

    return () => {
      clearInterval(ticker)
      stop()
      ro.disconnect()
      cy.destroy()
      cyRef.current = null
    }
  }, [])

  return (
    <div class="graph-panel">
      <div class="graph-panel-header">
        <div>
          <h2>Run Graph</h2>
          <span>Drag to pan, scroll to zoom, click nodes for details.</span>
        </div>
        <div class="graph-legend">
          <span><span class="swatch" style="background: var(--pending)" /> pending</span>
          <span><span class="swatch" style="background: var(--running)" /> running</span>
          <span><span class="swatch" style="background: var(--success)" /> success</span>
          <span><span class="swatch" style="background: var(--rescued)" /> rescued</span>
          <span><span class="swatch" style="background: var(--error)" /> failed</span>
        </div>
      </div>
      <div class="graph-canvas">
        <div ref={ref} id="cy" />
      </div>
    </div>
  )
}

const STAGES = ['repo_facts', 'discovery', 'reexamination', 'detail', 'connections', 'reconcile']

function seedStages(cy) {
  const stageX = stagePositions(cy)
  STAGES.forEach((name, i) => {
    cy.add({
      data: { id: 'stage:' + name, label: name, kind: 'stage', stage: name, status: 'pending' },
      position: { x: stageX[name], y: 70 },
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
        node.removeClass('status-pending status-running status-success status-failed status-cancelled status-rescued status-skipped')
        node.addClass('status-' + (st.status || 'pending'))
        node.data('progress', st.percent || 0)
        node.data('total', st.total || 0)
        node.data('done', st.done || 0)
        node.data('label', stageLabel(id, st))
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

    const stageX = stagePositions(cy)

    for (const [stage, list] of stageBuckets) {
      list.forEach((job, idx) => {
        const id = 'job:' + job.id
        seenJobIDs.add(id)
        const status = job.status || 'pending'
        let node = cy.getElementById(id)
        const label = jobLabelLive(job)
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
            position: { x: stageX[stage] || 80, y: 220 + idx * 72 },
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
          node.removeClass('status-pending status-running status-success status-failed status-cancelled status-rescued status-skipped')
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

// jobLabelLive builds the two-line label for a job node:
//
//     <entity name>
//     <live duration>
//
// The duration ticks every second while the job is running; once the
// job finishes it freezes at the final value. We compute live values
// off Date.now() rather than off the start timestamp so a single
// rerender keeps every node's clock in sync.
function jobLabelLive(job) {
  if (!job) return ''
  const name = jobName(job)
  const dur = jobDurationLabel(job)
  return dur ? `${name}\n${dur}` : name
}

// jobName extracts the human-readable display name for a node. The
// orchestrator emits the seed name in the job_started payload; if
// that's missing we fall back to the objective id or the tail of the
// job id.
function jobName(job) {
  const p = job.payload || {}
  if (p.name) return p.name
  if (p.objective_id) return p.objective_id
  return job.id.split('.').slice(-2).join('.')
}

// jobDurationLabel renders the per-node timer text. While the job is
// running the duration grows; once it completes/fails the duration
// reflects the final elapsed time.
function jobDurationLabel(job) {
  const start = job.startedAt ? new Date(job.startedAt).getTime() : null
  const end = job.finishedAt ? new Date(job.finishedAt).getTime() : null
  // If the job has a payload duration_ms (from the completed event)
  // prefer that — it's the canonical "what the server measured" value.
  if ((job.status === 'success' || job.status === 'failed' || job.status === 'cancelled' || job.status === 'rescued') && job.payload?.duration_ms != null) {
    return '✓ ' + formatMs(job.payload.duration_ms)
  }
  if (!start) return ''
  const ms = (end ?? Date.now()) - start
  if (ms < 0) return ''
  const prefix = job.status === 'running' ? '⏱ ' : '✓ '
  return prefix + formatMs(ms)
}

// stageLabel builds the two-line label for a stage node.
function stageLabel(name, st) {
  if (!st) return name
  const dur = stageDurationLabel(st)
  return dur ? `${name}\n${dur}` : name
}

function stageDurationLabel(st) {
  const start = st.startedAt ? new Date(st.startedAt).getTime() : null
  const end = st.finishedAt ? new Date(st.finishedAt).getTime() : null
  if (!start) return ''
  const ms = (end ?? Date.now()) - start
  if (ms < 0) return ''
  const prefix = st.status === 'running' ? '⏱ ' : (st.status === 'success' ? '✓ ' : st.status === 'failed' ? '✗ ' : '')
  return prefix + formatMs(ms)
}

// formatMs renders a duration in ms as the shortest unambiguous string:
//   <  60 000ms → "12s"
//   <  60 minutes → "2m 14s"
//   ≥ 60 minutes → "1h 03m"
function formatMs(ms) {
  if (!Number.isFinite(ms) || ms < 0) return ''
  const totalSec = Math.floor(ms / 1000)
  if (totalSec < 60) return totalSec + 's'
  const m = Math.floor(totalSec / 60)
  const s = totalSec % 60
  if (m < 60) return `${m}m ${String(s).padStart(2, '0')}s`
  const h = Math.floor(m / 60)
  const mm = m % 60
  return `${h}h ${String(mm).padStart(2, '0')}m`
}

// Helpers used by the 1-second ticker to look up current node state
// from the signal maps without a full re-sync.
function jobById(jobsMap, id) {
  for (const j of jobsMap.values()) if (j.id === id) return j
  return null
}
function stageById(stagesMap, id) {
  return stagesMap.get(id)
}

function layoutFor(cy) {
  cy.layout({
    name: 'preset',
    fit: false,
  }).run()

  // Compute a stable column-by-stage layout. Columns spread out on wide
  // screens but keep a minimum gap so stage and job labels do not collide.
  const STAGE_X = stagePositions(cy)

  const grouped = new Map()
  cy.nodes('[kind = "job"]').forEach((n) => {
    const stage = n.data('stage') || 'unknown'
    if (!grouped.has(stage)) grouped.set(stage, [])
    grouped.get(stage).push(n)
  })
  for (const [stage, nodes] of grouped) {
    nodes.sort((a, b) => a.data('label').localeCompare(b.data('label')))
    nodes.forEach((n, idx) => {
      // y stride must clear the new larger node height (~80px) plus
      // padding, otherwise neighbouring nodes overlap visually.
      n.position({ x: STAGE_X[stage] ?? 80, y: 220 + idx * 72 })
    })
  }

  // Stages always at the same y. Pulled down slightly so the stage
  // node's full height (now 80px) fits comfortably below the
  // graph header.
  STAGES.forEach((name, i) => {
    const node = cy.getElementById('stage:' + name)
    if (node.length) node.position({ x: STAGE_X[name], y: 90 })
  })
}

function stagePositions(cy) {
  const width = Math.max(cy.width() || 0, 0)
  // The stride between adjacent stage columns must accommodate the new
  // wider node (220px) plus comfortable padding. We bumped the floor
  // from 220 to 280 so labels never bleed into neighbouring columns.
  const xStep = width > 0 ? Math.max(280, (width - 220) / Math.max(1, STAGES.length - 1)) : 280
  const out = {}
  STAGES.forEach((name, i) => { out[name] = 130 + i * xStep })
  return out
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
      'font-size': 13,
      'font-weight': 600,
      'width': 220,
      'height': 80,
      // Wrap so long stage labels + timer line fit; keep newlines so
      // the two-line label renders as intended.
      'text-wrap': 'wrap',
      'text-max-width': 200,
      'line-height': 1.3,
      'padding': '6px',
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
    selector: 'node[kind = "stage"].status-cancelled',
    style: { 'border-color': '#f59e0b', 'background-color': '#33220d' },
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
      'font-size': 11,
      'width': 230,
      'height': 58,
      // Wrap long entity names (some are 30+ characters) and give the
      // duration line room. The two-line layout means height bumps
      // proportionally; 58px fits 2 lines comfortably at 11px font.
      'text-wrap': 'wrap',
      'text-max-width': 210,
      'line-height': 1.25,
      'padding': '4px',
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
    selector: 'node[kind = "job"].status-cancelled',
    style: { 'border-color': '#f59e0b', 'background-color': '#33220d' },
  },
  {
    selector: 'node[kind = "job"].status-rescued',
    style: { 'border-color': '#f59e0b', 'background-color': '#33220d' },
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
