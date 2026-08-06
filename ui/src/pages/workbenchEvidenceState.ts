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

export function workbenchEvidenceInputFromRoute(
  params: URLSearchParams,
): WorkbenchEvidenceInput {
  const value = defaultWorkbenchEvidenceInput()
  const service_repository = params.get('service_repository')?.trim() ?? ''
  const source_service = params.get('source_service')?.trim() ?? ''
  const target_service = params.get('target_service')?.trim() ?? ''
  return {
    ...value,
    filters: {
      ...value.filters,
      ...(service_repository ? { service_repository } : {}),
      ...(source_service ? { source_service } : {}),
      ...(target_service ? { target_service } : {}),
    },
  }
}

export function workbenchServiceRouteParams(
  filters: WorkbenchImpactFilters,
): Record<string, string> {
  const values: Record<string, string> = {}
  for (const key of [
    'service_repository',
    'source_service',
    'target_service',
  ] as const) {
    const value = filters[key]?.trim()
    if (value) values[key] = value
  }
  return values
}
