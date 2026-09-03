import test from 'node:test'
import assert from 'node:assert/strict'
import { jobCanCancel, jobCanRetry, jobStatus } from './operations.js'
import { parseRoute } from './router.js'

test('operation actions respect roles and persisted lifecycle', () => {
  assert.equal(jobCanCancel({ status: 'queued' }, 'viewer'), false)
  assert.equal(jobCanCancel({ status: 'queued' }, 'editor'), true)
  assert.equal(jobCanCancel({ status: 'running', cancel_requested: true }, 'admin'), false)
  assert.equal(jobCanRetry({ status: 'succeeded' }, 'admin'), false)
  assert.equal(jobCanRetry({ status: 'failed', attempts: [] }, 'editor'), true)
  assert.equal(jobCanRetry({ status: 'failed', attempts: Array(100) }, 'admin'), false)
  assert.equal(jobCanRetry({ status: 'failed' }, 'viewer'), false)
})
test('operation labels distinguish retry and draining cancellation', () => {
  assert.equal(jobStatus({ status: 'queued', attempts: [{}] }), 'queued for retry')
  assert.equal(jobStatus({ status: 'running', cancel_requested: true }), 'cancelling')
  assert.equal(jobStatus({ status: 'failed' }), 'failed')
})
test('operations routes decode project identity', () => {
  assert.deepEqual(parseRoute('/projects/my%20team/operations'), { name: 'operations', pid: 'my team' })
})
