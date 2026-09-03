import test from 'node:test'
import assert from 'node:assert/strict'
import { tokenRequest, tokenStatus } from './tokens.js'

test('token issuance is bounded and never grants admin', () => {
  assert.deepEqual(tokenRequest('My agent', 'viewer', 30), { name: 'My agent', role: 'viewer', expires_in_seconds: 2592000 })
  assert.equal(tokenRequest('Refresh bot', 'editor', 365).expires_in_seconds, 31536000)
  for (const name of ['', ' padded', 'line\nbreak', 'é'.repeat(51)]) assert.throws(() => tokenRequest(name, 'viewer', 1))
  for (const days of [0, 366, -1, 1.5, NaN, Infinity, '30']) assert.throws(() => tokenRequest('agent', 'viewer', days))
  for (const role of ['admin', '', 'owner']) assert.throws(() => tokenRequest('agent', role, 1))
})
test('expiry boundary and revocation take effect in the token list', () => {
  const token = { expires_at: '2026-09-03T12:00:00Z' }
  const expiry = Date.parse(token.expires_at)
  assert.equal(tokenStatus(token, expiry - 1), 'Active')
  assert.equal(tokenStatus(token, expiry), 'Expired')
  assert.equal(tokenStatus({ ...token, revoked_at: '2026-09-02T12:00:00Z' }, expiry - 1), 'Revoked')
  assert.equal(tokenStatus({ expires_at: 'invalid' }), 'Expired')
})
