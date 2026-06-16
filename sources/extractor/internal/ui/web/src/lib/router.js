// Minimal hash-based router. Hash routing keeps the SPA a single static
// bundle with no server-side route rewrites — refreshing any URL just reloads
// index.html and the hash is re-read on boot, so deep links survive a refresh.
//
// Routes:
//   #/                 → canonical architecture catalog
//   #/runs             → automation runs dashboard
//   #/runs/<run_id>    → per-run detail view
import { useEffect, useState } from 'preact/hooks'

// currentPath returns the normalised hash path, always starting with '/'.
export function currentPath() {
  const h = window.location.hash || '#/'
  const p = h.replace(/^#/, '')
  return p.startsWith('/') ? p : '/' + p
}

// navigate sets the hash, which triggers the hashchange listeners below.
export function navigate(path) {
  const target = path.startsWith('/') ? path : '/' + path
  if (currentPath() === target) return
  window.location.hash = target
}

// useRoute re-renders the subscribing component whenever the hash changes and
// returns a small parsed route object.
export function useRoute() {
  const [path, setPath] = useState(currentPath())
  useEffect(() => {
    const onChange = () => setPath(currentPath())
    window.addEventListener('hashchange', onChange)
    return () => window.removeEventListener('hashchange', onChange)
  }, [])
  return parseRoute(path)
}

// parseRoute maps a path to { name, runID }.
export function parseRoute(path) {
  const m = path.match(/^\/runs\/([^/]+)$/)
  if (m) return { name: 'detail', runID: decodeURIComponent(m[1]) }
  if (path === '/runs') return { name: 'runs' }
  return { name: 'architecture' }
}
