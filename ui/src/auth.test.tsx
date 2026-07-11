import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, test, vi } from 'vitest'
import { Client } from 'styletron-engine-monolithic'
import { Provider as StyletronProvider } from 'styletron-react'
import { BaseProvider, LightTheme } from 'baseui'
import { AuthProvider, useAuth } from './auth'
import LoginPage from './pages/LoginPage'

const engine = new Client()

const status = (overrides: Record<string, unknown> = {}) => ({
  authenticated: false,
  auth_required: true,
  setup_required: false,
  oidc_enabled: false,
  password_enabled: true,
  ...overrides,
})

const jsonResponse = (body: unknown, ok = true) => ({
  ok,
  status: ok ? 200 : 401,
  json: async () => body,
  text: async () => JSON.stringify(body),
})

function renderLogin() {
  return render(
    <StyletronProvider value={engine}>
      <BaseProvider theme={LightTheme}>
        <AuthProvider><LoginPage /></AuthProvider>
      </BaseProvider>
    </StyletronProvider>,
  )
}

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

test('OIDC-only login hides unusable password controls', async () => {
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(status({
    oidc_enabled: true,
    password_enabled: false,
  })))
  vi.stubGlobal('fetch', fetchMock)
  renderLogin()

  expect(await screen.findByRole('button', { name: 'Continue with SSO' })).toBeTruthy()
  expect(screen.queryByRole('textbox', { name: 'Email' })).toBeNull()
  expect(fetchMock).toHaveBeenCalledWith('/api/auth/status', { credentials: 'same-origin' })
})

test('first-run setup sends the operator token and establishes auth state', async () => {
  const fetchMock = vi.fn()
    .mockResolvedValueOnce(jsonResponse(status({ setup_required: true, password_enabled: false })))
    .mockResolvedValueOnce(jsonResponse(status({
      authenticated: true,
      password_enabled: true,
      csrf_token: 'csrf-1',
      user: { id: 'u1', email: 'admin@example.com', is_admin: true },
    })))
  vi.stubGlobal('fetch', fetchMock)
  renderLogin()

  fireEvent.change(await screen.findByRole('textbox', { name: 'Email' }), { target: { value: 'admin@example.com' } })
  fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'long enough password' } })
  fireEvent.change(screen.getByLabelText('Setup token'), { target: { value: 'operator-token' } })
  fireEvent.click(screen.getByRole('button', { name: 'Create administrator' }))

  await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2))
  const [url, init] = fetchMock.mock.calls[1] as [string, RequestInit]
  expect(url).toBe('/api/auth/setup')
  expect(init.credentials).toBe('same-origin')
  expect(JSON.parse(String(init.body))).toEqual({
    email: 'admin@example.com',
    password: 'long enough password',
    setup_token: 'operator-token',
  })
})

test('login renders the backend error message without raw JSON', async () => {
  const fetchMock = vi.fn()
    .mockResolvedValueOnce(jsonResponse(status()))
    .mockResolvedValueOnce(jsonResponse({ error: 'invalid email or password' }, false))
  vi.stubGlobal('fetch', fetchMock)
  renderLogin()

  fireEvent.change(await screen.findByRole('textbox', { name: 'Email' }), { target: { value: 'admin@example.com' } })
  fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'wrong' } })
  fireEvent.click(screen.getByRole('button', { name: 'Sign in' }))

  expect(await screen.findByText('invalid email or password')).toBeTruthy()
  expect(document.body.textContent).not.toContain('{"error"')
})

function LogoutProbe() {
  const { status: current, logout } = useAuth()
  if (!current) return null
  return <button onClick={() => void logout()}>{current.authenticated ? 'Logout now' : 'Logged out'}</button>
}

test('session logout carries the CSRF token returned by auth status', async () => {
  const fetchMock = vi.fn()
    .mockResolvedValueOnce(jsonResponse(status({ authenticated: true, csrf_token: 'csrf-logout' })))
    .mockResolvedValueOnce({ ok: true, status: 204, text: async () => '' })
    .mockResolvedValueOnce(jsonResponse(status()))
  vi.stubGlobal('fetch', fetchMock)
  render(<AuthProvider><LogoutProbe /></AuthProvider>)

  fireEvent.click(await screen.findByRole('button', { name: 'Logout now' }))
  expect(await screen.findByRole('button', { name: 'Logged out' })).toBeTruthy()
  expect(fetchMock.mock.calls[1]).toEqual([
    '/api/auth/logout',
    {
      credentials: 'same-origin',
      method: 'POST',
      headers: { 'X-CSRF-Token': 'csrf-logout' },
    },
  ])
})
