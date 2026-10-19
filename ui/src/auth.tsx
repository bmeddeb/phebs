/* oxlint-disable react/only-export-components */
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react'
import { AUTH_REQUIRED_EVENT, csrfHeaders, setCSRFToken } from './authSession'

export interface AuthUser {
  id: string
  email: string
  display_name?: string
  is_admin: boolean
}

export interface AuthStatus {
  authenticated: boolean
  auth_required: boolean
  setup_required: boolean
  oidc_enabled: boolean
  password_enabled: boolean
  csrf_token?: string
  user?: AuthUser
}

interface AuthContextValue {
  status: AuthStatus | null
  loading: boolean
  error: string
  refresh: () => Promise<void>
  login: (email: string, password: string) => Promise<void>
  setup: (email: string, password: string, setupToken: string) => Promise<void>
  logout: () => Promise<void>
}

const AuthContext = createContext<AuthContextValue | null>(null)

async function authRequest(url: string, init: RequestInit = {}): Promise<Response> {
  return fetch(url, { credentials: 'same-origin', ...init })
}

async function readStatus(res: Response): Promise<AuthStatus> {
  if (!res.ok) {
    throw new Error(await responseError(res, `authentication failed (${res.status})`))
  }
  const next: unknown = await res.json()
  if (!next || typeof next !== 'object' || Array.isArray(next)) throw new Error('Invalid authentication status. Retry authentication.')
  const value = next as Record<string, unknown>
  if (['authenticated', 'auth_required', 'setup_required', 'oidc_enabled', 'password_enabled'].some(key => typeof value[key] !== 'boolean') ||
    (value.csrf_token !== undefined && typeof value.csrf_token !== 'string')) throw new Error('Invalid authentication status. Retry authentication.')
  if (value.user !== undefined) {
    const user = value.user as Record<string, unknown> | null
    if (!user || typeof user !== 'object' || Array.isArray(user) || typeof user.id !== 'string' || typeof user.email !== 'string' ||
      typeof user.is_admin !== 'boolean' || (user.display_name !== undefined && typeof user.display_name !== 'string')) {
      throw new Error('Invalid authentication status. Retry authentication.')
    }
  }
  return value as unknown as AuthStatus
}

async function responseError(res: Response, fallback: string): Promise<string> {
  const body = await res.text()
  if (!body) return fallback
  try {
    const parsed = JSON.parse(body) as { error?: unknown }
    if (typeof parsed.error === 'string' && parsed.error) return parsed.error
  } catch {
    // Plain-text proxy errors are still useful to the operator.
  }
  return body
}

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const [status, setStatus] = useState<AuthStatus | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const statusGeneration = useRef({ value: 0 })
  const actionGeneration = useRef({ value: 0, controller: null as AbortController | null })

  const applyStatus = useCallback((next: AuthStatus) => {
    setCSRFToken(next.csrf_token)
    setStatus(next)
    setError('')
  }, [])

  const refresh = useCallback(async () => {
    const generation = ++statusGeneration.current.value
    try {
      const next = await readStatus(await authRequest('/api/auth/status'))
      if (generation === statusGeneration.current.value) applyStatus(next)
    } catch (cause) {
      if (generation !== statusGeneration.current.value) return
      setCSRFToken()
      setStatus(null)
      setError(String(cause))
    } finally {
      if (generation === statusGeneration.current.value) setLoading(false)
    }
  }, [applyStatus])

  useEffect(() => {
    void refresh()
    const handleRequired = () => void refresh()
    window.addEventListener(AUTH_REQUIRED_EVENT, handleRequired)
    const pending = statusGeneration.current
    const actions = actionGeneration.current
    return () => {
      pending.value++
      actions.value++
      actions.controller?.abort()
      window.removeEventListener(AUTH_REQUIRED_EVENT, handleRequired)
    }
  }, [refresh])

  const authenticate = useCallback(async (url: string, body: Record<string, string>) => {
    const actions = actionGeneration.current
    const generation = ++actions.value
    actions.controller?.abort()
    const controller = new AbortController()
    actions.controller = controller
    statusGeneration.current.value++
    try {
      const next = await readStatus(await authRequest(url, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
        signal: controller.signal,
      }))
      if (generation !== actions.value) return
      statusGeneration.current.value++
      applyStatus(next)
      setLoading(false)
    } catch (cause) {
      if (generation === actions.value) throw cause
    } finally {
      if (generation === actions.value) actions.controller = null
    }
  }, [applyStatus])

  const login = useCallback((email: string, password: string) =>
    authenticate('/api/auth/login', { email, password }), [authenticate])

  const setup = useCallback((email: string, password: string, setupToken: string) =>
    authenticate('/api/auth/setup', { email, password, setup_token: setupToken }), [authenticate])

  const logout = useCallback(async () => {
    const actions = actionGeneration.current
    const generation = ++actions.value
    actions.controller?.abort()
    const controller = new AbortController()
    actions.controller = controller
    statusGeneration.current.value++
    try {
      const res = await authRequest('/api/auth/logout', {
        method: 'POST',
        headers: csrfHeaders(),
        signal: controller.signal,
      })
      if (generation !== actionGeneration.current.value) return
      if (!res.ok) throw new Error(await responseError(res, `logout failed (${res.status})`))
      await refresh()
    } catch (cause) {
      if (generation !== actionGeneration.current.value) return
      setError(cause instanceof Error ? cause.message : String(cause))
      throw cause
    } finally {
      if (generation === actions.value) actions.controller = null
    }
  }, [refresh])

  const value = useMemo<AuthContextValue>(() => ({
    status,
    loading,
    error,
    refresh,
    login,
    setup,
    logout,
  }), [status, loading, error, refresh, login, setup, logout])

  return <AuthContext value={value}>{children}</AuthContext>
}

export function useAuth(): AuthContextValue {
  const value = useContext(AuthContext)
  if (!value) throw new Error('useAuth must be used within AuthProvider')
  return value
}
