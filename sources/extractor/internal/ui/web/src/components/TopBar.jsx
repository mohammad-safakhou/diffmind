import { runMeta } from '../lib/store.js'
import { cancelRun } from '../lib/api.js'

// TopBar shows brand + active-run status + cancel + help.
export function TopBar({ onHelp }) {
  const meta = runMeta.value
  const status = meta?.status || 'idle'
  const onCancel = async () => {
    if (!meta?.id) return
    try { await cancelRun(meta.id) } catch (e) { console.error(e) }
  }

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
        <span class={'status-pill ' + status}>{status}</span>
        {status === 'running' && (
          <button class="btn danger" onClick={onCancel}>Cancel</button>
        )}
        <button class="btn secondary" onClick={onHelp} title="Help (press ?)">?</button>
      </div>
    </header>
  )
}
