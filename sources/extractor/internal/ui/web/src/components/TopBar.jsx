import { runMeta } from '../lib/store.js'
import { cancelRun } from '../lib/api.js'

// TopBar shows brand + active-run status + cancel + help.
export function TopBar({ onHelp, onGraph, showGraph }) {
  const meta = runMeta.value
  const status = meta?.status || 'idle'
  const onCancel = async () => {
    if (!meta?.id) return
    // Optimistically flip the status pill to "cancelling" so the user gets
    // immediate feedback. The real terminal status (cancelled/failed)
    // arrives via SSE when the orchestrator finishes unwinding, which can
    // take several seconds while in-flight LLM calls drain.
    runMeta.value = { ...meta, status: 'cancelling' }
    try {
      await cancelRun(meta.id)
    } catch (e) {
      console.error(e)
      // Roll back the optimistic update on POST failure so the UI doesn't
      // lie about the run state.
      runMeta.value = { ...runMeta.value, status: meta.status }
    }
  }

  // The status-pill title shows the error message on hover when failed,
  // and the empty hint when the run produced 0 entities. This makes the
  // pill itself a self-explanatory status indicator.
  let pillTitle = status
  if (meta?.status === 'failed' && meta?.error) pillTitle = 'failed: ' + meta.error
  else if (meta?.status === 'completed' && meta?.empty) pillTitle = 'completed but produced no entities'

  return (
    <header class="topbar">
      <div class="logo">
        <span class="target" />
        <span>DiffMind</span>
      </div>
      <div class="right">
        {meta?.id && (
          <span class="muted" style="font-family: 'JetBrains Mono', monospace; font-size: 12px; color: var(--text-muted);">
            run {meta.id}
          </span>
        )}
        <span class={'status-pill ' + status} title={pillTitle}>
          {status}{meta?.status === 'completed' && meta?.empty ? ' (empty)' : ''}
        </span>
        {status === 'running' && (
          <button class="btn danger" onClick={onCancel}>Cancel</button>
        )}
        {meta?.id && (
          <button
            class={'og-graph-btn' + (showGraph ? ' active' : '')}
            onClick={onGraph}
            title="Outcome graph (connections view)"
          >
            Graph
          </button>
        )}
        <button class="btn secondary" onClick={onHelp} title="Help (press ?)">?</button>
      </div>
    </header>
  )
}
