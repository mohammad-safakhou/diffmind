export function limitsRequest(revision, pending, workers) {
  const integer = (value, max) => {
    if (!/^\d+$/.test(String(value))) throw new Error('Limits must be whole numbers; use 0 to inherit.')
    const number = Number(value)
    if (!Number.isSafeInteger(number) || number > max) throw new Error(`Limit must be between 0 and ${max}.`)
    return number
  }
  return { revision: integer(revision, Number.MAX_SAFE_INTEGER), max_pending_jobs: integer(pending, 10000), repository_workers: integer(workers, 32) }
}
