import { useEffect, useRef, useState } from 'preact/hooks'
import { applyEvent, resetStore, runMeta, selection } from '../lib/store.js'
import { openEventStream } from '../lib/sse.js'
import { getRunState, ssePath } from '../lib/api.js'
import { navigate } from '../lib/router.js'
import { TopBar } from '../components/TopBar.jsx'
import { PipelineStrip } from '../components/PipelineStrip.jsx'
import { LiveGraph } from '../components/LiveGraph.jsx'
import { Timeline } from '../components/Timeline.jsx'
import { DetailDrawer } from '../components/DetailDrawer.jsx'
import { HelpOverlay } from '../components/HelpOverlay.jsx'
import { StatusBanner } from '../components/StatusBanner.jsx'
import { SystemStatus } from '../components/SystemStatus.jsx'
import { OutcomeGraph } from '../components/OutcomeGraph.jsx'

// Detail renders the live/replay experience for a single run, identified by
// the runID from the URL. It owns the per-run SSE subscription; on EOF or
// disconnect it reconciles the run's terminal status from the authoritative
// /api/runs/{id}/state endpoint.
export function Detail({ runID }) {
  const closeRef = useRef(null)
  const [help, setHelp] = useState(false)
  const [showGraph, setShowGraph] = useState(false)

  // reconcileTerminalStatus flips runMeta to the run's real terminal status if
  // the SSE stream closed while we still thought it was running.
  const reconcileTerminalStatus = async (id) => {
    try {
      const s = await getRunState(id)
      const snap = s.state
      if (!snap) return
      const term = snap.status
      if (term === 'running' || term === 'cancelling' || term === 'idle') return
      const cur = runMeta.value
      if (!cur || cur.status === term) return
      runMeta.value = { ...cur, status: term, finishedAt: snap.finished_at || cur.finishedAt, error: snap.error || cur.error }
    } catch {
      /* pruned or network blip — SSE reconnect path will retry */
    }
  }

  // Whenever runID changes, reset the store, cold-load the run state, then
  // attach the SSE stream (which transparently replays events.jsonl for a
  // finished run).
  useEffect(() => {
    let cancelled = false
    const attach = (id) => {
      if (closeRef.current) { closeRef.current(); closeRef.current = null }
      const url = ssePath(`/api/runs/${encodeURIComponent(id)}/events`)
      closeRef.current = openEventStream(url, {
        onEvent: (e) => applyEvent(e),
        onEOF: () => { void reconcileTerminalStatus(id) },
        onError: () => { void reconcileTerminalStatus(id) },
      })
    }
    const init = async () => {
      resetStore()
      try {
        const s = await getRunState(runID)
        if (cancelled) return
        const snap = s.state || {}
        runMeta.value = {
          id: runID,
          startedAt: snap.started_at,
          finishedAt: snap.finished_at,
          status: snap.status || 'completed',
          repo: snap.repo_path,
          error: snap.error || '',
        }
        for (const e of s.events || []) applyEvent(e)
      } catch {
        /* unknown run; the SSE attach + reconcile will surface the truth */
      }
      if (!cancelled) attach(runID)
    }
    init()
    return () => {
      cancelled = true
      if (closeRef.current) { closeRef.current(); closeRef.current = null }
    }
  }, [runID])

  // Keyboard shortcuts.
  useEffect(() => {
    const handler = (e) => {
      if (['INPUT', 'TEXTAREA', 'SELECT'].includes(e.target.tagName)) return
      if (e.key === '?' || (e.shiftKey && e.key === '/')) {
        setHelp((v) => !v)
      } else if (e.key === 'Escape') {
        if (showGraph) setShowGraph(false)
        else if (help) setHelp(false)
        else if (selection.value) selection.value = null
      }
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [help, showGraph])

  return (
    <div class="app">
      <div class="detail-topbar-row">
        <button class="btn secondary back-btn" onClick={() => navigate('/')} title="Back to all runs">
          {'←'} All runs
        </button>
        <span class="detail-run-id">{runID}</span>
      </div>
      <TopBar onHelp={() => setHelp(true)} onGraph={() => setShowGraph((v) => !v)} showGraph={showGraph} />
      {showGraph && <OutcomeGraph onClose={() => setShowGraph(false)} />}
      <SystemStatus />
      <StatusBanner />
      <PipelineStrip />
      <div class="workspace detail-workspace">
        <section class="center-pane">
          <div class="graph-host">
            <LiveGraph />
          </div>
          <Timeline />
        </section>
        <section class="details-pane"><DetailDrawer /></section>
      </div>
      {help && <HelpOverlay onClose={() => setHelp(false)} />}
    </div>
  )
}
