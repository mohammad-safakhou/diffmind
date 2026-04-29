import { runMeta } from '../lib/store.js'

// StatusBanner shows a one-line strip below the top bar whenever the
// current run finished in a way the user should look at: failed, cancelled,
// or "completed but with zero entities" (which almost always means a
// misconfigured provider/model).
export function StatusBanner() {
  const meta = runMeta.value
  if (!meta) return null
  if (meta.status === 'failed') {
    return (
      <div class="banner-strip error">
        <strong>Run failed.</strong>
        <span>{meta.error || 'See activity log for details.'}</span>
        <ul class="banner-hints">
          <li>Confirm the OpenCode server is running and reachable at the URL you configured.</li>
          <li>Confirm <code>opencode auth login</code> was completed for the provider you selected.</li>
          <li>Check that the provider id and model id match what you authenticated.</li>
        </ul>
      </div>
    )
  }
  if (meta.status === 'cancelled') {
    return (
      <div class="banner-strip warn">
        <strong>Run cancelled.</strong>
        <span>{meta.error || 'You stopped the run; partial artifacts (if any) are on disk.'}</span>
      </div>
    )
  }
  if (meta.status === 'completed' && meta.empty) {
    return (
      <div class="banner-strip warn">
        <strong>Run completed with no entities.</strong>
        <span>This usually means OpenCode rejected every prompt. Check the activity log for "agent_failure" rows.</span>
        <ul class="banner-hints">
          <li>Provider / model id mismatch with what <code>opencode auth list</code> reports.</li>
          <li>OpenCode server config has <code>permission</code> denying every tool the agent needs to read.</li>
          <li>The repository path is empty after our skip rules (very rare).</li>
        </ul>
      </div>
    )
  }
  return null
}
