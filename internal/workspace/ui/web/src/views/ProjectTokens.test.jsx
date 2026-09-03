import test from 'node:test'
import assert from 'node:assert/strict'
import { parseHTML } from 'linkedom'
import { render } from 'preact'
import { act } from 'preact/test-utils'
import { ProjectTokens } from './ProjectTokens.jsx'

const settle = () => act(async () => { await new Promise(setImmediate) })

// These are component/state tests, not a replacement for native browser checks
// (focus, layout, TLS, navigation). The real fetch wrapper is used against an
// in-memory HTTP stub; no credentials or external services are involved.
async function mount(t, { enabled = true, tokens = [], failIssue = false, failList = false, failRevoke = false } = {}) {
  const { window, document } = parseHTML('<html><body><div id="root"></div></body></html>')
  const previous = { window: globalThis.window, document: globalThis.document, fetch: globalThis.fetch }
  Object.assign(globalThis, { window, document })
  const calls = []
  let sequence = 0
  globalThis.fetch = async (path, options = {}) => {
    const method = options.method || 'GET'
    calls.push({ path, method, body: options.body && JSON.parse(options.body) })
    let status = 200, body
    if (method === 'GET') {
      status = failList ? 503 : 200
      body = failList ? { error: 'registry unavailable' } : { enabled, tokens }
    } else if (path.endsWith('/revoke')) {
      status = failRevoke ? 503 : 200
      const id = path.split('/').at(-2)
      const revoked = { ...tokens.find((token) => token.id === id), revoked_at: new Date().toISOString(), revoked_by: 'admin' }
      if (!failRevoke) tokens = tokens.map((token) => token.id === id ? revoked : token)
      body = failRevoke ? { error: 'revocation failed' } : revoked
    } else {
      status = failIssue ? 503 : 201
      const request = JSON.parse(options.body)
      const token = { ...request, id: String(++sequence), project_id: 'test-project', created_by: 'admin', created_at: new Date().toISOString(), expires_at: new Date(Date.now() + request.expires_in_seconds * 1000).toISOString() }
      if (!failIssue) tokens = [token, ...tokens]
      body = failIssue ? { error: 'issuance uncertain' } : { token, secret: 'disposable-test-secret' }
    }
    return { ok: status < 400, status, text: async () => JSON.stringify(body) }
  }
  const root = document.getElementById('root')
  t.after(async () => {
    await act(() => render(null, root))
    Object.assign(globalThis, previous)
  })
  await act(async () => { render(<ProjectTokens pid="test-project" />, root) })
  await settle()
  const button = (text) => {
    const found = [...root.querySelectorAll('button')].find((node) => node.textContent === text)
    assert.ok(found, `missing button ${text}`)
    return found
  }
  const click = async (text) => {
    const node = button(text)
    assert.equal(node.disabled, false, `${text} unexpectedly disabled`)
    await act(async () => node.dispatchEvent(new window.Event('click', { bubbles: true })))
    await settle()
  }
  const submit = async (name = 'My agent') => {
    const input = root.querySelector('[aria-label="Token name"]')
    await act(() => { input.value = name; input.dispatchEvent(new window.Event('input', { bubbles: true })) })
    await act(async () => root.querySelector('form').dispatchEvent(new window.Event('submit', { bubbles: true, cancelable: true })))
    await settle()
  }
  return { root, calls, button, click, submit }
}

test('token screen issues once, hides secrets, confirms/cancels revocation and retains history', async (t) => {
  const { root, calls, button, click, submit } = await mount(t)
  await submit()
  assert.deepEqual(calls.find((call) => call.method === 'POST'), { path: '/api/v1/projects/test-project/tokens', method: 'POST', body: { name: 'My agent', role: 'viewer', expires_in_seconds: 2592000 } })
  assert.equal(root.querySelector('textarea').value, 'disposable-test-secret')
  assert.equal(button('Issue token').disabled, true)
  await click('I saved it — hide secret')
  assert.equal(root.querySelector('textarea'), null)
  assert.equal(button('Issue token').disabled, false)
  await click('Revoke')
  assert.match(root.textContent, /This cannot be undone/)
  await click('Keep token')
  assert.equal(calls.filter((call) => call.path.endsWith('/revoke')).length, 0)
  await click('Revoke')
  await click('Confirm revocation')
  assert.equal(calls.filter((call) => call.path.endsWith('/revoke')).length, 1)
  assert.match(root.textContent, /Token revoked/)
  assert.match(root.textContent, /Revoked/)
  assert.equal(button('Revoke').disabled, true)
  await click('Reload token status')
  assert.match(root.textContent, /My agent/)
  assert.equal(root.querySelector('textarea'), null)
})

test('revoking the newly issued token removes its displayed secret', async (t) => {
  const { root, click, submit } = await mount(t)
  await submit()
  await click('Revoke')
  await click('Confirm revocation')
  assert.equal(root.querySelector('textarea'), null)
})

test('legacy mode and unavailable registries cannot issue via the screen', async (t) => {
  await t.test('legacy', async (t) => {
    const { root, button } = await mount(t, { enabled: false })
    assert.equal(button('Issue token').disabled, true)
    assert.match(root.textContent, /disabled in legacy mode/)
  })
  await t.test('unavailable', async (t) => {
    const { root } = await mount(t, { failList: true })
    assert.equal(root.querySelector('form'), null)
    assert.match(root.textContent, /registry unavailable/)
  })
})

test('issuance errors do not leak a secret or claim success', async (t) => {
  const { root, button, submit } = await mount(t, { failIssue: true })
  await submit()
  assert.match(root.textContent, /issuance uncertain/)
  assert.match(root.textContent, /reload and revoke/)
  assert.equal(root.querySelector('textarea'), null)
  assert.equal(button('Issue token').disabled, false)
})

test('failed revocation preserves the active record and permits a retry', async (t) => {
  const { root, button, submit, click } = await mount(t, { failRevoke: true })
  await submit()
  await click('Revoke')
  await click('Confirm revocation')
  assert.match(root.textContent, /revocation failed/)
  assert.doesNotMatch(root.textContent, /Token revoked/)
  assert.equal(button('Confirm revocation').disabled, false)
  assert.equal(button('Revoke').disabled, false)
})
