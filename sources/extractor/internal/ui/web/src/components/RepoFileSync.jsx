import { useEffect, useMemo, useState } from 'preact/hooks'
import {
  getRepoFile,
  listRuns,
  mergeRepoFile,
  runProposal,
  upsertRepo,
} from '../lib/api.js'
import { Badge, Button, Card, useToast } from './ui/index.js'
import { FsPicker } from './FsPicker.jsx'

export function RepoFileSync({ repo, onChanged, onGraph }) {
  const toast = useToast()
  const [path, setPath] = useState(defaultFilePath(repo))
  const [status, setStatus] = useState(null)
  const [runs, setRuns] = useState([])
  const [selectedRun, setSelectedRun] = useState('')
  const [plan, setPlan] = useState(null)
  const [busy, setBusy] = useState('')
  const [picking, setPicking] = useState(false)

  const completedRuns = useMemo(() => runs.filter((r) => r.status === 'completed' && r.repo_path === repo.path), [runs, repo.path])

  const refreshStatus = async (p = path) => {
    if (!p) { setStatus(null); return }
    try {
      setStatus(await getRepoFile(p))
    } catch (e) {
      setStatus({ exists: false, valid: false, error: e.message || String(e) })
    }
  }

  const loadRuns = async () => {
    try {
      const data = await listRuns()
      const next = Array.isArray(data?.runs) ? data.runs : []
      setRuns(next)
      const first = next.find((r) => r.status === 'completed' && r.repo_path === repo.path)
      setSelectedRun((current) => current || first?.run_id || '')
    } catch (e) {
      toast.error(e.message || String(e))
    }
  }

  useEffect(() => {
    setPath(defaultFilePath(repo))
    setPlan(null)
    refreshStatus(defaultFilePath(repo))
    loadRuns()
  }, [repo.id])

  const choosePath = async (picked) => {
    setPicking(false)
    setPath(picked)
    setPlan(null)
    try {
      await upsertRepo({ path: repo.path, file_path: picked })
      onChanged?.()
    } catch (e) {
      toast.error(e.message || String(e))
    }
    refreshStatus(picked)
  }

  const preview = async () => {
    if (!selectedRun) { toast.error('Pick a completed run first.'); return }
    setBusy('preview')
    try {
      const next = await runProposal(path, selectedRun)
      setPlan(next)
      const adds = next.append?.length || 0
      const skips = next.skip?.length || 0
      toast.success(adds || skips ? `Preview ready: ${adds} add, ${skips} already present.` : 'Preview ready: no records found.')
    } catch (e) {
      toast.error(e.message || String(e))
    } finally {
      setBusy('')
    }
  }

  const apply = async () => {
    setBusy('apply')
    try {
      const result = await mergeRepoFile(path)
      await upsertRepo({ path: repo.path, file_path: path })
      setPlan(null)
      await refreshStatus(path)
      onChanged?.()
      toast.success(result.merged ? `Imported ${result.merged} fact(s) as proposed — review them in the Review tab.` : 'Nothing to import.')
    } catch (e) {
      toast.error(e.message || String(e))
    } finally {
      setBusy('')
    }
  }

  const mode = status?.exists ? 'Update from run' : 'Generate discovery file'

  return (
    <Card class="repo-sync">
      <div class="repo-sync-head">
        <div>
          <div class="repo-section-kicker">{mode}</div>
          <h2>{status?.exists ? 'Keep diffmind.yaml current' : 'Create diffmind.yaml from automation'}</h2>
          <p>Pick one completed run, preview the diff, then import new facts as <b>proposed</b>. Curate them to verified in the Review tab.</p>
        </div>
        <FileStatusBadge status={status} />
      </div>

      <div class="fw-path-row">
        <div class="fw-path-info">
          <span class="fw-path-label">Discovery file</span>
          <code class="fw-path">{path}</code>
        </div>
        <div class="fw-path-actions">
          <Button variant="secondary" size="tiny" onClick={() => setPicking(true)}>Change location</Button>
          {status?.exists && <Button variant="secondary" size="tiny" onClick={onGraph}>Open graph</Button>}
        </div>
      </div>
      {status?.error && <div class="fw-parse-error">Parse error: {status.error}</div>}

      <div class="repo-sync-controls">
        <label class="ui-field">
          <span class="ui-field-label">Completed run</span>
          <select value={selectedRun} onChange={(e) => { setSelectedRun(e.target.value); setPlan(null) }}>
            <option value="">Select a completed run…</option>
            {completedRuns.map((r) => <option value={r.run_id} key={r.run_id}>{r.run_id}</option>)}
          </select>
        </label>
        <div class="fw-action-row">
          <Button variant="secondary" onClick={preview} disabled={!selectedRun || !!busy}>{busy === 'preview' ? 'Previewing…' : 'Preview diff'}</Button>
          <Button onClick={apply} disabled={!plan?.append?.length || !!busy}>{busy === 'apply' ? 'Applying…' : status?.exists ? 'Apply update' : 'Generate file'}</Button>
        </div>
      </div>

      {completedRuns.length === 0 && <div class="fw-diff empty">No completed runs for this repository yet. Launch a run first.</div>}
      {plan && <MergeDiff plan={plan} />}
      {picking && <FsPicker initialPath={parentDir(path) || repo.path} onPick={choosePath} onClose={() => setPicking(false)} />}
    </Card>
  )
}

export function MergeDiff({ plan }) {
  const append = plan.append || []
  const skip = plan.skip || []
  if (!append.length && !skip.length) return <div class="fw-diff empty">No records to merge.</div>
  return (
    <div class="fw-diff">
      {append.length > 0 && (
        <div class="fw-diff-group">
          <div class="fw-diff-group-head">Will add <span class="fw-diff-count">{append.length}</span></div>
          {append.map((e, i) => <DiffRow entry={e} mark="+" className="add" key={'a-' + i} />)}
        </div>
      )}
      {skip.length > 0 && (
        <div class="fw-diff-group">
          <div class="fw-diff-group-head muted">Already present <span class="fw-diff-count">{skip.length}</span></div>
          {skip.map((e, i) => <DiffRow entry={e} mark="=" className="skip" key={'s-' + i} />)}
        </div>
      )}
    </div>
  )
}

function DiffRow({ entry, mark, className }) {
  return (
    <div class={'fw-diff-row ' + className}>
      <span class="fw-diff-mark">{mark}</span>
      <span class="fw-diff-type">{entry.kind}</span>
      <span class="fw-diff-name">{entry.name}</span>
    </div>
  )
}

function FileStatusBadge({ status }) {
  if (!status) return <Badge tone="neutral">checking</Badge>
  if (!status.exists) return <Badge tone="warn">no file - generate</Badge>
  if (!status.valid) return <Badge tone="error">invalid</Badge>
  const c = status.counts || {}
  return <Badge tone="success">{(c.exposures || 0) + (c.dependencies || 0)} nodes · {c.connections || 0} conns</Badge>
}

function defaultFilePath(repo) {
  return repo.file_path || `${repo.path}/diffmind.yaml`
}

function parentDir(p) {
  const i = p.lastIndexOf('/')
  return i > 0 ? p.slice(0, i) : ''
}
