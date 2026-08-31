// StatusBadge renders a run's lifecycle status. Palette extracted verbatim from
// the original Home.jsx so the look is unchanged; reused by the runs list and
// the repositories overview.
export function StatusBadge({ status }) {
  if (!status) return <span class="muted">—</span>
  const colours = {
    completed: ['#062b13', '#22c55e'],
    failed: ['#3a0e11', '#ef4444'],
    cancelled: ['#3a2306', '#f59e0b'],
    running: ['#0e2240', '#4f8cff'],
    cancelling: ['#3a2306', '#f59e0b'],
  }
  const [bg, fg] = colours[status] || ['#1a2238', '#9aa6c0']
  return (
    <span style={`background:${bg};color:${fg};border:1px solid ${fg}44;border-radius:999px;padding:2px 8px;font-size:10px;font-weight:600;text-transform:uppercase;letter-spacing:0.04em`}>
      {status}
    </span>
  )
}
