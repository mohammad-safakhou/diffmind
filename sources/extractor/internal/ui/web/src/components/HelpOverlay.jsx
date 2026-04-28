// HelpOverlay is a single-page cheat-sheet shown when the user presses '?'
// or clicks the help button in the top bar. We keep it intentionally small
// so it never goes stale.

export function HelpOverlay({ onClose }) {
  return (
    <div
      onClick={onClose}
      style="position: fixed; inset: 0; background: rgba(5, 8, 17, 0.85); z-index: 50; display: flex; align-items: center; justify-content: center; padding: 24px;"
    >
      <div
        onClick={(e) => e.stopPropagation()}
        style="max-width: 720px; width: 100%; background: var(--bg-1); border: 1px solid var(--border); border-radius: 12px; padding: 28px; box-shadow: var(--shadow); max-height: 90vh; overflow: auto;"
      >
        <div style="display:flex; justify-content: space-between; align-items: center;">
          <h2 style="margin:0; font-size: 18px;">DiffMind Dashboard</h2>
          <button class="btn secondary" onClick={onClose}>Close</button>
        </div>
        <p style="color: var(--text-muted); margin-top: 8px;">
          Live, evidence-backed extraction of exposures, dependencies, and
          conditional connections from any repository, powered by an OpenCode
          server.
        </p>

        <h3 style="margin-top: 18px; font-size: 13px; text-transform: uppercase; letter-spacing: 0.06em; color: var(--text-muted);">
          Pipeline
        </h3>
        <ol style="line-height: 1.7; margin-top: 4px;">
          <li><b>Repo facts</b> &mdash; one LLM call to fingerprint the tech stack.</li>
          <li><b>Discovery</b> &mdash; one parallel LLM call per objective (HTTP routes, queue consumers, DB ops, etc.).</li>
          <li><b>Re-examination</b> &mdash; targeted re-asks for low-signal candidates.</li>
          <li><b>Detail</b> &mdash; per-entity enrichment with method/path/queue/etc.</li>
          <li><b>Connections</b> &mdash; per-exposure traversal mapping ordered call paths to dependencies.</li>
          <li><b>Reconcile</b> &mdash; local dedup, orphan-drop, deterministic ordering.</li>
        </ol>

        <h3 style="margin-top: 18px; font-size: 13px; text-transform: uppercase; letter-spacing: 0.06em; color: var(--text-muted);">
          Keyboard shortcuts
        </h3>
        <table style="width:100%; font-size: 13px; border-collapse: collapse;">
          <tbody>
            <tr><td style="padding: 4px 8px; color: var(--text-muted);"><kbd style={kbdStyle}>?</kbd></td><td>Toggle this help</td></tr>
            <tr><td style="padding: 4px 8px; color: var(--text-muted);"><kbd style={kbdStyle}>Esc</kbd></td><td>Close help / drawer</td></tr>
          </tbody>
        </table>

        <h3 style="margin-top: 18px; font-size: 13px; text-transform: uppercase; letter-spacing: 0.06em; color: var(--text-muted);">
          Tips
        </h3>
        <ul style="line-height: 1.7;">
          <li>Click any node in the graph or any row in the activity log to inspect prompts, responses, and event history in the right drawer.</li>
          <li>The legend at the bottom-left of the graph maps node colors to job status.</li>
          <li>Pick a run from the recent-runs panel on the left to replay its full event timeline (it streams from <code>events.jsonl</code>).</li>
          <li>Watchdog and session events are visible in the activity log: when an OpenCode session asks for permission or clarification, DiffMind auto-replies so the run never deadlocks.</li>
        </ul>
      </div>
    </div>
  )
}

const kbdStyle = 'background: var(--bg-2); border: 1px solid var(--border); border-radius: 4px; padding: 1px 6px; font-family: monospace;'
