import { useEffect, useMemo, useState } from 'preact/hooks'
import { getPullRequestImpact, getPullRequests } from '../lib/api.js'
import { navigate } from '../lib/router.js'
import { FlowRibbon } from './FlowRibbon.jsx'

const CATEGORY_HELP = {
  security: {
    title: 'Security & identity',
    description: 'Files whose paths mention authentication, authorization, permissions, policies, IAM/RBAC, OAuth, secrets, credentials, certificates, or TLS.',
  },
  api: {
    title: 'API & contracts',
    description: 'OpenAPI, Swagger, GraphQL, protobuf, controller, route, or API-path files that may change a public or service-to-service contract.',
  },
  data: {
    title: 'Data & migrations',
    description: 'Database schemas and migration files, including Flyway, Liquibase, Prisma, and SQL schema changes.',
  },
  infrastructure: {
    title: 'Infrastructure',
    description: 'Terraform, Helm, Kubernetes, Docker, and GitHub Actions workflow files that affect build or deployment behavior.',
  },
  dependencies: {
    title: 'Dependencies',
    description: 'Package manifests and lock files such as pom.xml, go.mod, package.json, and language-specific lock files.',
  },
  configuration: {
    title: 'Configuration',
    description: 'YAML, JSON, TOML, properties, environment, and other configuration files not already assigned to a higher-risk category.',
  },
  code: {
    title: 'Application code',
    description: 'Production source files that do not match a more specific API, data, security, infrastructure, or dependency category.',
  },
  tests: {
    title: 'Tests',
    description: 'Unit, integration, and specification files identified from test directories and common test filename patterns.',
  },
  documentation: {
    title: 'Documentation',
    description: 'Markdown, reStructuredText, and files under documentation directories.',
  },
}

