import test from 'node:test'
import assert from 'node:assert/strict'
import { limitsRequest } from './limits.js'

test('quota requests preserve revision and explicit inherit, with bounded integer caps', () => {
  assert.deepEqual(limitsRequest(3, '0', '32'), { revision: 3, max_pending_jobs: 0, repository_workers: 32 })
  assert.deepEqual(limitsRequest(0, '10000', '0'), { revision: 0, max_pending_jobs: 10000, repository_workers: 0 })
  for (const value of ['', '-1', '1.5', ' 1', '1e2', 'Infinity', '10001', null, undefined]) assert.throws(() => limitsRequest(0, value, 1))
  for (const value of [-1, 33, NaN]) assert.throws(() => limitsRequest(0, 1, value))
  assert.throws(() => limitsRequest(-1, 1, 1))
})
