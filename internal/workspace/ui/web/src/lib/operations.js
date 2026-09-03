export function jobCanCancel(job, role) {
  return ['admin', 'editor'].includes(role) && ['queued', 'running'].includes(job.status) && !job.cancel_requested
}
export function jobCanRetry(job, role) {
  return ['admin', 'editor'].includes(role) && ['failed', 'cancelled', 'interrupted'].includes(job.status) && (job.attempts?.length || 0) < 100
}
export function jobStatus(job) {
  if (job.cancel_requested && job.status === 'running') return 'cancelling'
  if (job.status === 'queued' && job.attempts?.length) return 'queued for retry'
  return job.status
}
