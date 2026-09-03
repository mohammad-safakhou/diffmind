// Run history is newest first. Keep explicitly pinned IDs, even if their page
// has not loaded (or the artifact was removed); the query reports that error.
export function comparisonDefaults(runs, params = {}) {
  const available = runs.filter((run) => run.graph_available)
  const to = params.to || available[0]?.id || ''
  const toIndex = available.findIndex((run) => run.id === to)
  const from = params.from || available[toIndex + 1]?.id || available.find((run) => run.id !== to)?.id || ''
  return { from, to }
}

export function comparisonKeyLabel(key) {
  try {
    const parts = JSON.parse(key)
    if (Array.isArray(parts)) return parts.join(' · ')
  } catch { /* preserve unknown future identity formats */ }
  return key
}
