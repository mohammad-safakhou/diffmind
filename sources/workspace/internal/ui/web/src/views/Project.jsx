import { useEffect, useState } from 'preact/hooks'
import { getProject } from '../lib/api.js'
import { navigate } from '../lib/router.js'
import { RunsTab } from './tabs/RunsTab.jsx'
import { ReposTab } from './tabs/ReposTab.jsx'
import { BlueprintsTab } from './tabs/BlueprintsTab.jsx'
import { SettingsTab } from './tabs/SettingsTab.jsx'

const TABS = ['Runs', 'Repos', 'Blueprints', 'Settings']

// Project is the per-project home with four tabs. The active tab is kept in
// local state (not the URL) since the tabs share the same project route.
export function Project({ pid }) {
  const [project, setProject] = useState(null)
  const [error, setError] = useState('')
  const [tab, setTab] = useState('Runs')

  const refresh = async () => {
    try { setProject(await getProject(pid)); setError('') }
    catch (e) { setError(e.message) }
  }
  useEffect(() => { refresh() }, [pid])

  return (
    <div class="page">
      <header class="topbar">
        <div class="crumbs">
          <button class="btn ghost tiny" onClick={() => navigate('/')}>← Projects</button>
          <h1>{project ? project.name : pid}</h1>
        </div>
      </header>

      {error && <div class="banner error">{error}</div>}

      <nav class="tabs">
        {TABS.map((t) => (
          <button key={t} class={'tab' + (tab === t ? ' active' : '')} onClick={() => setTab(t)}>{t}</button>
        ))}
      </nav>

      <div class="content">
        {tab === 'Runs' && <RunsTab pid={pid} />}
        {tab === 'Repos' && <ReposTab pid={pid} />}
        {tab === 'Blueprints' && <BlueprintsTab pid={pid} />}
        {tab === 'Settings' && project && <SettingsTab project={project} onChanged={refresh} />}
      </div>
    </div>
  )
}
