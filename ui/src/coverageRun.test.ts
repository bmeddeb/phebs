import { describe, expect, it } from 'vitest'
import type { CoverageRun } from './api'
import { runServed } from './coverageRun'

const run = (status: string): CoverageRun => ({ domain: 'scip-thrift-field', status } as CoverageRun)

describe('runServed', () => {
  it('treats legacy published and partitioned roots as served (PHEBS-37)', () => {
    expect(runServed(run('published'))).toBe(true)
    expect(runServed(run('partitioned'))).toBe(true)
    expect(runServed(run('unpublished'))).toBe(false)
    expect(runServed(run('superseded'))).toBe(false)
  })
})
