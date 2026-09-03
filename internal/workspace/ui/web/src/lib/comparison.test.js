import test from 'node:test'
import assert from 'node:assert/strict'
import { comparisonDefaults, comparisonKeyLabel } from './comparison.js'
import { parseRoute } from './router.js'

const runs = [{ id: 'failed', graph_available: false }, ...['new', 'middle', 'old'].map((id) => ({ id, graph_available: true }))]
test('comparison defaults select available snapshots and respect pinned IDs', () => {
  assert.deepEqual(comparisonDefaults(runs), { from: 'middle', to: 'new' })
  assert.deepEqual(comparisonDefaults(runs, { to: 'middle' }), { from: 'old', to: 'middle' })
  assert.deepEqual(comparisonDefaults(runs, { from: 'removed', to: 'pinned' }), { from: 'removed', to: 'pinned' })
  assert.deepEqual(comparisonDefaults([]), { from: '', to: '' })
  assert.deepEqual(comparisonDefaults([{ id: 'only', graph_available: true }]), { from: '', to: 'only' })
})
test('comparison routes preserve explicit direction and escaped identifiers', () => {
  assert.deepEqual(parseRoute('/projects/my%20team/compare?from=run%2B1&to=run2'), { name: 'compare', pid: 'my team', params: { from: 'run+1', to: 'run2' } })
})
test('semantic keys display safely without requiring a known schema', () => {
  assert.equal(comparisonKeyLabel('["api","http","GET /orders"]'), 'api · http · GET /orders')
  assert.equal(comparisonKeyLabel('future-key'), 'future-key')
})
