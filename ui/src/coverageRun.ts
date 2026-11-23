import type { CoverageRun } from './api'

// runServed reports current publication authority: a legacy published run
// or a domain served from its fenced partitioned root (PHEBS-37). It lives
// outside ./api so page tests that mock the network module keep it real.
export function runServed(run: CoverageRun): boolean {
  return run.status === 'published' || run.status === 'partitioned'
}
