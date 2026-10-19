import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, test, vi } from 'vitest'
import { Client } from 'styletron-engine-monolithic'
import { Provider as StyletronProvider } from 'styletron-react'
import { BaseProvider, LightTheme } from 'baseui'
import { AuthProvider, useAuth } from './auth'
import LoginPage from './pages/LoginPage'
import { csrfHeaders, notifyAuthRequired, setCSRFToken } from './authSession'

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
        <AuthProvider><LoginBoundary /></AuthProvider>
      </BaseProvider>
    </StyletronProvider>,
  )
}

function LoginBoundary() {
  const { status: current } = useAuth()
  return current?.authenticated ? <div>Authenticated application</div> : <LoginPage />
}

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  setCSRFToken()
})

function deferredStatus() {
  let resolve!: (response: ReturnType<typeof jsonResponse>) => void
  let reject!: (error: Error) => void
  const promise = new Promise<ReturnType<typeof jsonResponse>>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}

function SessionProbe() {
  const auth = useAuth()
  return <>
    <div>{auth.status?.authenticated ? 'Signed in' : 'Signed out'}</div>
    <button onClick={() => void auth.refresh()}>Refresh session</button>
    <button onClick={() => void auth.login('reader@example.com', 'password')}>Login</button>
    <button onClick={() => void auth.setup('reader@example.com', 'password', 'setup')}>Setup</button>
    <button onClick={() => void auth.logout().catch(() => {})}>Logout</button>
    <div>{auth.error}</div>
  </>
}

test.each(['success', 'failure'])('late auth-status %s cannot replace a newer signed-out status', async outcome => {
  const older = deferredStatus()
  const newer = deferredStatus()
  vi.stubGlobal('fetch', vi.fn().mockReturnValueOnce(older.promise).mockReturnValueOnce(newer.promise))
  render(<AuthProvider><SessionProbe /></AuthProvider>)
  fireEvent.click(screen.getByRole('button', { name: 'Refresh session' }))
  await act(async () => newer.resolve(jsonResponse(status({ csrf_token: 'current' }))))
  await act(async () => {
    if (outcome === 'success') older.resolve(jsonResponse(status({ authenticated: true, csrf_token: 'stale' })))
    else older.reject(new Error('stale failure'))
  })
  expect(screen.getByText('Signed out')).toBeTruthy()
  expect(csrfHeaders()).toEqual({ 'X-CSRF-Token': 'current' })
})

test.each(['Login', 'Setup'])('late status cannot undo completed %s', async action => {
  const older = deferredStatus()
  vi.stubGlobal('fetch', vi.fn()
    .mockResolvedValueOnce(jsonResponse(status()))
    .mockReturnValueOnce(older.promise)
    .mockResolvedValueOnce(jsonResponse(status({ authenticated: true, csrf_token: 'new-session' }))))
  render(<AuthProvider><SessionProbe /></AuthProvider>)
  await act(async () => {})
  fireEvent.click(screen.getByRole('button', { name: 'Refresh session' }))
  fireEvent.click(screen.getByRole('button', { name: action }))
  expect(await screen.findByText('Signed in')).toBeTruthy()
  await act(async () => older.resolve(jsonResponse(status())))
  expect(screen.getByText('Signed in')).toBeTruthy()
  expect(csrfHeaders()).toEqual({ 'X-CSRF-Token': 'new-session' })
})

test('late authenticated status cannot restore a logged-out session', async () => {
  const older = deferredStatus()
  vi.stubGlobal('fetch', vi.fn()
    .mockResolvedValueOnce(jsonResponse(status({ authenticated: true, csrf_token: 'old-session' })))
    .mockReturnValueOnce(older.promise)
    .mockResolvedValueOnce({ ok: true, status: 204 })
    .mockResolvedValueOnce(jsonResponse(status())))
  render(<AuthProvider><SessionProbe /></AuthProvider>)
  expect(await screen.findByText('Signed in')).toBeTruthy()
  act(() => notifyAuthRequired())
  fireEvent.click(screen.getByRole('button', { name: 'Logout' }))
  expect(await screen.findByText('Signed out')).toBeTruthy()
  await act(async () => older.resolve(jsonResponse(status({ authenticated: true, csrf_token: 'old-session' }))))
  expect(screen.getByText('Signed out')).toBeTruthy()
  expect(csrfHeaders()).toEqual({})
})

test('an unmounted provider cannot replace the current CSRF token', async () => {
  const older = deferredStatus()
  vi.stubGlobal('fetch', vi.fn().mockReturnValueOnce(older.promise))
  const view = render(<AuthProvider><SessionProbe /></AuthProvider>)
  view.unmount()
  setCSRFToken('replacement-provider')
  await act(async () => older.resolve(jsonResponse(status({ authenticated: true, csrf_token: 'stale' }))))
  expect(csrfHeaders()).toEqual({ 'X-CSRF-Token': 'replacement-provider' })
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

test('password and OIDC login methods are separated without changing either action', async () => {
  const fetchMock = vi.fn().mockResolvedValue(jsonResponse(status({
    oidc_enabled: true,
    password_enabled: true,
  })))
  vi.stubGlobal('fetch', fetchMock)
  renderLogin()

  expect(await screen.findByRole('button', { name: 'Sign in' })).toBeTruthy()
  expect(screen.getByText('or')).toBeTruthy()
  expect(screen.getByRole('button', { name: 'Continue with SSO' })).toBeTruthy()
})

test('first-run setup normalizes operator input and enters the authenticated application', async () => {
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

  fireEvent.change(await screen.findByRole('textbox', { name: 'Email' }), { target: { value: '  admin@example.com  ' } })
  fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'long enough password' } })
  fireEvent.change(screen.getByLabelText('Setup token'), { target: { value: '  operator-token  ' } })
  fireEvent.click(screen.getByRole('button', { name: 'Create administrator' }))

  expect(await screen.findByText('Authenticated application')).toBeTruthy()
  expect(screen.queryByRole('button', { name: 'Create administrator' })).toBeNull()
  expect(fetchMock).toHaveBeenCalledTimes(2)
  const [url, init] = fetchMock.mock.calls[1] as [string, RequestInit]
  expect(url).toBe('/api/auth/setup')
  expect(init.credentials).toBe('same-origin')
  expect(JSON.parse(String(init.body))).toEqual({
    email: 'admin@example.com',
    password: 'long enough password',
    setup_token: 'operator-token',
  })
})

