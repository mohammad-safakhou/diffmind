export function ingestionCanResume(ingestion) {
  return Boolean(ingestion?.request && !ingestion.job_id && ['failed', 'partial', 'cancelled', 'interrupted'].includes(ingestion.status))
}

export function ingestionProgress(ingestion) {
  if (ingestion.repo_progress?.length) {
    return ingestion.repo_progress.filter((repo) => ['completed', 'reused', 'failed', 'cancelled', 'skipped'].includes(repo.status)).length
  }
  return (ingestion.analyzed || 0) + (ingestion.reused || 0)
}
