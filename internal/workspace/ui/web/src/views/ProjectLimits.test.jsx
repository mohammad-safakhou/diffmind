import test from 'node:test'
import assert from 'node:assert/strict'
import { parseHTML } from 'linkedom'
import { render } from 'preact'
import { act } from 'preact/test-utils'
import { ProjectLimits } from './ProjectLimits.jsx'

const settle = () => act(async () => { await new Promise(setImmediate) })
async function mount(t, { canManage = true, failRead = false, failSave = false } = {}) {
  const { window, document } = parseHTML('<html><body><div id="root"></div></body></html>')
  const previous = { window: globalThis.window, document: globalThis.document, fetch: globalThis.fetch }
  Object.assign(globalThis, { window, document })
  const calls = []
  let limits = { revision: 2, max_pending_jobs: 8, repository_workers: 2 }
  globalThis.fetch = async (path, options = {}) => {
    const method = options.method || 'GET', body = options.body && JSON.parse(options.body)
    calls.push({ path, method, body })
    const status = method === 'GET' ? (failRead ? 503 : 200) : (failSave ? 409 : 200)
    if (method === 'PUT' && status === 200) limits = { ...body, revision: body.revision + 1 }
    const result = status >= 400 ? { error: 'Policy changed or unavailable' } : method === 'PUT' ? limits : { limits, effective_pending_jobs: limits.max_pending_jobs || 256, effective_repository_workers: limits.repository_workers || 4, pending_jobs: 1, active_repository_workers: 0 }
    return { ok: status < 400, status, text: async () => JSON.stringify(result) }
  }
  const root = document.getElementById('root')
  t.after(async () => { await act(() => render(null, root)); Object.assign(globalThis, previous) })
  await act(async () => render(<ProjectLimits pid="a b" canManage={canManage} />, root)); await settle()
  const input = async (label, value) => {
    const node = root.querySelector(`[aria-label="${label}"]`)
    await act(() => { node.value = value; node.dispatchEvent(new window.Event('input', { bubbles: true })) })
  }
  const submit = async () => { await act(async () => root.querySelector('form').dispatchEvent(new window.Event('submit', { bubbles: true, cancelable: true }))); await settle() }
  const button = (name) => [...root.querySelectorAll('button')].find((node) => node.textContent === name)
  const reload = async () => { await act(async () => button('Reload limits (discard edits)').dispatchEvent(new window.Event('click', { bubbles: true }))); await settle() }
  return { root, calls, input, submit, button, reload }
}

test('admin saves bounded limits with revision and reloads effective usage', async (t) => {
  const { root, calls, input, submit } = await mount(t)
  assert.match(root.textContent, /1 \/ 8 pending jobs/)
  await input('Pending job limit', '0'); await input('Repository worker limit', '1'); await submit()
  assert.deepEqual(calls.find((c) => c.method === 'PUT'), { path: '/api/v1/projects/a%20b/limits', method: 'PUT', body: { revision: 2, max_pending_jobs: 0, repository_workers: 1 } })
  assert.match(root.textContent, /revision 3/)
  assert.match(root.textContent, /1 \/ 256 pending jobs/)
  assert.match(root.textContent, /Project limits saved/)
})
test('reader sees limits without editing controls', async (t) => {
  const { root, calls } = await mount(t, { canManage: false })
  assert.equal(root.querySelector('form'), null)
  assert.match(root.textContent, /Only a global administrator/)
  assert.equal(calls.length, 1)
})
test('invalid drafts never reach the server', async (t) => {
  const { root, calls, input, submit } = await mount(t)
  await input('Repository worker limit', '33'); await submit()
  assert.match(root.textContent, /between 0 and 32/)
  assert.equal(calls.filter((c) => c.method === 'PUT').length, 0)
})
test('conflicting saves retain drafts, require reload and do not silently overwrite', async (t) => {
  const { root, calls, input, submit, button, reload } = await mount(t, { failSave: true })
  await input('Pending job limit', '1'); await submit()
  assert.equal(root.querySelector('[aria-label="Pending job limit"]').value, '1')
  assert.equal(button('Save limits').disabled, true)
  await submit()
  assert.equal(calls.filter((c) => c.method === 'PUT').length, 1)
  await reload()
  assert.equal(root.querySelector('[aria-label="Pending job limit"]').value, '8')
  assert.equal(button('Save limits').disabled, false)
})
test('unavailable policy fails closed in the screen', async (t) => {
  const { root } = await mount(t, { failRead: true })
  assert.match(root.querySelector('[role="alert"]').textContent, /unavailable/)
  assert.equal(root.querySelector('form'), null)
})
