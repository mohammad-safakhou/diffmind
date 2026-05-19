import { useState, useMemo, useEffect } from 'preact/hooks'
import { startRun } from '../lib/api.js'
import { runMeta } from '../lib/store.js'

// Default form values are persisted in localStorage so re-runs are quick.
//
// STORAGE_KEY is versioned. Bump the suffix whenever the saved shape
// changes in a way that could silently miscompute on the server.
// Specifically: schema bumps that ADD a field are fine (load() will
// fall back to the new default), but RENAMING or CHANGING the
// SEMANTICS of an existing field requires a bump so old saved values
// don't poison new runs.
//
// v2 (this version): bumped because the old form had
// opencode.timeout_seconds: 300 baked in. Users with that saved
// kept hitting the 300-second wall even after the SPA was patched
// to default to 0, because the localStorage merge was shallow.
// Bumping the key forces a clean slate; we also delete the old key
// on first load so it doesn't sit around forever.
const STORAGE_KEY = 'diffmind:form-defaults-v2'
const LEGACY_STORAGE_KEYS = ['diffmind:form-defaults']

// `timeout_seconds` is the raw http.Client.Timeout on the transport.
// We DELIBERATELY do not surface it as a primary control — the
// liveness watchdog (`idle_timeout_seconds`) is what catches stuck
// calls. The transport timeout is now a 4-hour fail-safe; sending 0
// from the form means "use the server's default", which is what we
// want for ~all use cases. The CLI flag is still available for power
// users.
const DEFAULTS = {
  repo_path: '',
  opencode: {
    base_url: 'http://127.0.0.1:4096',
    username: 'opencode',
    password: '',
    provider_id: 'anthropic',
    model_id: 'claude-sonnet-4-5',
    model_variant: 'high',
    timeout_seconds: 0, // 0 = use server default (4h fail-safe)
  },
  runtime: {
    workers: 6,
    max_catalog_items: 80,
    reuse_opencode_session: false,
    cleanup_opencode_sessions: false,
    opencode_delete_delay_seconds: 5,
    skip_reexamination: false,
    // Liveness watchdog: this is the real "wait at most N seconds
    // with no observable progress before aborting" control.
    idle_timeout_seconds: 120,
    max_call_seconds: 1800,
    liveness_poll_seconds: 5,
  },
  quality: { min_confidence: 0.7 },
}

// deepMerge produces a copy of `base` with values from `over` layered
// on top, recursing into plain objects. Crucially, this is NOT a
// shallow spread: a shallow merge of {opencode: {...}} over
// DEFAULTS.opencode would replace the entire opencode block,
// dropping any fields that were added in a newer DEFAULTS version.
// We saw that exact bug let stale opencode.timeout_seconds: 300
// survive a SPA refresh and re-cripple a run.
function deepMerge(base, over) {
  if (over === null || typeof over !== 'object' || Array.isArray(over)) {
    return over === undefined ? base : over
  }
  const out = Array.isArray(base) ? [...base] : { ...base }
  for (const k of Object.keys(over)) {
    const bv = base ? base[k] : undefined
    if (bv && typeof bv === 'object' && !Array.isArray(bv)) {
      out[k] = deepMerge(bv, over[k])
    } else {
      out[k] = over[k]
    }
  }
  return out
}

function load() {
  // First, evict any legacy storage entries so the form never reads
  // them again. We do this regardless of whether the current key is
  // present.
  try {
    for (const legacy of LEGACY_STORAGE_KEYS) localStorage.removeItem(legacy)
  } catch {}
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return JSON.parse(JSON.stringify(DEFAULTS))
    const saved = JSON.parse(raw)
    return deepMerge(JSON.parse(JSON.stringify(DEFAULTS)), saved)
  } catch {
    return JSON.parse(JSON.stringify(DEFAULTS))
  }
}

function save(form) {
  try { localStorage.setItem(STORAGE_KEY, JSON.stringify(form)) } catch {}
}

