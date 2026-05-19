import { useEffect, useRef, useState } from 'preact/hooks'
import { applyEvent, resetStore, runMeta, selection } from './lib/store.js'
import { openEventStream } from './lib/sse.js'
import { getActive, getRunState, onAuthFailure, ssePath, getToken, setToken } from './lib/api.js'
import { TopBar } from './components/TopBar.jsx'
import { PipelineStrip } from './components/PipelineStrip.jsx'
import { RunForm } from './components/RunForm.jsx'
import { LiveGraph } from './components/LiveGraph.jsx'
import { Timeline } from './components/Timeline.jsx'
import { DetailDrawer } from './components/DetailDrawer.jsx'
import { RunsSidebar } from './components/RunsSidebar.jsx'
import { HelpOverlay } from './components/HelpOverlay.jsx'
import { StatusBanner } from './components/StatusBanner.jsx'

// App owns the SSE subscription. Whenever a new run is launched OR the user
// picks a finished run from the sidebar to replay, we close the previous
// stream and open a new one. Replay is transparent: the server falls back
// to events.jsonl on its own.
export function App() {
  const closeRef = useRef(null)
  const [help, setHelp] = useState(false)
  const [authError, setAuthError] = useState(false)

  useEffect(() => {
    onAuthFailure(() => setAuthError(true))
  }, [])

  const attach = (runID) => {
    if (closeRef.current) {
      closeRef.current()
      closeRef.current = null
    }
    if (!runID) return
    const url = ssePath(`/api/runs/${encodeURIComponent(runID)}/events`)
    closeRef.current = openEventStream(url, {
      onEvent: (e) => applyEvent(e),
      // Safety net: when SSE signals EOF (or fully disconnects), the
      // run has either reached a terminal state OR our connection
      // missed the terminal event mid-flight. Re-query the runner's
      // authoritative state and reconcile. If the runner says
      // "failed" but our runMeta still says "running", we flip the
      // pill. This is the catch-all for the historical class of
      // "stuck at running" bugs.
      onEOF: () => { void reconcileTerminalStatus(runID) },
      onError: () => { void reconcileTerminalStatus(runID) },
    })
  }

  // reconcileTerminalStatus fetches the runner's State for a run
  // whose SSE has closed and updates runMeta if it still says
  // "running" / "cancelling". Idempotent and cheap.
  const reconcileTerminalStatus = async (runID) => {
    try {
      const active = await getActive()
      // Two cases:
      //  - The runner's active state still has this run id: trust
      //    its status field directly.
      //  - The runner has moved on (status: 'idle' or different id):
      //    look up the run via /api/runs/{id}/state which returns
      //    the historical state too.
      let snap = null
      if (active && active.run_id === runID) {
        snap = active
      } else {
        try {
          const s = await getRunState(runID)
          snap = s.state
        } catch {
          /* run was pruned; nothing to reconcile against */
        }
      }
      if (!snap) return
      const term = snap.status
      if (term === 'running' || term === 'cancelling' || term === 'idle') return
      const cur = runMeta.value
      if (!cur) return
      if (cur.status !== term) {
        runMeta.value = {
          ...cur,
          status: term,
          finishedAt: snap.finished_at || cur.finishedAt,
          error: snap.error || cur.error,
        }
      }
    } catch {
      /* network blip or auth — ignore; the SSE reconnect path will retry */
    }
  }

  // First load: hydrate from the singleton runner if a run is active, or
  // from the latest finished run otherwise.
  useEffect(() => {
    const init = async () => {
      try {
        const active = await getActive()
        if (active && active.run_id && active.status !== 'idle') {
          const state = await getRunState(active.run_id)
          resetStore()
          // Seed runMeta from the runner state BEFORE applying events,
          // so the topbar shows the right status even if the events
          // ring buffer has evicted run_started, or if no run_started/
          // run_completed event exists yet (race during cold load).
          runMeta.value = {
            id: active.run_id,
            startedAt: active.started_at,
            finishedAt: active.finished_at,
            status: active.status,
            repo: active.repo_path,
            error: active.error,
          }
          for (const e of state.events || []) applyEvent(e)
          attach(active.run_id)
        }
      } catch {
        /* fall through; user will pick or start a run */
      }
    }
    init()

    return () => {
      if (closeRef.current) closeRef.current()
    }
  }, [])

  // Keyboard shortcut: '?' toggles help.
  useEffect(() => {
    const handler = (e) => {
      if (e.target.tagName === 'INPUT' || e.target.tagName === 'TEXTAREA' || e.target.tagName === 'SELECT') return
      if (e.key === '?' || (e.shiftKey && e.key === '/')) {
        setHelp((v) => !v)
      } else if (e.key === 'Escape') {
        if (help) {
          setHelp(false)
        } else if (selection.value) {
          selection.value = null
        }
      }
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [help])

  const onLaunched = (runID) => {
    resetStore()
    attach(runID)
  }

  const onReplay = (run) => {
    resetStore()
    // Synthesize a minimal runMeta so the top bar / pipeline strip have
    // something to render before the first replayed event arrives. We
    // MUST honour the run's real terminal status from the runs index
    // — passing every non-completed run through as "running" leaves
    // the pill stuck on "running" until the replay reaches its
    // terminal event (which can be many seconds), and stays wrong
    // entirely if the SSE replay drops before the terminal event.
    // The runs-index `status` is one of: completed, failed,
    // cancelled, running. Trust it.
    const status = run.status || 'completed'
    runMeta.value = {
      id: run.run_id,
      startedAt: run.started_at,
      finishedAt: run.finished_at,
      status,
      repo: run.repo_path,
      error: run.error || '',
    }
    attach(run.run_id)
  }

  return (
    <div class="app">
      <TopBar onHelp={() => setHelp(true)} />
      {authError && <AuthBanner onSubmit={(t) => { setToken(t); setAuthError(false); window.location.reload() }} initial={getToken()} />}
      <StatusBanner />
      <PipelineStrip />
      <div class="workspace">
        <section>
          <RunsSidebar onPick={onReplay} />
          <RunForm onLaunched={onLaunched} />
        </section>
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

// AuthBanner shows when the server returned 401. The user pastes their
// shared secret; we persist it client-side and reload.
function AuthBanner({ onSubmit, initial }) {
  return (
    <div style="background: rgba(245, 158, 11, 0.12); border-bottom: 1px solid var(--warn); padding: 10px 16px; display: flex; gap: 10px; align-items: center;">
      <strong style="color: var(--warn);">Authentication required</strong>
      <span style="color: var(--text-muted); font-size: 12px;">
        Paste the token printed by <code>diffmind ui --ui-token</code> (or set <code>DIFFMIND_UI_TOKEN</code>).
      </span>
      <form
        style="display:flex; gap:8px; margin-left:auto;"
        onSubmit={(e) => {
          e.preventDefault()
          const v = e.target.elements.token.value
          if (v) onSubmit(v)
        }}
      >
        <input name="token" type="password" placeholder="ui token" defaultValue={initial} style="padding: 4px 8px; border-radius: 6px; background: var(--bg-2); border: 1px solid var(--border); color: var(--text);" />
        <button class="btn" type="submit">Save</button>
      </form>
    </div>
  )
}
