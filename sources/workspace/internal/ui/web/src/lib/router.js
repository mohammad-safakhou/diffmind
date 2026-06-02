// Hash router with parameterised routes. Hash routing keeps the SPA a single
// static bundle: refreshing any deep link reloads index.html and the hash is
// re-parsed on boot.
//
// Routes:
//   #/                              project index
//   #/projects/{pid}                project home (tabs)
//   #/projects/{pid}/runs/{rid}     graph viewer
import { useEffect, useState } from 'preact/hooks'

export function currentPath() {
  const h = window.location.hash || '#/'
  const p = h.replace(/^#/, '')
  return p.startsWith('/') ? p : '/' + p
}

export function navigate(path) {
  const target = path.startsWith('/') ? path : '/' + path
  if (currentPath() === target) return
  window.location.hash = target
}

export function useRoute() {
  const [path, setPath] = useState(currentPath())
  useEffect(() => {
    const onChange = () => setPath(currentPath())
    window.addEventListener('hashchange', onChange)
    return () => window.removeEventListener('hashchange', onChange)
  }, [])
  return parseRoute(path)
}

export function parseRoute(path) {
  let m = path.match(/^\/projects\/([^/]+)\/runs\/([^/]+)$/)
  if (m) return { name: 'run', pid: decodeURIComponent(m[1]), rid: decodeURIComponent(m[2]) }
  m = path.match(/^\/projects\/([^/]+)$/)
  if (m) return { name: 'project', pid: decodeURIComponent(m[1]) }
  return { name: 'projects' }
}
