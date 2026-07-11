/* oxlint-disable react/only-export-components */
import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react'
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
  return res.json() as Promise<AuthStatus>
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

  const applyStatus = useCallback((next: AuthStatus) => {
    setCSRFToken(next.csrf_token)
    setStatus(next)
    setError('')
  }, [])

  const refresh = useCallback(async () => {
    try {
      applyStatus(await readStatus(await authRequest('/api/auth/status')))
    } catch (cause) {
      setCSRFToken()
      setStatus(null)
      setError(String(cause))
    } finally {
      setLoading(false)
    }
  }, [applyStatus])

  useEffect(() => {
    void refresh()
    const handleRequired = () => void refresh()
    window.addEventListener(AUTH_REQUIRED_EVENT, handleRequired)
    return () => window.removeEventListener(AUTH_REQUIRED_EVENT, handleRequired)
  }, [refresh])

  const login = useCallback(async (email: string, password: string) => {
    const next = await readStatus(await authRequest('/api/auth/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ email, password }),
    }))
    applyStatus(next)
  }, [applyStatus])

  const setup = useCallback(async (email: string, password: string, setupToken: string) => {
    const next = await readStatus(await authRequest('/api/auth/setup', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ email, password, setup_token: setupToken }),
    }))
    applyStatus(next)
  }, [applyStatus])

  const logout = useCallback(async () => {
    try {
      const res = await authRequest('/api/auth/logout', {
        method: 'POST',
        headers: csrfHeaders(),
      })
      if (!res.ok) throw new Error(await responseError(res, `logout failed (${res.status})`))
      await refresh()
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause))
      throw cause
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
