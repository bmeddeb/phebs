import type {
  WorkbenchImpactFilters,
  WorkbenchImplementationAnchor,
} from '../api'

export interface WorkbenchEvidenceInput {
  compatibilityRun: string
  filters: WorkbenchImpactFilters
  anchors: WorkbenchImplementationAnchor[]
}

export const defaultWorkbenchEvidenceInput = (): WorkbenchEvidenceInput => ({
  compatibilityRun: '',
  filters: {
    freshness: 'any',
    resolution: 'any',
    ordering: 'source',
    level: 'occurrence',
  },
  anchors: [],
})
