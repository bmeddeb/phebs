// One exact scope — repository and service key — read from the URL (T43.8).
// The ScopeContextBar renders it; these helpers keep it in the URL across
// transitions (charter §2 deep-link discipline).

export interface ActiveScope {
  repository: string
  serviceKey: string
}

/** Scope params appended to a navigation target so transitions preserve it. */
export function scopeParams(scope: ActiveScope | null): Record<string, string> {
  if (!scope) return {}
  const params: Record<string, string> = {}
  if (scope.repository) params.repository = scope.repository
  if (scope.serviceKey) params.service_key = scope.serviceKey
  return params
}
