// CoverageBar shows progress toward a fully verified (100%) architecture.
export function CoverageBar({ coverage }) {
  const c = coverage || { verified: 0, proposed: 0, needs_review: 0, total: 0 }
  const total = c.total || 0
  const pct = total ? Math.round((c.verified / total) * 100) : 100
  const pending = (c.proposed || 0) + (c.needs_review || 0)
  return (
    <div class="coverage">
      <div class="coverage-head">
        <span class="coverage-pct">{pct}%</span>
        <span class="coverage-label">verified{pending ? ` · ${pending} pending` : ''}</span>
      </div>
      <div class="coverage-track">
        <div class="coverage-fill" style={`width:${pct}%`} />
      </div>
      <div class="coverage-counts">
        <span class="coverage-dot verified" /> {c.verified || 0} verified
        {pending > 0 && <><span class="coverage-dot pending" /> {pending} pending</>}
      </div>
    </div>
  )
}
