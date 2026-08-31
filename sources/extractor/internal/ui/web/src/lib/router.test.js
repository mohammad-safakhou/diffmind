import { describe, expect, it } from 'vitest'
import { parseRoute } from './router.js'

describe('parseRoute', () => {
  it('uses the repositories overview as the root surface', () => {
    expect(parseRoute('/')).toEqual({ name: 'repos' })
  })

  it('routes a repository detail by id', () => {
    expect(parseRoute('/repos/abc123')).toEqual({ name: 'repo', repoID: 'abc123' })
  })

  it('routes run detail without a global runs surface', () => {
    expect(parseRoute('/runs')).toEqual({ name: 'repos' })
    expect(parseRoute('/runs/run%201')).toEqual({ name: 'detail', runID: 'run 1' })
  })

  it('falls back to the repositories overview', () => {
    expect(parseRoute('/unknown')).toEqual({ name: 'repos' })
  })
})
