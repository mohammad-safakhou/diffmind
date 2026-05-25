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

// Main pipeline row, in left-to-right execution order.
const STAGES = ['repo_facts', 'discovery', 'reexamination', 'detail', 'index', 'connections', 'reconcile']
// Parallel stages live ABOVE the main row. Today we have one
// parallel stage: 'index.build' (the per-language Docker image
// build that runs while Stages 1-3 LLM work happens). It has
// edges FROM repo_facts (which triggers it) and INTO index
// (which consumes its output), making the parallelism legible.
const PARALLEL_STAGES = ['index.build']

// y positions. PARALLEL_Y is the row above the main pipeline;
// MAIN_Y is the main row.
const PARALLEL_Y = 30
const MAIN_Y = 130
const JOBS_Y_START = 270 // top of the job-node block under stages

function seedStages(cy) {
  const stageX = stagePositions(cy)
  STAGES.forEach((name, i) => {
    cy.add({
      data: { id: 'stage:' + name, label: name, kind: 'stage', stage: name, status: 'pending' },
      position: { x: stageX[name], y: MAIN_Y },
      classes: 'stage status-pending',
    })
    if (i > 0) {
      cy.add({
        data: { id: `e:${STAGES[i-1]}->${name}`, source: 'stage:' + STAGES[i-1], target: 'stage:' + name, kind: 'stage-edge' },
        classes: 'stage-edge',
      })
    }
  })
  // Parallel branch: place "index.build" between repo_facts and
  // index on the row ABOVE the main pipeline. Edges flow from
  // repo_facts (which triggers it) and into the main `index`
  // stage (which waits on the built image).
  PARALLEL_STAGES.forEach((name) => {
    // Position roughly between repo_facts and index along x.
    const px = (stageX['repo_facts'] + stageX['index']) / 2
    cy.add({
      data: { id: 'stage:' + name, label: name, kind: 'stage', stage: name, status: 'pending' },
      position: { x: px, y: PARALLEL_Y },
      classes: 'stage parallel status-pending',
    })
    cy.add({
      data: { id: 'e:repo_facts->' + name, source: 'stage:repo_facts', target: 'stage:' + name, kind: 'stage-edge' },
      classes: 'stage-edge parallel-edge',
    })
    cy.add({
      data: { id: 'e:' + name + '->index', source: 'stage:' + name, target: 'stage:index', kind: 'stage-edge' },
      classes: 'stage-edge parallel-edge',
    })
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
        const isBatch = job.payload?.batch === true
        let node = cy.getElementById(id)
        const label = jobLabelLive(job)
        // The "kind" subtype lets the stylesheet pick a distinct
        // appearance for batch nodes vs single-entity jobs (bigger,
        // bolder, different border colour). The dashboard's
        // detail stage is dominated by these, so making them
        // visually distinct from their children is what makes the
        // batching legible at a glance.
        const subkind = isBatch ? 'batch' : 'job'
        if (node.length === 0) {
          cy.add({
            data: {
              id,
              label,
              kind: 'job',
              subkind,
              stage,
              jobID: job.id,
              status,
            },
            position: { x: stageX[stage] || 80, y: 220 + idx * 72 },
            classes: 'job ' + subkind + ' status-' + status,
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
          node.data('subkind', subkind)
          node.removeClass('status-pending status-running status-success status-failed status-cancelled status-rescued status-skipped batch job')
          node.addClass(subkind + ' status-' + status)
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
//
// Batch nodes get a special label that includes the entity count:
// "BATCH × 12 · GET /a + 11 more". Without this users could not
// tell at a glance which nodes are batches.
function jobName(job) {
  const p = job.payload || {}
  if (p.batch === true) {
    const size = p.batch_size || (p.seed_names ? p.seed_names.length : '?')
    const preview = jobBatchPreview(p)
    return preview ? `BATCH × ${size}\n${preview}` : `BATCH × ${size}`
  }
  if (p.name) return p.name
  if (p.objective_id) return p.objective_id
  return job.id.split('.').slice(-2).join('.')
}

// jobBatchPreview returns "GET /a, GET /b, + N more" for a batch
// job. Uses the seed_names array the server attached at
// job_pending / job_started time.
function jobBatchPreview(p) {
  const names = p.seed_names || []
  if (!Array.isArray(names) || names.length === 0) return ''
  const head = names.slice(0, 2).join(', ')
  if (names.length <= 2) return head
  return `${head} +${names.length - 2} more`
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

  // Compute a stable column-by-stage layout. Within each stage we
  // group jobs by their batch parent: each batch node appears
  // first (slightly to the left, bolder visual), then its child
  // entities appear indented to the right. Nodes with no batch
  // parent (objective-level jobs or non-batched stages) line up
  // along the stage's primary column.
  const STAGE_X = stagePositions(cy)

  const grouped = new Map()
  cy.nodes('[kind = "job"]').forEach((n) => {
    const stage = n.data('stage') || 'unknown'
    if (!grouped.has(stage)) grouped.set(stage, [])
    grouped.get(stage).push(n)
  })
  for (const [stage, nodes] of grouped) {
    // index.build jobs live on the parallel row, so its column
    // is the midpoint between repo_facts and index (matches
    // seedStages). Other stages use their own column.
    let x
    if (stage === 'index.build') {
      x = (STAGE_X['repo_facts'] + STAGE_X['index']) / 2
    } else {
      x = STAGE_X[stage] ?? 80
    }
    // index.build jobs stack ABOVE the stage node (toward y<PARALLEL_Y);
    // main-row jobs stack BELOW (toward y>MAIN_Y). The layoutStageNodes
    // helper accepts a startY + direction so both orientations work.
    if (stage === 'index.build') {
      layoutStageNodes(cy, nodes, x, PARALLEL_Y - 90, -1)
    } else {
      layoutStageNodes(cy, nodes, x, JOBS_Y_START, +1)
    }
  }

  // Pin stage nodes to their fixed positions so node insertions
  // don't drift them. Main row at MAIN_Y; parallel row at PARALLEL_Y.
  STAGES.forEach((name) => {
    const node = cy.getElementById('stage:' + name)
    if (node.length) node.position({ x: STAGE_X[name], y: MAIN_Y })
  })
  PARALLEL_STAGES.forEach((name) => {
    const node = cy.getElementById('stage:' + name)
    if (node.length) {
      const px = (STAGE_X['repo_facts'] + STAGE_X['index']) / 2
      node.position({ x: px, y: PARALLEL_Y })
    }
  })
}

// layoutStageNodes arranges all job nodes in one stage column.
// Visual model:
//   - Batch nodes sit at the column's main X.
//   - Entity nodes whose parentId is a batch sit OFFSET (to the
//     right for downward-growing stages, to the left for
//     upward-growing parallel stages). The offset is bounded so
//     entity nodes never cross into the NEXT stage's column.
//   - Plain (non-batched) job nodes line up at the main X.
//
// startY is the y coordinate of the FIRST job; dirY is +1 to
// grow downward (main row) or -1 to grow upward (parallel row).
// Caller passes the right combination based on stage placement.
//
// CRITICAL LAYOUT FIX (Sprint 4): batched entities used to indent
// rightward by 260px, which on small viewports + many batches put
// them straight into the next stage's column. Now we bound the
// indent + cap each stage's job stack at a maximum width so no
// batched child node overlaps the next column.
function layoutStageNodes(cy, nodes, mainX, startY, dirY) {
  // The next column's center is the closest reference point;
  // we keep child entities at most halfway between us and them.
  const stageX = stagePositions(cy)
  const allStageX = Object.values(stageX).sort((a, b) => a - b)
  const myIdx = allStageX.indexOf(mainX)
  const nextX = myIdx >= 0 && myIdx + 1 < allStageX.length ? allStageX[myIdx + 1] : mainX + 280
  const halfGap = Math.max(80, Math.min(160, (nextX - mainX) / 2 - 20))
  const batchEntityOffsetX = halfGap // children indent toward (but not into) the next column
  const yStride = 70                 // vertical spacing between sibling rows

  // Identify roots vs batch children.
  const byID = new Map()
  for (const n of nodes) byID.set(n.id(), n)

  const rootNodes = []
  const childrenByBatch = new Map()
  for (const n of nodes) {
    const parentJobID = parentJobOf(cy, n)
    if (parentJobID) {
      const parentNode = cy.getElementById(parentJobID)
      if (parentNode.length && parentNode.data('subkind') === 'batch') {
        if (!childrenByBatch.has(parentJobID)) childrenByBatch.set(parentJobID, [])
        childrenByBatch.get(parentJobID).push(n)
        continue
      }
    }
    rootNodes.push(n)
  }
  rootNodes.sort((a, b) => sortKey(a).localeCompare(sortKey(b)))

  let y = startY
  const stride = dirY * yStride
  for (const root of rootNodes) {
    root.position({ x: mainX, y })
    y += stride
    const kids = childrenByBatch.get(root.id())
    if (kids && kids.length) {
      kids.sort((a, b) => sortKey(a).localeCompare(sortKey(b)))
      for (const k of kids) {
        k.position({ x: mainX + batchEntityOffsetX, y })
        y += stride
      }
      y += dirY * 14 // extra breathing room after a batch's last entity
    }
  }
}

// parentJobOf returns the job: id of a node's parent if its parent
// is a job-typed node (i.e. another job inside the graph). Returns
// '' when the parent is a stage node (or there is no parent edge
// yet because Cytoscape hasn't processed the incoming edge yet).
function parentJobOf(cy, n) {
  const inc = n.incomers('edge')
  for (let i = 0; i < inc.length; i++) {
    const src = inc[i].source()
    if (src.data('kind') === 'job') return src.id()
  }
  return ''
}

// sortKey is used to order sibling nodes in the column. We prefer
// the label which the renderer already keeps in sync with the
// underlying entity name; falls back to the node id for stability.
function sortKey(n) {
  const l = n.data('label') || n.id()
  return String(l)
}

function stagePositions(cy) {
  const width = Math.max(cy.width() || 0, 0)
  // Stride between adjacent stage columns must accommodate the
  // wider stage node (220 px) plus comfortable padding. We bumped
  // the floor to 260 so labels never bleed into neighbouring
  // columns; for very wide viewports we stretch the row out to
  // fill available space.
  const minStride = 260
  const xStep = width > 0
    ? Math.max(minStride, (width - 200) / Math.max(1, STAGES.length - 1))
    : minStride
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
  // BATCH nodes — visually distinct from per-entity job nodes so the
  // user can see at a glance "this is one LLM call covering N
  // entities" vs "this is one entity". Bigger, bolder border,
  // slightly different background.
  {
    selector: 'node[kind = "job"].batch',
    style: {
      'width': 260,
      'height': 70,
      'border-width': 2,
      'border-color': '#7aa2ff',
      'background-color': '#21305c',
      'font-weight': 700,
      'font-size': 12,
    },
  },
  {
    selector: 'node[kind = "job"].batch.status-running',
    style: { 'border-color': '#4f8cff', 'background-color': '#243a72', 'border-width': 3 },
  },
  {
    selector: 'node[kind = "job"].batch.status-success',
    style: { 'border-color': '#22c55e', 'background-color': '#143a23' },
  },
  {
    selector: 'node[kind = "job"].batch.status-failed',
    style: { 'border-color': '#ef4444', 'background-color': '#3a121a' },
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
  // Parallel stages (index.build) render with a dashed border so
  // the user instantly sees they don't sit on the main sequential
  // pipeline row.
  {
    selector: 'node[kind = "stage"].parallel',
    style: {
      'border-style': 'dashed',
      'border-width': 2,
      'background-color': '#172240',
    },
  },
  {
    selector: 'edge.parallel-edge',
    style: {
      'width': 1,
      'line-style': 'dashed',
      'line-color': '#4f8cff',
      'target-arrow-color': '#4f8cff',
      'curve-style': 'unbundled-bezier',
      'control-point-distances': [-40],
      'control-point-weights': [0.5],
    },
  },
  {
    selector: 'node:selected',
    style: { 'border-color': '#22c55e', 'border-width': 3 },
  },
]
