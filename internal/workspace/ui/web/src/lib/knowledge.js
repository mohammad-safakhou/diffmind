// Keep pack provenance readable without rendering source snippets or following
// arbitrary repository-provided URLs.
export function knowledgeRows(details = {}) {
  if (!details?.pack_id) return []
  const locations = Array.isArray(details.source_locations) ? details.source_locations : []
  const sources = locations
    .filter(loc => typeof loc?.file === 'string' && Number.isInteger(loc.start_line) && loc.start_line > 0)
    .map(loc => `${loc.file}:${loc.start_line}`)
  return [
    ['Knowledge pack', `${details.pack_id}${details.pack_version ? `@${details.pack_version}` : ''}`],
    ['Detector', details.detector],
    ['Basis', 'Configured relationship (not runtime reachability)'],
    ['Source', [...new Set(sources)].join(', ')],
    ['Resolution', details.resolution_reason],
  ].filter(([, value]) => value != null && value !== '')
}
