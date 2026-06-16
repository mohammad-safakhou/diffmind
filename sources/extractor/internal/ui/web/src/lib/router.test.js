import { describe, expect, it } from 'vitest'
import { parseRoute } from './router.js'

describe('parseRoute', () => {
  it('uses the architecture catalog as the root surface', () => {
    expect(parseRoute('/')).toEqual({ name: 'architecture' })
  })

  it('routes automation runs separately from run detail', () => {
    expect(parseRoute('/runs')).toEqual({ name: 'runs' })
    expect(parseRoute('/runs/run%201')).toEqual({ name: 'detail', runID: 'run 1' })
  })

  it('falls back to the architecture catalog', () => {
    expect(parseRoute('/unknown')).toEqual({ name: 'architecture' })
  })
})
