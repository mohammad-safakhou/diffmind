import { useState, useMemo, useEffect } from 'preact/hooks'
import { startRun } from '../lib/api.js'
import { runMeta, preflight } from '../lib/store.js'

const STORAGE_KEY = 'diffmind:form-defaults-v7'
const LEGACY_STORAGE_KEYS = [
  'diffmind:form-defaults',
  'diffmind:form-defaults-v2',
  'diffmind:form-defaults-v3',
  'diffmind:form-defaults-v4',
  'diffmind:form-defaults-v5',
  'diffmind:form-defaults-v6',
]

const DEFAULTS = {
  repo_path: '',
  runtime: {
    workers: 6,
  },
  quality: {
    min_confidence: 0.7,
  },
}

function deepMerge(base, over) {
  if (over === null || typeof over !== 'object' || Array.isArray(over)) {
    return over === undefined ? base : over
  }
  const out = Array.isArray(base) ? [...base] : { ...base }
  for (const k of Object.keys(over)) {
    const bv = base ? base[k] : undefined
    if (bv && typeof bv === 'object' && !Array.isArray(bv)) {
      out[k] = deepMerge(bv, over[k])
    } else if (k in out) {
      out[k] = over[k]
    }
  }
  return out
}

function sanitizePrefill(p) {
  const out = {}
  if (p?.runtime) {
    out.runtime = {}
    if (p.runtime.workers !== undefined) out.runtime.workers = p.runtime.workers
  }
  if (p?.quality) {
    out.quality = {}
    if (p.quality.min_confidence !== undefined) out.quality.min_confidence = p.quality.min_confidence
  }
  return out
}

function load(prefill) {
  try {
    for (const legacy of LEGACY_STORAGE_KEYS) localStorage.removeItem(legacy)
  } catch {}

  let base = JSON.parse(JSON.stringify(DEFAULTS))
  if (prefill && typeof prefill === 'object') {
    base = deepMerge(base, sanitizePrefill(prefill))
  }
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return base
    return deepMerge(base, JSON.parse(raw))
  } catch {
    return base
  }
}

function save(form) {
  try { localStorage.setItem(STORAGE_KEY, JSON.stringify(form)) } catch {}
}

export function RunForm({ onLaunched, prefill, gateOnActiveRun = true }) {
  const [form, setForm] = useState(() => load(prefill))
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const meta = runMeta.value
  const running = gateOnActiveRun && (meta?.status === 'running' || meta?.status === 'cancelling')
  const pf = preflight.value
  const preflightBlocked = pf && pf.overall === 'fail'

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
    const repoPath = form.repo_path?.trim()
    if (!repoPath) {
      setError('Repository path is required.')
      return
    }
    if (!/^[\\/~]/.test(repoPath)) {
      setError('Repository path should be an absolute path.')
      return
    }
    setBusy(true)
    try {
      const payload = {
        repo_path: repoPath,
        runtime: { workers: Number(form.runtime.workers) || 0 },
        quality: { min_confidence: Number(form.quality.min_confidence) || 0 },
      }
      const res = await startRun(payload)
      if (onLaunched) onLaunched(res.run_id)
    } catch (e) {
      setError(e.message || String(e))
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
          <label>Workers</label>
          <input type="number" min="1" value={form.runtime.workers} onInput={(e) => update('runtime.workers', Number(e.target.value))} disabled={running} />
        </div>
        <div class="field">
          <label>Min confidence</label>
          <input type="number" step="0.05" min="0" max="1" value={form.quality.min_confidence} onInput={(e) => update('quality.min_confidence', Number(e.target.value))} disabled={running} />
        </div>
      </div>

      <div class="actions">
        <button
          class="btn"
          onClick={submit}
          disabled={running || busy || preflightBlocked}
          title={preflightBlocked ? preflightFailReason(pf) : ''}
        >
          {busy
            ? 'Starting...'
            : running
              ? 'Run in progress...'
              : preflightBlocked
                ? 'Blocked by preflight'
                : 'Run deterministic extraction'}
        </button>
        <button class="btn secondary" onClick={() => { setForm(load(prefill)); }} disabled={running}>Reset</button>
      </div>

      {preflightBlocked && (
        <div class="banner error">
          The system is not ready: {preflightFailReason(pf)}.
        </div>
      )}

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
  if (f.runtime.workers) parts.push(`  --workers ${f.runtime.workers}`)
  if (f.quality.min_confidence) parts.push(`  --min-confidence ${f.quality.min_confidence}`)
  return parts.join(' \\\n')
}

function q(s) {
  if (!s) return ''
  if (/[\s'"$]/.test(s)) return "'" + s.replace(/'/g, "'\\''") + "'"
  return s
}

function preflightFailReason(rep) {
  if (!rep || !Array.isArray(rep.checks)) return 'preflight check failed'
  const failed = rep.checks.filter((c) => c.severity === 'fail')
  if (failed.length === 0) return 'preflight check failed'
  return failed.map((c) => (c.title || c.name) + ': ' + c.message).join('; ')
}