export function RunForm({ onLaunched }) {
  const [form, setForm] = useState(load)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [advanced, setAdvanced] = useState(false)

  const meta = runMeta.value
  const running = meta?.status === 'running' || meta?.status === 'cancelling'

  const update = (path, value) => {
    setForm((f) => {
      const next = { ...f }
      const parts = path.split('.')
      let cur = next
      for (let i = 0; i < parts.length - 1; i++) {
        cur[parts[i]] = { ...cur[parts[i]] }
        cur = cur[parts[i]]
      }
      cur[parts[parts.length - 1]] = value
      return next
    })
  }

  useEffect(() => { save(form) }, [form])

  const cli = useMemo(() => buildCLI(form), [form])

  const submit = async () => {
    setError('')
    // Pre-flight validation. We surface clear messages instead of letting
    // the server reject the request.
    if (!form.repo_path?.trim()) {
      setError('Repository path is required.')
      return
    }
    if (!/^[\\/~]/.test(form.repo_path.trim())) {
      // basic sanity: most absolute paths start with / or ~
      setError('Repository path should be an absolute path.')
      return
    }
    if (!form.opencode.base_url?.trim()) {
      setError('OpenCode URL is required.')
      return
    }
    if (!form.opencode.provider_id?.trim() || !form.opencode.model_id?.trim()) {
      setError('Provider id and model id are required (run `opencode auth login` first).')
      return
    }
    setBusy(true)
    try {
      const res = await startRun(form)
      if (onLaunched) onLaunched(res.run_id)
    } catch (e) {
      const msg = e.message || String(e)
      // Server returned a 4xx with a hint; show it verbatim. Common cases:
      //   - opencode-url missing
      //   - repo_path inaccessible
      //   - "another run is already in progress"
      setError(msg)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div class="form">
      <h2>New Run</h2>

      <div class="field">
        <label>Repository absolute path</label>
        <input value={form.repo_path} onInput={(e) => update('repo_path', e.target.value)} placeholder="/abs/path/to/repo" disabled={running} />
      </div>

      <div class="row-2">
        <div class="field">
          <label>OpenCode URL</label>
          <input value={form.opencode.base_url} onInput={(e) => update('opencode.base_url', e.target.value)} disabled={running} />
        </div>
        <div class="field">
          <label title="Maximum seconds a prompt can go without observable progress on the OpenCode session before the liveness watchdog aborts it. The agent making tool calls or producing reasoning resets this clock. 0 = use server default (120s).">
            Idle timeout (sec)
          </label>
          <input type="number" value={form.runtime.idle_timeout_seconds} onInput={(e) => update('runtime.idle_timeout_seconds', Number(e.target.value))} disabled={running} />
        </div>
      </div>

      <div class="row-2">
        <div class="field">
          <label>Username</label>
          <input value={form.opencode.username} onInput={(e) => update('opencode.username', e.target.value)} disabled={running} />
        </div>
        <div class="field">
          <label>Password</label>
          <input type="password" value={form.opencode.password} onInput={(e) => update('opencode.password', e.target.value)} disabled={running} />
        </div>
      </div>

      <div class="row-3">
        <div class="field">
          <label>Provider</label>
          <input value={form.opencode.provider_id} onInput={(e) => update('opencode.provider_id', e.target.value)} disabled={running} />
        </div>
        <div class="field">
          <label>Model</label>
          <input value={form.opencode.model_id} onInput={(e) => update('opencode.model_id', e.target.value)} disabled={running} />
        </div>
        <div class="field">
          <label>Variant</label>
          <select value={form.opencode.model_variant} onInput={(e) => update('opencode.model_variant', e.target.value)} disabled={running}>
            <option value="">default</option>
            <option value="low">low</option>
            <option value="medium">medium</option>
            <option value="high">high</option>
            <option value="max">max</option>
          </select>
        </div>
      </div>

      <div class="row-3">
        <div class="field">
          <label>Workers</label>
          <input type="number" value={form.runtime.workers} onInput={(e) => update('runtime.workers', Number(e.target.value))} disabled={running} />
        </div>
        <div class="field">
          <label>Catalog batch</label>
          <input type="number" value={form.runtime.max_catalog_items} onInput={(e) => update('runtime.max_catalog_items', Number(e.target.value))} disabled={running} />
        </div>
        <div class="field">
          <label>Min confidence</label>
          <input type="number" step="0.05" min="0" max="1" value={form.quality.min_confidence} onInput={(e) => update('quality.min_confidence', Number(e.target.value))} disabled={running} />
        </div>
      </div>

      <button class="btn secondary" type="button" onClick={() => setAdvanced((v) => !v)}>
        {advanced ? '\u25BE' : '\u25B8'} Advanced
      </button>

      {advanced && (
        <div style="display:flex; flex-direction:column; gap: 8px; padding-left: 4px; border-left: 2px solid var(--border);">
          <div class="row-3">
            <div class="field">
              <label title="Hard ceiling on a single LLM call. Even with continuous progress the watchdog aborts past this. 0 = use server default (1800s = 30min).">
                Max call (sec)
              </label>
              <input type="number" value={form.runtime.max_call_seconds} onInput={(e) => update('runtime.max_call_seconds', Number(e.target.value))} disabled={running} />
            </div>
            <div class="field">
              <label title="How often the liveness watchdog polls OpenCode for progress. Default 5s.">
                Liveness poll (sec)
              </label>
              <input type="number" value={form.runtime.liveness_poll_seconds} onInput={(e) => update('runtime.liveness_poll_seconds', Number(e.target.value))} disabled={running} />
            </div>
            <div class="field">
              <label title="Raw http.Client.Timeout on the transport. This is a fail-safe; the primary control is Idle timeout above. Default 4h. 0 = server default.">
                Transport timeout (sec)
              </label>
              <input type="number" value={form.opencode.timeout_seconds} onInput={(e) => update('opencode.timeout_seconds', Number(e.target.value))} disabled={running} />
            </div>
          </div>
          <div class="toggle">
            <input type="checkbox" id="reuse" checked={form.runtime.reuse_opencode_session} onInput={(e) => update('runtime.reuse_opencode_session', e.target.checked)} disabled={running} />
            <label for="reuse">Reuse one OpenCode session per run</label>
          </div>
          <div class="toggle">
            <input type="checkbox" id="cleanup" checked={form.runtime.cleanup_opencode_sessions} onInput={(e) => update('runtime.cleanup_opencode_sessions', e.target.checked)} disabled={running} />
            <label for="cleanup">Cleanup OpenCode sessions after run</label>
          </div>
          <div class="toggle">
            <input type="checkbox" id="skip-reex" checked={form.runtime.skip_reexamination} onInput={(e) => update('runtime.skip_reexamination', e.target.checked)} disabled={running} />
            <label for="skip-reex">Skip Stage 2 (re-examination)</label>
          </div>
        </div>
      )}

      <div class="actions">
        <button class="btn" onClick={submit} disabled={running || busy}>
          {busy ? 'Starting…' : (running ? 'Run in progress…' : 'Run extraction')}
        </button>
        <button class="btn secondary" onClick={() => { setForm(load()); }} disabled={running}>Reset</button>
      </div>

      {error && <div class="banner error">{error}</div>}

      <details>
        <summary>Equivalent CLI</summary>
        <pre class="cli-preview">{cli}</pre>
      </details>
    </div>
  )
}

function buildCLI(f) {
  const parts = ['go run ./cmd/diffmind run', `  --repo ${q(f.repo_path || '<repo>')}`]
  if (f.opencode.base_url) parts.push(`  --opencode-url ${q(f.opencode.base_url)}`)
  if (f.opencode.username) parts.push(`  --opencode-username ${q(f.opencode.username)}`)
  if (f.opencode.password) parts.push(`  --opencode-password ${q('***')}`)
  if (f.opencode.provider_id) parts.push(`  --provider-id ${q(f.opencode.provider_id)}`)
  if (f.opencode.model_id) parts.push(`  --model-id ${q(f.opencode.model_id)}`)
  if (f.opencode.model_variant) parts.push(`  --model-variant ${f.opencode.model_variant}`)
  if (f.opencode.timeout_seconds) parts.push(`  --opencode-timeout-seconds ${f.opencode.timeout_seconds}`)
  if (f.runtime.workers) parts.push(`  --workers ${f.runtime.workers}`)
  if (f.runtime.max_catalog_items) parts.push(`  --max-catalog-items ${f.runtime.max_catalog_items}`)
  if (f.runtime.idle_timeout_seconds) parts.push(`  --idle-timeout-seconds ${f.runtime.idle_timeout_seconds}`)
  if (f.runtime.max_call_seconds) parts.push(`  --max-call-seconds ${f.runtime.max_call_seconds}`)
  if (f.runtime.liveness_poll_seconds) parts.push(`  --liveness-poll-seconds ${f.runtime.liveness_poll_seconds}`)
  if (f.runtime.reuse_opencode_session) parts.push('  --reuse-opencode-session')
  if (f.runtime.cleanup_opencode_sessions) parts.push('  --cleanup-opencode-sessions')
  if (f.runtime.skip_reexamination) parts.push('  --skip-reexamination')
  if (f.quality.min_confidence) parts.push(`  --min-confidence ${f.quality.min_confidence}`)
  return parts.join(' \\\n')
}

function q(s) {
  if (!s) return ''
  if (/[\s'"$]/.test(s)) return "'" + s.replace(/'/g, "'\\''") + "'"
  return s
}
