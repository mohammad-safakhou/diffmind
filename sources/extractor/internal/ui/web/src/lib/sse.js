// Minimal SSE client wrapping EventSource with two extras the standard API
// doesn't give us:
//   - automatic reconnect honoring Last-Event-ID,
//   - a clean way to consume *all* event kinds with a single handler
//     (the standard onmessage only fires for unnamed events).

export function openEventStream(url, { onEvent, onEOF, onError } = {}) {
  let es = null
  let lastID = null
  let stopped = false

  const open = () => {
    if (stopped) return
    // If the caller's URL already includes a query string (e.g. ?token=…
    // when --ui-token is set on the server) we have to merge ?from=…
    // with an `&` rather than appending another `?`. Browsers are lenient
    // and would still send the request, but the server only honours the
    // first `?` so the resume point is silently dropped.
    const u = lastID ? appendQuery(url, 'from', String(lastID + 1)) : url
    es = new EventSource(u)
    // Generic listener: any non-default event kind.
    es.addEventListener('message', (ev) => handle(ev))
    // We send named events from the server (event: stage_started, etc.).
    // EventSource doesn't have wildcard listeners, so we attach for every
    // known kind; unknown kinds fall through to message.
    KNOWN_KINDS.forEach((k) => es.addEventListener(k, handle))
    es.addEventListener('eof', () => {
      onEOF && onEOF()
      stopped = true
      try { es.close() } catch {}
    })
    es.onerror = () => {
      // EventSource auto-reconnects, but if it can't (404 etc.) we don't
      // want a tight loop. Wait briefly and try once.
      onError && onError()
      try { es.close() } catch {}
      if (!stopped) setTimeout(open, 800)
    }
  }

  const handle = (ev) => {
    if (ev.lastEventId) {
      const n = Number(ev.lastEventId)
      if (Number.isFinite(n) && (lastID == null || n > lastID)) lastID = n
    }
    let payload
    try {
      payload = JSON.parse(ev.data)
    } catch {
      return
    }
    onEvent && onEvent(payload)
  }

  open()
  return () => {
    stopped = true
    if (es) try { es.close() } catch {}
  }
}

// appendQuery returns url with the given key=value pair added to its
// query string, picking the right separator based on whether the URL
// already has a query.
function appendQuery(url, key, value) {
  const sep = url.includes('?') ? '&' : '?'
  return url + sep + key + '=' + encodeURIComponent(value)
}

const KNOWN_KINDS = [
  'run_started', 'run_completed', 'run_failed', 'run_cancelled',
  'stage_started', 'stage_progress', 'stage_completed',
  'job_pending', 'job_started', 'job_completed', 'job_failed',
  'llm_call_started', 'llm_call_completed',
  'session_created', 'session_aborted',
  'watchdog_action', 'log', 'subscriber_dropped',
]
