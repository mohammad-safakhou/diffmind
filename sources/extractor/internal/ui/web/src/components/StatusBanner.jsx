import { useState } from 'preact/hooks'
import { runMeta } from '../lib/store.js'
import { retryRun } from '../lib/api.js'

// StatusBanner shows a one-line strip below the top bar whenever the
// current run finished in a way the user should look at: failed, cancelled,
// or "completed but with zero entities" (which almost always means a
// misconfigured provider/model).
export function StatusBanner() {
  const meta = runMeta.value
  if (!meta) return null
  if (meta.status === 'failed') {
    return <FailedBanner meta={meta} />
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

// FailedBanner renders the run-failed strip with a Retry button.
// Clicking Retry POSTs /api/runs/{id}/retry which resumes from the
// failed stage onwards (stages that completed are loaded from
// state/*.json and not re-executed). We expose a small "fresh
// credentials" panel for the canonical use case: the failure was an
// auth/quota issue, the user has new credentials, and they want
// those applied to the retry without losing earlier stages' work.
function FailedBanner({ meta }) {
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const [expand, setExpand] = useState(false)
  const [creds, setCreds] = useState({ password: '', provider_id: '', model_id: '' })

  const errorClass = meta?.errorClass || meta?.error_class || meta?.summary?.error_class || ''
  // Auth/quota are the failure classes where re-credentialing
  // before retry is the typical fix. Surface the credentials box
  // by default for those; collapse it for everything else (most
  // retries just need the original config).
  const showCredsByDefault = errorClass === 'auth' || errorClass === 'quota'

  const submit = async () => {
    setErr('')
    setBusy(true)
    try {
      // The server builds the retry config starting from
      // config.Default() and applies whatever the request supplies on
      // top. config.Default() has an empty OpenCode.BaseURL, so a
      // retry with an empty body would immediately fail with
      // "opencode-url is required". To avoid that, we hydrate the
      // request from the user's last-used form defaults stored in
      // localStorage (same key the RunForm writes to), then layer
      // the "fresh credentials" overrides on top. The user always
      // gets a meaningful retry without re-typing their entire
      // OpenCode setup.
      const saved = loadFormDefaults()
      const body = {
        opencode: { ...(saved?.opencode || {}) },
        runtime: { ...(saved?.runtime || {}) },
        quality: { ...(saved?.quality || {}) },
      }
      // Sanitize: drop empty strings so the server treats them as
      // "use default" rather than "overwrite with empty".
      for (const k of Object.keys(body.opencode)) {
        if (body.opencode[k] === '' || body.opencode[k] == null) delete body.opencode[k]
      }
      // Apply the credentials the user typed into the banner. Same
      // empty-trim rule.
      for (const k of ['password', 'provider_id', 'model_id']) {
        if ((creds[k] || '').trim()) body.opencode[k] = creds[k].trim()
      }
      await retryRun(meta.id, body)
    } catch (e) {
      setErr(e.message || String(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div class="banner-strip error">
      <strong>Run failed.</strong>
      <span>{meta.error || 'See activity log for details.'}</span>
      <div class="banner-actions">
        <button class="btn" disabled={busy} onClick={submit}>
          {busy ? 'Retrying…' : 'Retry from failed stage'}
        </button>
        <button class="btn secondary" type="button" onClick={() => setExpand((v) => !v || showCredsByDefault)}>
          {expand || showCredsByDefault ? '\u25BE' : '\u25B8'} Fresh credentials
        </button>
      </div>
      {(expand || showCredsByDefault) && (
        <div class="banner-creds">
          <div class="field">
            <label>New OpenCode password</label>
            <input
              type="password"
              placeholder="leave blank to keep existing"
              value={creds.password}
              onInput={(e) => setCreds({ ...creds, password: e.target.value })}
            />
          </div>
          <div class="field">
            <label>Override provider id</label>
            <input
              placeholder="optional (e.g. anthropic)"
              value={creds.provider_id}
              onInput={(e) => setCreds({ ...creds, provider_id: e.target.value })}
            />
          </div>
          <div class="field">
            <label>Override model id</label>
            <input
              placeholder="optional (e.g. claude-sonnet-4-5)"
              value={creds.model_id}
              onInput={(e) => setCreds({ ...creds, model_id: e.target.value })}
            />
          </div>
        </div>
      )}
      {err && <div class="banner-creds-error">{err}</div>}
      <ul class="banner-hints">
        <li>Confirm the OpenCode server is running and reachable at the URL you configured.</li>
        <li>Confirm <code>opencode auth login</code> was completed for the provider you selected.</li>
        <li>Stages that already finished are loaded from <code>state/*.json</code> — retry resumes from the failed step, not from scratch.</li>
      </ul>
    </div>
  )
}

// loadFormDefaults reads the user's last-saved RunForm config so
// the Retry button can submit a fully-populated payload (with
// OpenCode URL / provider / model) instead of a bare {} that would
// fail at "opencode-url is required". We honour both the current
// storage key AND the legacy one because a user who lands directly
// on a failed run without opening the form would otherwise have
// neither — and we definitely want Retry to "just work" right after
// a fresh upgrade.
//
// Keep this list in sync with RunForm.jsx (STORAGE_KEY +
// LEGACY_STORAGE_KEYS).
function loadFormDefaults() {
  const keys = ['diffmind:form-defaults-v2', 'diffmind:form-defaults']
  for (const k of keys) {
    try {
      const raw = localStorage.getItem(k)
      if (raw) {
        const parsed = JSON.parse(raw)
        if (parsed && typeof parsed === 'object') return parsed
      }
    } catch {
      // try the next key
    }
  }
  return null
}
