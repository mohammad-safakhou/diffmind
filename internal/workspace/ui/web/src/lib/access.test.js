import test from 'node:test'
import assert from 'node:assert/strict'
import { canCreateProject, membersFromRows } from './access.js'
import { parseRoute } from './router.js'

test('project creation respects scoped administration', () => {
  assert.equal(canCreateProject(null), false)
  assert.equal(canCreateProject({ role: 'viewer', project_access: 'legacy' }), false)
  assert.equal(canCreateProject({ role: 'editor', project_access: 'scoped' }), false)
  assert.equal(canCreateProject({ role: 'editor', project_access: 'legacy' }), true)
  assert.equal(canCreateProject({ role: 'admin', project_access: 'scoped' }), true)
})
test('member drafts preserve exact subjects and reject unsafe grants', () => {
  const members = membersFromRows([{ subject: '__proto__', role: 'viewer' }, { subject: 'CaseSensitive', role: 'editor' }])
  assert.equal(Object.getPrototypeOf(members), null)
  assert.equal(members.__proto__, 'viewer')
  for (const rows of [[{ subject: '', role: 'viewer' }], [{ subject: ' alice ', role: 'viewer' }], [{ subject: 'a\nb', role: 'viewer' }], [{ subject: 'a', role: 'admin' }], [{ subject: 'a', role: 'viewer' }, { subject: 'a', role: 'editor' }]]) assert.throws(() => membersFromRows(rows))
  assert.deepEqual(Object.keys(membersFromRows([])), [])
})
test('access route preserves project identity', () => assert.deepEqual(parseRoute('/projects/example/access'), { name: 'access', pid: 'example' }))
