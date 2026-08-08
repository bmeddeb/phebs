// One exact scope — repository, service key, and the service generation it
// was acquired under — read from the URL (T43.8). The ScopeContextBar
// renders it; these helpers keep it in the URL across transitions (charter
// §2 deep-link discipline). The generation is the fence: when publication
// moves the active identity, the bar says so instead of silently adopting
// different authority under an unchanged URL.

export interface ActiveScope {
  repository: string
  serviceKey: string
  generation: string
}

/**
 * Reads the active scope from any route's params. The Workbench consumes
 * scope under its own param names (service_repository / source_service);
 * both spellings are one scope.
 */
export function scopeFromParams(params: URLSearchParams): ActiveScope | null {
  const repository = params.get('repository') ?? params.get('service_repository') ?? ''
  if (!repository) return null
  return {
    repository,
    serviceKey: params.get('service_key') ?? params.get('source_service') ?? '',
    generation: params.get('scope_generation') ?? '',
  }
}

/** Scope params appended to a navigation target so transitions preserve it. */
export function scopeParams(scope: ActiveScope | null): Record<string, string> {
  if (!scope) return {}
  const params: Record<string, string> = {}
  if (scope.repository) params.repository = scope.repository
  if (scope.serviceKey) params.service_key = scope.serviceKey
  if (scope.generation) params.scope_generation = scope.generation
  return params
}

/** The same scope in the Workbench's own param vocabulary. */
export function workbenchScopeParams(scope: ActiveScope | null): Record<string, string> {
  if (!scope) return {}
  const params: Record<string, string> = {}
  if (scope.repository) params.service_repository = scope.repository
  if (scope.serviceKey) params.source_service = scope.serviceKey
  if (scope.generation) params.scope_generation = scope.generation
  return params
}

/** Every param spelling that carries scope, for explicit clearing. */
export const SCOPE_PARAM_KEYS = [
  'repository',
  'service_key',
  'scope_generation',
  'service_repository',
  'source_service',
  'scope',
] as const

export interface RecentScope {
  repository: string
  serviceKey: string
  generation: string
}

const RECENT_KEY_PREFIX = 'phebs-recent-scopes:'
const RECENT_LIMIT = 5

// Recency is keyed by the authenticated principal so one browser's users
// never see each other's scope identities, and it is recorded only after an
// authorized read succeeded (the scope bar's authority fetch) — a URL alone
// proves nothing.
const recentKey = (principal: string) => `${RECENT_KEY_PREFIX}${principal}`

/** Bounded, identity-only recency: repository/key/generation, never authority. */
export function rememberScope(principal: string, scope: ActiveScope | null) {
  if (!principal || !scope?.repository) return
  try {
    const current: RecentScope[] = JSON.parse(localStorage.getItem(recentKey(principal)) ?? '[]')
    const next = [
      { repository: scope.repository, serviceKey: scope.serviceKey, generation: scope.generation },
      ...current.filter((entry) =>
        entry.repository !== scope.repository || entry.serviceKey !== scope.serviceKey),
    ].slice(0, RECENT_LIMIT)
    localStorage.setItem(recentKey(principal), JSON.stringify(next))
  } catch {
    // Storage may be unavailable; recency is an accelerator, never required.
  }
}

export function recentScopes(principal: string): RecentScope[] {
  if (!principal) return []
  try {
    const parsed: unknown = JSON.parse(localStorage.getItem(recentKey(principal)) ?? '[]')
    if (!Array.isArray(parsed)) return []
    return parsed
      .filter((entry): entry is RecentScope =>
        typeof entry === 'object' && entry !== null &&
        typeof (entry as RecentScope).repository === 'string')
      .slice(0, RECENT_LIMIT)
  } catch {
    return []
  }
}
