import { useEffect, useState } from 'preact/hooks'
import { onAuthFailure, getToken, setToken } from './lib/api.js'
import { useRoute } from './lib/router.js'
import { Home } from './views/Home.jsx'
import { Detail } from './views/Detail.jsx'

// App is the router shell. It switches between the runs dashboard (home) and a
// per-run detail view based on the URL hash, and surfaces the auth banner when
// the server rejects a request with 401.
export function App() {
  const route = useRoute()
  const [authError, setAuthError] = useState(false)

  useEffect(() => {
    onAuthFailure(() => setAuthError(true))
  }, [])

  return (
    <div>
      {authError && (
        <AuthBanner
          onSubmit={(t) => { setToken(t); setAuthError(false); window.location.reload() }}
          initial={getToken()}
        />
      )}
      {route.name === 'detail'
        ? <Detail runID={route.runID} key={route.runID} />
        : <Home />}
    </div>
  )
}

// AuthBanner shows when the server returned 401. The user pastes their shared
// secret; we persist it client-side and reload.
function AuthBanner({ onSubmit, initial }) {
  return (
    <div style="background: rgba(245, 158, 11, 0.12); border-bottom: 1px solid var(--warn); padding: 10px 16px; display: flex; gap: 10px; align-items: center;">
      <strong style="color: var(--warn);">Authentication required</strong>
      <span style="color: var(--text-muted); font-size: 12px;">
        Paste the token printed by <code>diffmind ui --ui-token</code> (or set <code>DIFFMIND_UI_TOKEN</code>).
      </span>
      <form
        style="display:flex; gap:8px; margin-left:auto;"
        onSubmit={(e) => {
          e.preventDefault()
          const v = e.target.elements.token.value
          if (v) onSubmit(v)
        }}
      >
        <input name="token" type="password" placeholder="ui token" defaultValue={initial} style="padding: 4px 8px; border-radius: 6px; background: var(--bg-2); border: 1px solid var(--border); color: var(--text);" />
        <button class="btn" type="submit">Save</button>
      </form>
    </div>
  )
}
