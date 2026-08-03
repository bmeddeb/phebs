import type { AnalysisScopeProjection, RepoStatus } from '../api'

export function analysisScopeFromRepoStatus(
  repository: RepoStatus,
): AnalysisScopeProjection {
  return {
    repository: repository.name,
    commit: repository.indexed_commit_hash,
    scope_posture: repository.analysis_unit?.search_index_posture ??
      'whole-repository',
    analysis_unit: repository.analysis_unit,
  }
}
