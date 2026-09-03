export function tokenRequest(name, role, days) {
  if (!name || name.trim() !== name || new TextEncoder().encode(name).length > 100 || /[\u0000-\u001f\u007f-\u009f]/.test(name)) throw new Error('Enter an exact name of 1–100 bytes without control characters.')
  if (!['viewer', 'editor'].includes(role)) throw new Error('Choose viewer or editor.')
  if (!Number.isInteger(days) || days < 1 || days > 365) throw new Error('Choose a lifetime of 1–365 days.')
  return { name, role, expires_in_seconds: days * 86400 }
}

export function tokenStatus(token, now = Date.now()) {
  if (token.revoked_at) return 'Revoked'
  const expiry = Date.parse(token.expires_at)
  if (!Number.isFinite(expiry) || expiry <= now) return 'Expired'
  return 'Active'
}
