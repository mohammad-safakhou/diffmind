import { useState, useMemo, useEffect } from 'preact/hooks'
import { startRun } from '../lib/api.js'
import { runMeta } from '../lib/store.js'

// Default form values are persisted in localStorage so re-runs are quick.
const STORAGE_KEY = 'diffmind:form-defaults'

const DEFAULTS = {
  repo_path: '',
  opencode: {
    base_url: 'http://127.0.0.1:4096',
    username: 'opencode',
    password: '',
    provider_id: 'anthropic',
    model_id: 'claude-sonnet-4-5',
    model_variant: 'high',
    timeout_seconds: 300,
  },
  runtime: {
    workers: 6,
    max_catalog_items: 80,
    reuse_opencode_session: false,
    cleanup_opencode_sessions: false,
    opencode_delete_delay_seconds: 5,
    skip_reexamination: false,
  },
  quality: { min_confidence: 0.7 },
}

function load() {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return JSON.parse(JSON.stringify(DEFAULTS))
    return { ...JSON.parse(JSON.stringify(DEFAULTS)), ...JSON.parse(raw) }
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
          <label>Timeout (sec)</label>
          <input type="number" value={form.opencode.timeout_seconds} onInput={(e) => update('opencode.timeout_seconds', Number(e.target.value))} disabled={running} />
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
