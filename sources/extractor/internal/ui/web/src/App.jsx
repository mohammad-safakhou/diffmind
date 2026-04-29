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
    })
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
    // something to render before the first replayed event arrives.
    runMeta.value = {
      id: run.run_id,
      startedAt: run.started_at,
      status: run.status === 'completed' ? 'completed' : 'running',
      repo: run.repo_path,
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