export function PullRequestsView({ pid }) {
  const [data, setData] = useState(null)
  const [selected, setSelected] = useState(null)
  const [impact, setImpact] = useState(null)
  const [repoFilter, setRepoFilter] = useState('')
  const [teamFilter, setTeamFilter] = useState('')
  const [repoSearch, setRepoSearch] = useState('')
  const [openOnly, setOpenOnly] = useState(true)
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

  const repositories = data?.repositories || []
  const teams = useMemo(() => Array.from(new Set(repositories.map((repo) => repo.team || 'default'))).sort(), [data])
  const scopedRepos = useMemo(() => {
    const q = repoSearch.trim().toLowerCase()
    return repositories.filter((repo) => {
      if (teamFilter && (repo.team || 'default') !== teamFilter) return false
      if (openOnly && repo.open_count === 0) return false
      if (q && !`${repo.repo_name} ${repo.team || 'default'}`.toLowerCase().includes(q)) return false
      return true
    })
  }, [data, teamFilter, repoSearch, openOnly])
  const scopedRepoIDs = useMemo(() => new Set(scopedRepos.map((repo) => repo.repo_id)), [scopedRepos])
  const pulls = useMemo(() => {
    const q = query.trim().toLowerCase()
    return flattenPulls(data).filter((pr) => {
      if (!scopedRepoIDs.has(pr.repo_id)) return false
      if (repoFilter && pr.repo_id !== repoFilter) return false
      if (!q) return true
      return `${pr.title} ${pr.repo_name} ${pr.author} ${(pr.labels || []).join(' ')}`.toLowerCase().includes(q)
    })
  }, [data, scopedRepoIDs, repoFilter, query])

  useEffect(() => {
    if (repoFilter && !scopedRepoIDs.has(repoFilter)) setRepoFilter('')
  }, [repoFilter, scopedRepoIDs])
  useEffect(() => {
    if (!pulls.length) {
      setSelected(null)
      return
    }
    setSelected((current) => pulls.find((pr) => samePR(pr, current)) || pulls[0])
  }, [repoFilter, teamFilter, repoSearch, openOnly, query, data])

  const activeRepos = scopedRepos.filter((repo) => repo.open_count > 0)
  const scopedOpen = scopedRepos.reduce((sum, repo) => sum + repo.open_count, 0)
  const visibleRepos = scopedRepos.slice(0, 100)
  const visiblePulls = pulls.slice(0, 100)
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
        <Metric value={data ? scopedOpen : '—'} label="Open PRs in scope" tone="blue" />
        <Metric value={activeRepos.length} label="Repos with PRs in scope" tone="cyan" />
        <Metric value={data?.repo_count ?? '—'} label="Repositories checked" />
        <Metric value={impact ? `${impact.risk_score}/100` : '—'} label="Selected risk" tone={impact?.risk_level} />
        <Metric value={impact?.company?.available ? companyCount : '—'} label="Impacted services" tone={companyCount > 3 ? 'high' : 'green'} />
      </section>

      <div class="pr-workspace">
        <aside class="pr-repos">
          <div class="pr-section-head"><h2>Scope</h2><span>{scopedRepos.length}/{repositories.length}</span></div>
          <div class="pr-scope-controls">
            <label>
              <span>Team</span>
              <select value={teamFilter} onInput={(e) => setTeamFilter(e.currentTarget.value)}>
                <option value="">All teams</option>
                {teams.map((team) => <option value={team} key={team}>{team}</option>)}
              </select>
            </label>
            <label>
              <span>Find repository</span>
              <input value={repoSearch} onInput={(e) => setRepoSearch(e.currentTarget.value)} placeholder="Name or team…" />
            </label>
            <label class="pr-open-only">
              <input type="checkbox" checked={openOnly} onInput={(e) => setOpenOnly(e.currentTarget.checked)} />
              <span>Only repositories with open PRs</span>
            </label>
            {(teamFilter || repoSearch || !openOnly) && <button class="pr-clear-scope" onClick={() => { setTeamFilter(''); setRepoSearch(''); setOpenOnly(true); setRepoFilter('') }}>Reset scope</button>}
          </div>
          <div class="pr-section-head pr-repo-results"><h2>Repositories</h2><span>{scopedOpen} PRs</span></div>
          <button class={'pr-repo-row ' + (!repoFilter ? 'active' : '')} onClick={() => setRepoFilter('')}>
            <span>All repositories in scope</span><strong>{scopedOpen}</strong>
          </button>
          {visibleRepos.map((repo) => (
            <button key={repo.repo_id} class={'pr-repo-row ' + (repoFilter === repo.repo_id ? 'active' : '')} onClick={() => setRepoFilter(repo.repo_id)}>
              <span><b>{repo.repo_name}</b><small>{repo.team || 'default'} · {repo.status}</small></span>
              <strong>{repo.open_count}</strong>
            </button>
          ))}
          {scopedRepos.length > visibleRepos.length && <div class="pr-limit-note">Showing the first {visibleRepos.length} of {scopedRepos.length} repositories. Choose a team or search by repository name to narrow the list.</div>}
          {!loading && scopedRepos.length === 0 && <div class="pr-provider-note">No repositories match this team and repository scope.</div>}
          {(data?.error_count || 0) > 0 && <div class="pr-provider-note">{data.error_count} repositories could not be read. Check GitHub authentication and repository URLs.</div>}
        </aside>

        <section class="pr-list-panel">
          <div class="pr-list-tools">
            <input value={query} onInput={(e) => setQuery(e.currentTarget.value)} placeholder="Search title, author, label…" />
            <span>{visiblePulls.length === pulls.length ? `${pulls.length} shown` : `${visiblePulls.length} of ${pulls.length}`}</span>
          </div>
          <div class="pr-list">
            {loading && <LoadingBlock label="Loading open pull requests from GitHub…" />}
            {!loading && pulls.length === 0 && <EmptyBlock label="No open pull requests match this view." />}
            {visiblePulls.map((pr) => (
              <button key={`${pr.repo_id}/${pr.number}`} class={'pr-card ' + (samePR(pr, selected) ? 'active' : '')} onClick={() => setSelected(pr)}>
                <div class="pr-card-top"><span>{pr.repo_name} <b>#{pr.number}</b></span>{pr.draft && <em>Draft</em>}</div>
                <strong>{pr.title}</strong>
                <div class="pr-card-meta"><span>@{pr.author || 'unknown'}</span><span>{relativeDate(pr.updated_at)}</span></div>
                {(pr.labels || []).length > 0 && <div class="pr-labels">{pr.labels.slice(0, 4).map((label) => <span key={label}>{label}</span>)}</div>}
              </button>
            ))}
            {pulls.length > visiblePulls.length && <div class="pr-limit-note">Showing the first {visiblePulls.length} results. Narrow the scope or search PR titles, authors, and labels.</div>}
          </div>
        </section>

        <main class="pr-impact-panel">
          {!selected && <EmptyBlock label="Select a pull request to calculate its impact." />}
          {selected && impactLoading && <LoadingBlock label="Reading changed files and walking the company graph…" />}
          {impactError && <div class="banner error">{impactError}</div>}
          {selected && impact && <ImpactDetail impact={impact} key={`${selected.repo_id}/${selected.number}`} />}
        </main>
      </div>
    </div>
  )
}

