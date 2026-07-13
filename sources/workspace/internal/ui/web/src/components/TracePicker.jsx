import { useEffect, useRef, useState } from 'preact/hooks'
import { getRunArchGraphEntrypoints } from '../lib/api.js'

const KIND_LABELS = {
  http_endpoint: 'HTTP endpoint',
  rpc_endpoint: 'RPC endpoint',
  queue_consumer: 'Queue consumer',
  scheduled_job: 'Scheduled job',
  webhook: 'Webhook',
  cli_command: 'CLI command',
}

// TracePicker is the entry-point search: the full graph is far too large to
// ship to the browser, so each keystroke queries the server-side index.
export function TracePicker({ pid, rid, initialQuery = '', onPick }) {
  const [query, setQuery] = useState(initialQuery)
  const [results, setResults] = useState(null)
  const [loading, setLoading] = useState(false)
  const timer = useRef(null)

  useEffect(() => {
    if (timer.current) clearTimeout(timer.current)
    timer.current = setTimeout(() => {
      setLoading(true)
      getRunArchGraphEntrypoints(pid, rid, query, 60)
        .then((refs) => setResults(refs))
        .catch(() => setResults([]))
        .finally(() => setLoading(false))
    }, 200)
    return () => timer.current && clearTimeout(timer.current)
  }, [pid, rid, query])

  const groups = new Map()
  for (const ref of results || []) {
    const kind = KIND_LABELS[ref.kind] || ref.kind
    if (!groups.has(kind)) groups.set(kind, [])
    groups.get(kind).push(ref)
  }

  return (
    <div class="trace-picker">
      <input
        class="trace-picker-input"
        type="search"
        placeholder="Search an entry point — endpoint path, consumer, job, or service name…"
        value={query}
        onInput={(e) => setQuery(e.target.value)}
        autoFocus
      />
      {loading && <div class="muted small">Searching…</div>}
      {results && results.length === 0 && !loading && <div class="muted">No entry points match “{query}”.</div>}
      {[...groups.entries()].map(([kind, refs]) => (
        <div class="trace-picker-group" key={kind}>
          <h4>{kind}</h4>
          {refs.map((ref) => (
            <button class="trace-picker-row" key={ref.service + '|' + ref.id} onClick={() => onPick(ref)}>
              <strong>{ref.name}</strong>
              <span class="muted">{ref.service}</span>
              {ref.team && <span class="trace-picker-team">{ref.team}</span>}
            </button>
          ))}
        </div>
      ))}
    </div>
  )
}
