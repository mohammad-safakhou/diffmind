import { useEffect, useState } from 'preact/hooks'
import { preflight } from '../lib/store.js'
import { getPreflight } from '../lib/api.js'

// SystemStatus is the always-visible header strip that surfaces
// host-level readiness: Docker, OpenCode, credentials, disk, etc.
// It polls /api/preflight every 15 seconds; the server has its
// own 30-second ticker so a worst case the user sees a result
// up to 30s old. The expanded panel lists every check with its
// remediation.
//
// The Run button (in RunForm) reads the same preflight signal
// and is hard-disabled when overall === 'fail'.
export function SystemStatus() {
  const [open, setOpen] = useState(false)
  const rep = preflight.value

  useEffect(() => {
    let cancelled = false
    const tick = async () => {
      try {
        const r = await getPreflight()
        if (cancelled) return
        preflight.value = r
      } catch {
        /* ignore network blips; next tick will retry */
      }
    }
    tick()
    const interval = setInterval(tick, 15_000)
    const onVis = () => {
      if (document.visibilityState === 'visible') tick()
    }
    document.addEventListener('visibilitychange', onVis)
    return () => {
      cancelled = true
      clearInterval(interval)
      document.removeEventListener('visibilitychange', onVis)
    }
  }, [])

  if (!rep) {
    return (
      <div class={'system-status loading'}>
        <span class="system-status-pill">System status: checking…</span>
      </div>
    )
  }

  const overall = rep.overall || 'ok'
  return (
    <div class={'system-status ' + overall}>
      <button
        type="button"
        class={'system-status-pill ' + overall}
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        title={summaryTitle(rep)}
      >
        <span class={'dot ' + overall} />
        System status: <strong>{label(overall)}</strong>
        <span class="checks-summary">
          {countBy(rep.checks, 'ok')} ok · {countBy(rep.checks, 'warn')} warn · {countBy(rep.checks, 'fail')} fail
        </span>
        <span class="caret">{open ? '\u25BE' : '\u25B8'}</span>
      </button>
      {open && (
        <div class="system-status-detail">
          <table>
            <thead>
              <tr>
                <th>Check</th>
                <th>Status</th>
                <th>Message</th>
              </tr>
            </thead>
            <tbody>
              {(rep.checks || []).map((c) => (
                <tr key={c.name} class={'row-' + c.severity}>
                  <td>{c.title || c.name}</td>
                  <td>
                    <span class={'dot ' + c.severity} />{' '}
                    {label(c.severity)}
                  </td>
                  <td>
                    <div class="msg">{c.message}</div>
                    {c.detail && <div class="detail">{c.detail}</div>}
                    {c.remediation && (
                      <div class="remediation">{c.remediation}</div>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

function label(sev) {
  if (sev === 'ok') return 'Ready'
  if (sev === 'warn') return 'Degraded'
  if (sev === 'fail') return 'Blocked'
  return sev
}

function summaryTitle(rep) {
  if (!rep || !rep.checks) return ''
  const parts = rep.checks.map((c) => `${c.title || c.name}: ${c.message}`)
  return parts.join('\n')
}

function countBy(checks, sev) {
  if (!Array.isArray(checks)) return 0
  return checks.filter((c) => c.severity === sev).length
}