function ImpactDetail({ impact }) {
  const pr = impact.pull_request
  const code = impact.codebase
  const company = impact.company
  const [categoryFilter, setCategoryFilter] = useState('')
  const selectedCategory = categoryFilter ? (code.categories || []).find((cat) => cat.id === categoryFilter) : null
  const visibleFiles = categoryFilter ? (code.files || []).filter((file) => file.category === categoryFilter) : (code.files || [])
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
        <div class="pr-category-explainer">
          <strong>What the categories mean</strong>
          <p>They are changed-file buckets, not detected vulnerabilities or endpoint counts. Every file is assigned once from its path and type. The large number is the number of changed files; <code>+ / −</code> are added and removed lines. Select a bucket to inspect those files.</p>
        </div>
        <div class="pr-category-grid">
          {(code.categories || []).map((cat) => (
            <button type="button" class={'pr-category ' + cat.id + (categoryFilter === cat.id ? ' active' : '')} key={cat.id} onClick={() => setCategoryFilter((current) => current === cat.id ? '' : cat.id)}>
              <span>{cat.label}</span><strong>{cat.files} file{cat.files === 1 ? '' : 's'}</strong><small>+{cat.additions} −{cat.deletions} lines</small>
            </button>
          ))}
        </div>
        <div class="pr-category-definition">
          <strong>{selectedCategory ? `${selectedCategory.label} · ${selectedCategory.files} changed file${selectedCategory.files === 1 ? '' : 's'}` : 'All change categories'}</strong>
          <p>{selectedCategory ? (CATEGORY_HELP[selectedCategory.id]?.description || 'Files grouped by their path and file type.') : 'Select a category above to filter the file list and see exactly why those files were grouped together.'}</p>
          {categoryFilter && <button onClick={() => setCategoryFilter('')}>Show all files</button>}
        </div>
        {(code.signals || []).length > 0 && (
          <div class="pr-signals">
            <div class="pr-signal-intro"><strong>Review signals</strong><span>Attention prompts inferred from file paths and diff text; they do not prove a vulnerability or breaking change.</span></div>
            {(code.signals || []).map((signal) => <div class={'pr-signal ' + signal.severity} key={signal.kind}><b>{signal.label}</b><span>{signal.files.length} file{signal.files.length === 1 ? '' : 's'}</span></div>)}
          </div>
        )}
        <div class="pr-reasons">
          <h3>Why this score</h3>
          <ul>{(code.risk_reasons || []).map((reason) => <li key={reason}>{reason}</li>)}</ul>
        </div>
        <details class="pr-files" open>
          <summary>{selectedCategory ? selectedCategory.label : 'All changed files'} <span>{visibleFiles.length}</span></summary>
          <div class="pr-file-list">
            {visibleFiles.map((file) => (
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
            <BlastRadiusMap company={company} />
            <div class="pr-tags">{(company.teams || []).map((team) => <span key={team}>Team · {team}</span>)}{(company.resources || []).slice(0, 12).map((resource) => <span key={resource}>{resource}</span>)}</div>
            {company.flow && (
              <details class="pr-technical-graph">
                <summary>Technical dependency graph <span>Services, queues, resources, and protocols</span></summary>
                <div class="pr-graph-explainer">
                  <p>This is the detailed evidence behind the blast-radius summary. It is not a PR call graph. Solid blue paths are synchronous dependencies; green dashed paths are asynchronous message flows through queues or topics. Drag to pan and scroll to zoom.</p>
                  <div class="pr-graph-legend"><span><i class="solid" />Synchronous dependency</span><span><i class="async" />Async message path</span><span><i class="service" />Service</span><span><i class="resource" />Queue or resource</span></div>
                </div>
                <div class="pr-flow"><FlowRibbon flow={company.flow} /></div>
              </details>
            )}
            <p class="pr-confidence">Confidence: graph estimate. {(company.notes || []).join(' ')}</p>
          </>
        )}
      </section>
    </div>
  )
}

function BlastRadiusMap({ company }) {
  const byDepth = new Map()
  for (const service of company.services || []) {
    if (!byDepth.has(service.depth)) byDepth.set(service.depth, [])
    byDepth.get(service.depth).push(service)
  }
  const hops = Array.from(byDepth.entries()).sort((a, b) => a[0] - b[0])
  return (
    <div class="pr-blast-radius">
      <div class="pr-blast-explainer">
        <strong>How to read this blast radius</strong>
        <p>The PR changes the service at hop 0. Hop 1 services directly depend on it; hop 2+ services are reached through another dependency. This means “review and test this path,” not “this service will definitely break.”</p>
      </div>
      <div class="pr-hop-map">
        <div class="pr-hop-column root">
          <div class="pr-hop-title"><strong>Hop 0</strong><span>Change starts here</span></div>
          <div class="pr-hop-service root-service"><strong>{company.root_service}</strong><span>Repository changed by this PR</span></div>
        </div>
        {hops.map(([depth, services]) => (
          <div class="pr-hop-stage" key={depth}>
            <div class="pr-hop-arrow" aria-hidden="true"><span>→</span></div>
            <div class="pr-hop-column">
              <div class="pr-hop-title"><strong>Hop {depth}</strong><span>{depth === 1 ? 'Directly affected' : 'Indirectly affected'}</span></div>
              <div class="pr-hop-services">
                {services.map((service) => (
                  <div class="pr-hop-service" key={service.name}>
                    <strong>{service.name}</strong>
                    <span>Team {service.team || 'default'} · {service.reason}</span>
                  </div>
                ))}
              </div>
            </div>
          </div>
        ))}
        {hops.length === 0 && <div class="pr-hop-none">No downstream service dependency is proven by the current graph.</div>}
      </div>
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
