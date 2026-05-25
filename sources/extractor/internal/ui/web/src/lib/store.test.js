import { describe, it, expect, beforeEach } from 'vitest'
import { applyEvent, resetStore, runMeta, stages, buildLogs } from './store.js'

// Helper: replay a sequence of events through the reducer as if
// they came off the SSE wire in order.
function replay(events) {
  for (const e of events) applyEvent(e)
}

describe('runMeta status preservation on replay', () => {
  beforeEach(() => {
    resetStore()
  })

  // REGRESSION: clicking a FAILED run in the sidebar set
  // runMeta.status = 'failed', but the SSE replay's first event
  // (run_started) used to overwrite it back to 'running'. The user
  // saw "failed" for ~1 second, then "running" for the entire
  // replay window (until run_failed at the very end). The Retry
  // button disappeared during that window.
  //
  // Fix: run_started must preserve a known-terminal runMeta.status.
  it('preserves runMeta.status=failed when run_started arrives during replay', () => {
    // Step 1: sidebar's onReplay seeds runMeta with the run's real
    // terminal status before opening SSE.
    runMeta.value = {
      id: 'r1',
      status: 'failed',
      error: 'detail step crashed',
    }

    // Step 2: the SSE replay starts emitting events. The very first
    // event in events.jsonl is run_started.
    applyEvent({
      kind: 'run_started',
      run_id: 'r1',
      ts: '2026-06-01T12:00:00Z',
      payload: { repo: '/repo', snapshot: '/snap' },
    })

    // The pill must still say "failed". This is what was broken.
    expect(runMeta.value.status).toBe('failed')
    expect(runMeta.value.error).toBe('detail step crashed')
    expect(runMeta.value.id).toBe('r1')
    expect(runMeta.value.repo).toBe('/repo')
  })

  it('preserves runMeta.status=cancelled on replay', () => {
    runMeta.value = { id: 'r2', status: 'cancelled' }
    applyEvent({ kind: 'run_started', run_id: 'r2', ts: 't', payload: {} })
    expect(runMeta.value.status).toBe('cancelled')
  })

  it('preserves runMeta.status=completed on replay', () => {
    runMeta.value = { id: 'r3', status: 'completed' }
    applyEvent({ kind: 'run_started', run_id: 'r3', ts: 't', payload: {} })
    expect(runMeta.value.status).toBe('completed')
  })

  // For a FRESH run (no prior runMeta), run_started must still
  // transition to running. That's the live-run path used by the
  // "Run extraction" button.
  it('sets status=running on a fresh run_started with no prior state', () => {
    expect(runMeta.value).toBeNull()
    applyEvent({
      kind: 'run_started',
      run_id: 'r4',
      ts: '2026-06-01T12:00:00Z',
      payload: { repo: '/repo' },
    })
    expect(runMeta.value.status).toBe('running')
    expect(runMeta.value.id).toBe('r4')
  })

  // For a FRESH run that the SPA had previously surfaced as
  // "running" (e.g. via the active-run init), run_started keeps
  // status running.
  it('keeps status=running when prior was already running', () => {
    runMeta.value = { id: 'r5', status: 'running' }
    applyEvent({ kind: 'run_started', run_id: 'r5', ts: 't', payload: {} })
    expect(runMeta.value.status).toBe('running')
  })

  // After preserving status, a real run_failed in the replay stream
  // still settles to "failed" — the run_started fix doesn't
  // accidentally pin status to its old value forever.
  it('still transitions to terminal on run_failed during replay', () => {
    runMeta.value = { id: 'r6', status: 'failed', error: 'original' }
    replay([
      { kind: 'run_started', run_id: 'r6', ts: 't1', payload: {} },
      { kind: 'stage_started', stage: 'detail', run_id: 'r6', ts: 't2', payload: { total: 5 } },
      // run_failed with a fresher message; reducer should update
      // the error to the fresher one.
      {
        kind: 'run_failed',
        run_id: 'r6',
        ts: 't3',
        message: 'fresh failure message',
        payload: { error_class: 'schema' },
      },
    ])
    expect(runMeta.value.status).toBe('failed')
    expect(runMeta.value.error).toBe('fresh failure message')
    expect(runMeta.value.errorClass).toBe('schema')
  })

  // REGRESSION: the user complained "152 remaining, not correct if
  // some are batch processing". The detail stage now emits
  // batches_total alongside the entity total so the pipeline strip
  // can render "X/N entities · X/B batches". The store must keep
  // both counters in sync.
  it('tracks per-stage batch counts independently from entity counts', () => {
    // Stage starts with 152 entities and 20 batches.
    applyEvent({
      kind: 'stage_started',
      stage: 'detail',
      ts: 't1',
      payload: { total: 152, batches_total: 20, pending: 152 },
    })
    const detail0 = stages.value.get('detail')
    expect(detail0.total).toBe(152)
    expect(detail0.batchesTotal).toBe(20)
    expect(detail0.batchesDone).toBe(0)

    // A batch-level job_completed fires (payload.batch = true).
    applyEvent({
      kind: 'job_completed',
      stage: 'detail',
      job_id: 'detail.dep.batch.X',
      ts: 't2',
      status: 'success',
      payload: { batch: true, batch_size: 12 },
    })
    expect(stages.value.get('detail').batchesDone).toBe(1)

    // Per-entity job_completed events do NOT increment batchesDone.
    applyEvent({
      kind: 'job_completed',
      stage: 'detail',
      job_id: 'detail.dep.entity-1',
      ts: 't3',
      status: 'success',
      payload: { name: 'entity-1' },
    })
    expect(stages.value.get('detail').batchesDone).toBe(1)

    // Another batch finishes.
    applyEvent({
      kind: 'job_completed',
      stage: 'detail',
      job_id: 'detail.dep.batch.Y',
      ts: 't4',
      status: 'success',
      payload: { batch: true, batch_size: 8 },
    })
    expect(stages.value.get('detail').batchesDone).toBe(2)

    // A batch FAILURE also counts toward batchesDone (the batch
    // ran, just failed) — the dashboard shows "5/20 batches"
    // regardless of pass/fail, and the timeline + banner surface
    // the failure separately.
    applyEvent({
      kind: 'job_failed',
      stage: 'detail',
      job_id: 'detail.dep.batch.Z',
      ts: 't5',
      status: 'failed',
      payload: { batch: true, batch_size: 5 },
    })
    expect(stages.value.get('detail').batchesDone).toBe(3)
  })

  // Stages without batching (e.g. discovery, repo_facts) MUST NOT
  // have batchesTotal set, so the PipelineStrip skips that row.
  it('does not display batches when stage has no batch info', () => {
    applyEvent({
      kind: 'stage_started',
      stage: 'discovery',
      ts: 't1',
      payload: { total: 13 },
    })
    const disc = stages.value.get('discovery')
    expect(disc.total).toBe(13)
    expect(disc.batchesTotal).toBe(0)
  })

  // A live run sequence: run_started -> ... -> run_completed
  // should land on "completed" cleanly with no replay interference.
  it('handles a clean live run flow', () => {
    replay([
      { kind: 'run_started', run_id: 'r7', ts: 't1', payload: {} },
      { kind: 'stage_started', stage: 'repo_facts', run_id: 'r7', ts: 't2', payload: { total: 1 } },
      { kind: 'stage_completed', stage: 'repo_facts', run_id: 'r7', ts: 't3', status: 'success' },
      { kind: 'run_completed', run_id: 'r7', ts: 't4', payload: { exposures: 3, dependencies: 2 } },
    ])
    expect(runMeta.value.status).toBe('completed')
    expect(runMeta.value.summary?.exposures).toBe(3)
  })

  // log events scoped to the index.build stage land in the
  // buildLogs ring buffer; other 'log' events (rare) are ignored
  // so the dashboard doesn't accumulate noise.
  it('captures index.build log events into buildLogs (capped at 200)', () => {
    resetStore()
    applyEvent({
      kind: 'log',
      stage: 'index.build',
      ts: 't1',
      message: 'building base java:21',
      payload: { tail: '...maven cold pull...' },
    })
    expect(buildLogs.value.length).toBe(1)
    expect(buildLogs.value[0].message).toBe('building base java:21')

    // Unrelated log → ignored.
    applyEvent({ kind: 'log', stage: 'detail', ts: 't2', message: 'should be ignored' })
    expect(buildLogs.value.length).toBe(1)

    // Ring buffer cap at 200.
    for (let i = 0; i < 250; i++) {
      applyEvent({ kind: 'log', stage: 'index.build', ts: 't', message: 'line ' + i })
    }
    expect(buildLogs.value.length).toBe(200)
    expect(buildLogs.value[199].message).toBe('line 249')
  })
})
