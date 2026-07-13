import { useEffect, useMemo, useState } from 'preact/hooks'
import { getPullRequestImpact, getPullRequests } from '../lib/api.js'
import { navigate } from '../lib/router.js'
import { FlowRibbon } from './FlowRibbon.jsx'

export function PullRequestsView({ pid }) {
  const [data, setData] = useState(null)
  const [selected, setSelected] = useState(null)
  const [impact, setImpact] = useState(null)
  const [repoFilter, setRepoFilter] = useState('')
  const [query, setQuery] = useState('')
  const [loading, setLoading] = useState(true)
  const [impactLoading, setImpactLoading] = useState(false)
  const [error, setError] = useState('')
  const [impactError, setImpactError] = useState('')

  const refresh = async () => {
    setLoading(true)
    setError('')
    try {
      const next = await getPullRequests(pid)
      setData(next)
      const all = flattenPulls(next)
      setSelected((current) => all.find((pr) => samePR(pr, current)) || all[0] || null)
    } catch (e) {
      setError(e.message)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { refresh() }, [pid])
  useEffect(() => {
    if (!selected) {
      setImpact(null)
      return
    }
    let cancelled = false
    setImpactLoading(true)
    setImpactError('')
    setImpact(null)
    getPullRequestImpact(pid, selected.repo_id, selected.number)
      .then((next) => { if (!cancelled) setImpact(next) })
      .catch((e) => { if (!cancelled) setImpactError(e.message) })
      .finally(() => { if (!cancelled) setImpactLoading(false) })
    return () => { cancelled = true }
  }, [pid, selected?.repo_id, selected?.number])

  const pulls = useMemo(() => {
    const q = query.trim().toLowerCase()
    return flattenPulls(data).filter((pr) => {
      if (repoFilter && pr.repo_id !== repoFilter) return false
      if (!q) return true
      return `${pr.title} ${pr.repo_name} ${pr.author} ${(pr.labels || []).join(' ')}`.toLowerCase().includes(q)
    })
  }, [data, repoFilter, query])

  const activeRepos = (data?.repositories || []).filter((repo) => repo.open_count > 0)
  const impacted = impact?.company?.services || []
  const companyCount = impacted.length

  return (
    <div class="pr-page">
      <header class="pr-topbar">
        <div class="pr-heading">
          <button class="btn ghost tiny" onClick={() => navigate(`/projects/${encodeURIComponent(pid)}`)}>← Graph</button>
          <div>
            <h1>Pull request impact</h1>
            <p>Code change evidence, overlaid on the current company graph.</p>
          </div>
        </div>
        <div class="pr-top-actions">
          <span class="pr-estimate-badge">Estimated impact</span>
          <button class="btn ghost" disabled={loading} onClick={refresh}>{loading ? 'Refreshing…' : 'Refresh GitHub'}</button>
        </div>
      </header>

      {error && <div class="banner error pr-banner">{error}</div>}
      <section class="pr-kpis">
        <Metric value={data?.total_open ?? '—'} label="Open PRs" tone="blue" />
        <Metric value={activeRepos.length} label="Repos with PRs" tone="cyan" />
        <Metric value={data?.repo_count ?? '—'} label="Repositories checked" />
        <Metric value={impact ? `${impact.risk_score}/100` : '—'} label="Selected risk" tone={impact?.risk_level} />
        <Metric value={impact?.company?.available ? companyCount : '—'} label="Impacted services" tone={companyCount > 3 ? 'high' : 'green'} />
      </section>

      <div class="pr-workspace">
        <aside class="pr-repos">
          <div class="pr-section-head"><h2>Repositories</h2><span>{data?.total_open || 0}</span></div>
          <button class={'pr-repo-row ' + (!repoFilter ? 'active' : '')} onClick={() => setRepoFilter('')}>
            <span>All open pull requests</span><strong>{data?.total_open || 0}</strong>
          </button>
          {(data?.repositories || []).map((repo) => (
            <button key={repo.repo_id} class={'pr-repo-row ' + (repoFilter === repo.repo_id ? 'active' : '')} onClick={() => setRepoFilter(repo.repo_id)}>
              <span><b>{repo.repo_name}</b><small>{repo.team || 'default'} · {repo.status}</small></span>
              <strong>{repo.open_count}</strong>
            </button>
          ))}
          {(data?.error_count || 0) > 0 && <div class="pr-provider-note">{data.error_count} repositories could not be read. Check GitHub authentication and repository URLs.</div>}
        </aside>

        <section class="pr-list-panel">
          <div class="pr-list-tools">
            <input value={query} onInput={(e) => setQuery(e.currentTarget.value)} placeholder="Search title, author, label…" />
            <span>{pulls.length} shown</span>
          </div>
          <div class="pr-list">
            {loading && <LoadingBlock label="Loading open pull requests from GitHub…" />}
            {!loading && pulls.length === 0 && <EmptyBlock label="No open pull requests match this view." />}
            {pulls.map((pr) => (
              <button key={`${pr.repo_id}/${pr.number}`} class={'pr-card ' + (samePR(pr, selected) ? 'active' : '')} onClick={() => setSelected(pr)}>
                <div class="pr-card-top"><span>{pr.repo_name} <b>#{pr.number}</b></span>{pr.draft && <em>Draft</em>}</div>
                <strong>{pr.title}</strong>
                <div class="pr-card-meta"><span>@{pr.author || 'unknown'}</span><span>{relativeDate(pr.updated_at)}</span></div>
                {(pr.labels || []).length > 0 && <div class="pr-labels">{pr.labels.slice(0, 4).map((label) => <span key={label}>{label}</span>)}</div>}
              </button>
            ))}
          </div>
        </section>

        <main class="pr-impact-panel">
          {!selected && <EmptyBlock label="Select a pull request to calculate its impact." />}
          {selected && impactLoading && <LoadingBlock label="Reading changed files and walking the company graph…" />}
          {impactError && <div class="banner error">{impactError}</div>}
          {selected && impact && <ImpactDetail impact={impact} />}
        </main>
      </div>
    </div>
  )
}

function ImpactDetail({ impact }) {
  const pr = impact.pull_request
  const code = impact.codebase
  const company = impact.company
  return (
    <div class="pr-impact-detail">
      <header class="pr-impact-header">
        <div>
          <div class="pr-impact-eyebrow">{pr.repo_name} · #{pr.number}</div>
          <h2>{pr.title}</h2>
          <div class="pr-branch"><code>{pr.head}</code><span>→</span><code>{pr.base}</code></div>
        </div>
        <div class={'pr-risk ' + impact.risk_level}>
          <strong>{impact.risk_score}</strong><span>{impact.risk_level} risk</span>
        </div>
      </header>
      <div class="pr-impact-actions">
        <span>Opened by @{pr.author || 'unknown'}</span>
        <a class="btn ghost" href={pr.url} target="_blank" rel="noreferrer">Open on GitHub ↗</a>
      </div>

      <section class="pr-impact-section">
        <SectionTitle title="Codebase impact" subtitle="Evidence from the PR file list and available diff patches" />
        <div class="pr-mini-kpis">
          <MiniMetric value={code.changed_files} label="files" />
          <MiniMetric value={`+${code.additions}`} label="additions" tone="green" />
          <MiniMetric value={`−${code.deletions}`} label="deletions" tone="red" />
          <MiniMetric value={code.commits} label="commits" />
        </div>
        <div class="pr-category-grid">
          {(code.categories || []).map((cat) => (
            <div class={'pr-category ' + cat.id} key={cat.id}>
              <span>{cat.label}</span><strong>{cat.files}</strong><small>+{cat.additions} −{cat.deletions}</small>
            </div>
          ))}
        </div>
        {(code.signals || []).length > 0 && (
          <div class="pr-signals">
            {(code.signals || []).map((signal) => <div class={'pr-signal ' + signal.severity} key={signal.kind}><b>{signal.label}</b><span>{signal.files.length} file{signal.files.length === 1 ? '' : 's'}</span></div>)}
          </div>
        )}
        <div class="pr-reasons">
          <h3>Why this score</h3>
          <ul>{(code.risk_reasons || []).map((reason) => <li key={reason}>{reason}</li>)}</ul>
        </div>
        <details class="pr-files" open>
          <summary>Changed files <span>{code.files?.length || 0}</span></summary>
          <div class="pr-file-list">
            {(code.files || []).map((file) => (
              <div class="pr-file" key={file.path}>
                <span class={'file-status ' + file.status}>{String(file.status || 'M').slice(0, 1).toUpperCase()}</span>
                <code title={file.path}>{file.path}</code>
                <span class="file-category">{file.category}</span>
                <span class="file-delta"><b>+{file.additions}</b> <i>−{file.deletions}</i></span>
              </div>
            ))}
          </div>
        </details>
      </section>

      <section class="pr-impact-section company">
        <SectionTitle title="Company impact" subtitle="Reverse dependency blast radius from the latest completed graph" />
        {!company.available ? (
          <div class="pr-company-empty"><strong>Company impact unavailable</strong><p>{(company.notes || []).join(' ')}</p></div>
        ) : (
          <>
            <div class="pr-company-summary">
              <div><span>Changed service</span><strong>{company.root_service}</strong></div>
              <div><span>Directly affected</span><strong>{company.direct_services}</strong></div>
              <div><span>Indirectly affected</span><strong>{company.indirect_services}</strong></div>
              <div><span>Teams in radius</span><strong>{company.teams?.length || 0}</strong></div>
              <div><span>Resources touched</span><strong>{company.resources?.length || 0}</strong></div>
            </div>
            <div class="pr-impact-columns">
              <div>
                <h3>Services</h3>
                <div class="pr-service-list">{(company.services || []).map((service) => <div key={service.name}><strong>{service.name}</strong><span>{service.team || 'default'} · hop {service.depth}</span></div>)}</div>
              </div>
              <div>
                <h3>Teams & resources</h3>
                <div class="pr-tags">{(company.teams || []).map((team) => <span key={team}>Team · {team}</span>)}{(company.resources || []).slice(0, 12).map((resource) => <span key={resource}>{resource}</span>)}</div>
              </div>
            </div>
            {company.flow && <div class="pr-flow"><FlowRibbon flow={company.flow} /></div>}
            <p class="pr-confidence">Confidence: graph estimate. {(company.notes || []).join(' ')}</p>
          </>
        )}
      </section>
    </div>
  )
}

function Metric({ value, label, tone = '' }) {
  return <div class={'pr-kpi ' + (tone || '')}><strong>{value}</strong><span>{label}</span></div>
}

function MiniMetric({ value, label, tone = '' }) {
  return <div class={'pr-mini-kpi ' + tone}><strong>{value}</strong><span>{label}</span></div>
}

function SectionTitle({ title, subtitle }) {
  return <div class="pr-section-title"><div><h2>{title}</h2><p>{subtitle}</p></div></div>
}

function LoadingBlock({ label }) {
  return <div class="pr-state"><div class="activity-spinner" /><p>{label}</p></div>
}

function EmptyBlock({ label }) {
  return <div class="pr-state"><p>{label}</p></div>
}

function flattenPulls(data) {
  return (data?.repositories || []).flatMap((repo) => repo.pull_requests || []).sort((a, b) => String(b.updated_at).localeCompare(String(a.updated_at)))
}

function samePR(a, b) {
  return Boolean(a && b && a.repo_id === b.repo_id && a.number === b.number)
}

function relativeDate(value) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return 'unknown'
  const seconds = Math.max(1, Math.round((Date.now() - date.getTime()) / 1000))
  if (seconds < 60) return 'just now'
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`
  if (seconds < 86400 * 30) return `${Math.floor(seconds / 86400)}d ago`
  return date.toLocaleDateString()
}
