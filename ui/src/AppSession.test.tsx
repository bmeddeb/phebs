import { act, useState } from 'react'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, test, vi } from 'vitest'
import { Client } from 'styletron-engine-monolithic'
import { Provider as StyletronProvider } from 'styletron-react'
import { BaseProvider, LightTheme } from 'baseui'
import App from './App'
import { AuthProvider } from './auth'
import { notifyAuthRequired, setCSRFToken } from './authSession'

const api = vi.hoisted(() => ({ fetchVersion: vi.fn() }))
vi.mock('./api', async importOriginal => ({ ...await importOriginal<typeof import('./api')>(), fetchVersion: api.fetchVersion }))
vi.mock('./pages/SettingsPage', () => ({ default: function PrivateSettings() {
  const [secret, setSecret] = useState('')
  return <><button onClick={() => setSecret('first-user-secret')}>Create private key</button><div>{secret}</div></>
} }))
const engine = new Client()
afterEach(() => { cleanup(); vi.unstubAllGlobals(); setCSRFToken() })

test.each(['user', 'role', 'same'])('a %s refresh owns its Settings state and capability read', async change => {
  const original = { id: 'first', email: 'first@example.test', is_admin: false }
  const replacement = change === 'user' ? { ...original, id: 'second', email: 'second@example.test' }
    : change === 'role' ? { ...original, is_admin: true } : original
  const status = (user: typeof original) => ({ authenticated: true, auth_required: true, setup_required: false,
    oidc_enabled: false, password_enabled: true, user, csrf_token: user.id + '-token' })
  vi.stubGlobal('fetch', vi.fn().mockResolvedValueOnce({ ok: true, json: async () => status(original) })
    .mockResolvedValue({ ok: true, json: async () => ({ ...status(replacement), csrf_token: 'rotated-token' }) }))
  api.fetchVersion.mockReset().mockResolvedValue({ capabilities: [] })
  window.location.hash = '#/settings'
  render(<StyletronProvider value={engine}><BaseProvider theme={LightTheme}><AuthProvider><App /></AuthProvider></BaseProvider></StyletronProvider>)
  fireEvent.click(await screen.findByRole('button', { name: 'Create private key' }))
  await screen.findByText('first-user-secret')
  await act(async () => notifyAuthRequired())
  await waitFor(() => expect(api.fetchVersion).toHaveBeenCalledTimes(change === 'same' ? 1 : 2))
  if (change === 'same') expect(screen.getByText('first-user-secret')).toBeTruthy()
  else expect(screen.queryByText('first-user-secret')).toBeNull()
})