test('first-run setup reports a missing token instead of silently doing nothing', async () => {
  const fetchMock = vi.fn()
    .mockResolvedValueOnce(jsonResponse(status({ setup_required: true, password_enabled: false })))
  vi.stubGlobal('fetch', fetchMock)
  renderLogin()

  fireEvent.change(await screen.findByRole('textbox', { name: 'Email' }), { target: { value: 'admin@example.com' } })
  fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'long enough password' } })
  fireEvent.click(screen.getByRole('button', { name: 'Create administrator' }))

  expect(await screen.findByText('Enter the setup token from the server log.')).toBeTruthy()
  expect(fetchMock).toHaveBeenCalledTimes(1)
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
      signal: expect.any(AbortSignal),
    },
  ])
})

test.each(['Login', 'Setup', 'Logout'])('unmounted %s cannot replace another provider token', async action => {
  const pending = deferredStatus()
  vi.stubGlobal('fetch', vi.fn().mockResolvedValueOnce(jsonResponse(status())).mockReturnValueOnce(pending.promise))
  const view = render(<AuthProvider><SessionProbe /></AuthProvider>)
  await act(async () => {})
  fireEvent.click(screen.getByRole('button', { name: action }))
  const signal = (vi.mocked(fetch).mock.calls[1][1] as RequestInit).signal!
  view.unmount()
  expect(signal.aborted).toBe(true)
  setCSRFToken('replacement-provider')
  await act(async () => pending.resolve(jsonResponse(status({ authenticated: true, csrf_token: 'obsolete-action' }))))
  expect(csrfHeaders()).toEqual({ 'X-CSRF-Token': 'replacement-provider' })
})

test('a pending login cannot restore the session after logout', async () => {
  const pending = deferredStatus()
  vi.stubGlobal('fetch', vi.fn().mockResolvedValueOnce(jsonResponse(status())).mockReturnValueOnce(pending.promise)
    .mockResolvedValueOnce(jsonResponse({})).mockResolvedValueOnce(jsonResponse(status({ csrf_token: 'signed-out' }))))
  render(<AuthProvider><SessionProbe /></AuthProvider>)
  await act(async () => {})
  fireEvent.click(screen.getByRole('button', { name: 'Login' }))
  fireEvent.click(screen.getByRole('button', { name: 'Logout' }))
  await act(async () => {})
  await act(async () => pending.resolve(jsonResponse(status({ authenticated: true, csrf_token: 'obsolete-login' }))))
  expect(screen.getByText('Signed out')).toBeTruthy()
  expect(csrfHeaders()).toEqual({ 'X-CSRF-Token': 'signed-out' })
})

test.each(['success', 'failure'])('obsolete logout %s cannot replace a newer login', async outcome => {
  const pending = deferredStatus()
  const fetchMock = vi.fn().mockResolvedValueOnce(jsonResponse(status({ authenticated: true, csrf_token: 'original' })))
    .mockReturnValueOnce(pending.promise).mockResolvedValueOnce(jsonResponse(status({ authenticated: true, csrf_token: 'new-login' })))
    .mockResolvedValue(jsonResponse(status()))
  vi.stubGlobal('fetch', fetchMock)
  render(<AuthProvider><SessionProbe /></AuthProvider>)
  await screen.findByText('Signed in')
  fireEvent.click(screen.getByRole('button', { name: 'Logout' }))
  fireEvent.click(screen.getByRole('button', { name: 'Login' }))
  await act(async () => {})
  await act(async () => pending.resolve(jsonResponse(outcome === 'success' ? {} : { error: 'obsolete logout failure' }, outcome === 'success')))
  expect(screen.getByText('Signed in')).toBeTruthy()
  expect(screen.queryByText('obsolete logout failure')).toBeNull()
  expect(csrfHeaders()).toEqual({ 'X-CSRF-Token': 'new-login' })
  expect(fetchMock).toHaveBeenCalledTimes(3)
})

test('authentication status failure can retry without reloading the page', async () => {
  const fetchMock = vi.fn().mockRejectedValueOnce(new Error('status temporarily offline')).mockResolvedValueOnce(jsonResponse(status()))
  vi.stubGlobal('fetch', fetchMock)
  renderLogin()
  await screen.findByText(/status temporarily offline/)
  fireEvent.click(screen.getByRole('button', { name: 'Retry authentication' }))
  await screen.findByRole('textbox', { name: 'Email' })
  expect(screen.queryByText(/status temporarily offline/)).toBeNull()
  expect(fetchMock).toHaveBeenCalledTimes(2)
})

test.each([
  { auth_required: 'false' }, { csrf_token: {} }, { user: { id: 'crossed', is_admin: 'yes' } },
])('invalid authentication fields %j cannot establish a session', async invalid => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(status(invalid))))
  renderLogin()
  expect(await screen.findByText(/Invalid authentication status/)).toBeTruthy()
  expect(screen.queryByText('Authenticated application')).toBeNull()
  expect(csrfHeaders()).toEqual({})
})
