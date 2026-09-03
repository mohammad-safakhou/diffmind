import test from 'node:test'
import assert from 'node:assert/strict'
import { knowledgeRows } from './knowledge.js'

test('pack provenance includes source lines and declaration limits', () => {
  const rows = Object.fromEntries(knowledgeRows({
    pack_id: 'service-manifest', pack_version: '1.0.0', detector: 'HTTP clients',
    source_locations: [{ file: 'service.yaml', start_line: 4 }, { file: 'service.yaml', start_line: 4 }],
    resolution_reason: 'explicit alias',
  }))
  assert.equal(rows['Knowledge pack'], 'service-manifest@1.0.0')
  assert.equal(rows.Source, 'service.yaml:4')
  assert.equal(rows.Detector, 'HTTP clients')
  assert.equal(rows.Resolution, 'explicit alias')
  assert.match(rows.Basis, /not runtime reachability/)
})

test('legacy objects and malformed evidence do not invent provenance', () => {
  assert.deepEqual(knowledgeRows(), [])
  assert.deepEqual(knowledgeRows(null), [])
  assert.deepEqual(knowledgeRows({ source_locations: [{ file: 'source.go', start_line: 1 }] }), [])
  const rows = Object.fromEntries(knowledgeRows({ pack_id: 'test', source_locations: [null, {}, { file: 'bad', start_line: 0 }] }))
  assert.equal(rows.Source, undefined)
  assert.equal(rows['Knowledge pack'], 'test')
})
