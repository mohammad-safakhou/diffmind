import test from 'node:test'
import assert from 'node:assert/strict'
import { ingestionCanResume, ingestionProgress } from './ingestion.js'

test('only recoverable jobs with saved requests offer resume', () => {
  for (const status of ['failed', 'partial', 'cancelled', 'interrupted']) {
    assert.equal(ingestionCanResume({ status, request: {} }), true)
    assert.equal(ingestionCanResume({ status }), false)
    assert.equal(ingestionCanResume({ status, request: {}, job_id: 'refresh-123' }), false)
  }
  for (const status of ['running', 'completed', 'not_started']) assert.equal(ingestionCanResume({ status, request: {} }), false)
  assert.equal(ingestionCanResume(null), false)
})

test('progress includes terminal checkpoints, not just successful analyses', () => {
  assert.equal(ingestionProgress({ repo_progress: ['completed', 'reused', 'failed', 'cancelled', 'skipped', 'pending', 'analyzing', 'syncing'].map(status => ({ status })) }), 5)
  assert.equal(ingestionProgress({ analyzed: 2, reused: 3 }), 5)
  assert.equal(ingestionProgress({}), 0)
})
