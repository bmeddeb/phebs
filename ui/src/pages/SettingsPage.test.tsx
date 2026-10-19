import { act, useState } from 'react'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { BaseProvider, LightTheme } from 'baseui'
import { Client } from 'styletron-engine-monolithic'
import { Provider as StyletronProvider } from 'styletron-react'
import SettingsPage from './SettingsPage'
import { PaletteContext } from '../theme'
import type { PaletteName } from '../palette'

const api = vi.hoisted(() => ({
  fetchRepoStatus: vi.fn().mockResolvedValue([]),
  createAPIKey: vi.fn(),
  fetchAPIKeys: vi.fn(),
  fetchLifecycleStatus: vi.fn(),
  revokeAPIKey: vi.fn(),
}))

vi.mock('../api', () => api)
vi.mock('../typedIndex', () => ({ fetchTypedProviders: vi.fn().mockResolvedValue({ schema: 'phebs-typed-index-providers-v1', providers: [] }), fetchTypedView: vi.fn() }))

const engine = new Client()

function StatefulPalettePage() {
  const [palette, setPalette] = useState<PaletteName>('phebs')
  return (
    <PaletteContext.Provider value={{ palette, setPalette }}>
      <SettingsPage />
    </PaletteContext.Provider>
  )
}

function renderPage(isAdmin = false) {
  return render(
    <StyletronProvider value={engine}>
      <BaseProvider theme={LightTheme}>
        <SettingsPage isAdmin={isAdmin} />
      </BaseProvider>
    </StyletronProvider>,
  )
}

beforeEach(() => {
  api.fetchAPIKeys.mockReset().mockResolvedValue({
    keys: [
      {
        id: 'write-key',
        name: 'Investigation agent',
        prefix: 'phebs_write',
        capabilities: ['investigation:write'],
        created_at: '2026-07-27T12:00:00Z',
      },
      {
        id: 'read-key',
        name: 'Read-only client',
        prefix: 'phebs_read',
        capabilities: [],
        created_at: '2026-07-27T11:00:00Z',
      },
    ],
  })
  api.createAPIKey.mockReset().mockImplementation(
    async (name: string) => ({
      key: {
        id: 'new-key',
        name,
        prefix: 'phebs_new',
        capabilities: [],
        created_at: '2026-07-27T13:00:00Z',
      },
      token: 'phebs_new.secret',
    }),
  )
  api.revokeAPIKey.mockReset().mockResolvedValue(undefined)
  api.fetchLifecycleStatus.mockReset().mockResolvedValue({
    schema: 'phebs-lifecycle-status-v1',
    policy: {
      enabled: true,
      owners: 13,
      soft_watermark_percent: 80,
      hard_watermark_percent: 90,
      resume_watermark_percent: 75,
      max_candidates_per_turn: 64,
      max_deletes_per_turn: 16,
      max_queries_per_turn: 16,
    },
    capacity: {
      completeness: 'exact',
      pressure: 'collect',
      total_bytes: 1_000,
      used_bytes: 810,
      used_percent: 81,
      observed_at: '2026-08-05T20:00:00Z',
    },
    owners: Array.from({ length: 13 }, (_, index) => ({
      name: `owner-${String(index).padStart(2, '0')}`,
      state: index < 4 ? 'ok' : 'not_run',
      completeness: index < 4 ? 'exact' : 'unavailable',
      scanned: index === 0 ? 11 : 0,
      deleted: index === 0 ? 1 : 0,
      backlog: index === 0,
      attempted_at: index === 0 ? '2026-08-05T19:30:00Z' : undefined,
    })),
  })
})

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

test('Appearance offers every curated palette and commits through the preference (T44.2)', async () => {
  render(
    <StyletronProvider value={engine}>
      <BaseProvider theme={LightTheme}>
        <StatefulPalettePage />
      </BaseProvider>
    </StyletronProvider>,
  )
  expect(await screen.findByRole('heading', { name: 'Appearance' })).toBeTruthy()
  const select = screen.getByRole('combobox', { name: 'Code highlight palette' })
  expect(Array.from(select.querySelectorAll('option')).map((option) => option.textContent)).toEqual([
    'Phebs', 'Quiet', 'Classic', 'High contrast',
  ])
  let phebsColor = ''
  await waitFor(() => {
    expect(document.querySelector('[data-palette-ready="true"]')).toBeTruthy()
    const keyword = Array.from(document.querySelectorAll('pre span span')).find((el) => el.textContent === 'func') as HTMLElement | undefined
    expect(keyword?.style.color).toBeTruthy()
    phebsColor = keyword!.style.color
  })

  fireEvent.change(select, { target: { value: 'classic' } })
  expect((select as HTMLSelectElement).value).toBe('classic')
  // The prior palette's spans are not presented under the new selection.
  expect(document.querySelector('[data-palette-ready="true"]')).toBeNull()
  // The same mounted specimen then re-colors through the lazy tokenizer.
  await waitFor(() => {
    expect(document.querySelector('[data-palette-ready="true"]')).toBeTruthy()
    const keyword = Array.from(document.querySelectorAll('pre span span')).find((el) => el.textContent === 'func') as HTMLElement | undefined
    expect(keyword?.style.color).toBeTruthy()
    expect(keyword!.style.color).not.toBe(phebsColor)
  })
})

test.each(['language', 'tokenizer'] as const)('Appearance explains %s failure without exposing diagnostics (T44.2a)', async (failure) => {
  const lang = await import('../lang')
  const highlight = await import('../highlight')
  const privateFailure = new Error('private preview diagnostic must not render')
  if (failure === 'language') vi.spyOn(lang, 'languageFor').mockRejectedValue(privateFailure)
  else vi.spyOn(highlight, 'tokenize').mockImplementation(() => { throw privateFailure })
  render(
    <StyletronProvider value={engine}>
      <BaseProvider theme={LightTheme}><StatefulPalettePage /></BaseProvider>
    </StyletronProvider>,
  )
  const appearance = within(await screen.findByRole('region', { name: 'Appearance' }))
  expect((await appearance.findByRole('status')).textContent).toBe('Preview unavailable.')
  expect(appearance.getByRole('button', { name: 'Reload page' }).getAttribute('type')).toBe('button')
  expect(screen.queryByText(privateFailure.message)).toBeNull()
  const pre = document.querySelector('pre')!
  expect(pre.textContent).toBe('// verified span\nfunc Count(limit int) error {\n\treturn fmt.Errorf("bound=%d", 42)\n}')
  expect(pre.querySelector('[style]')).toBeNull()
  expect(pre.getAttribute('data-palette-ready')).toBeNull()
  expect(api.fetchAPIKeys).toHaveBeenCalledTimes(1)

  vi.restoreAllMocks()
  fireEvent.change(appearance.getByRole('combobox'), { target: { value: 'quiet' } })
  expect(appearance.queryByRole('status')).toBeNull()
  await waitFor(() => expect(pre.getAttribute('data-palette-ready')).toBe('true'))
  expect(appearance.queryByRole('button', { name: 'Reload page' })).toBeNull()
  expect(api.fetchAPIKeys).toHaveBeenCalledTimes(1)
})

test('a superseded preview rejection cannot replace the current palette (T44.2a)', async () => {
  const lang = await import('../lang')
  const original = lang.languageFor
  let reject!: (error: Error) => void
  const pending = new Promise<never>((_, fail) => { reject = fail })
  const load = vi.spyOn(lang, 'languageFor').mockReturnValue(pending)
  render(
    <StyletronProvider value={engine}>
      <BaseProvider theme={LightTheme}><StatefulPalettePage /></BaseProvider>
    </StyletronProvider>,
  )
  const appearance = within(await screen.findByRole('region', { name: 'Appearance' }))
  await waitFor(() => expect(load).toHaveBeenCalledWith('specimen.go'))
  load.mockImplementation(original)
  fireEvent.change(appearance.getByRole('combobox'), { target: { value: 'classic' } })
  await waitFor(() => expect(document.querySelector('[data-palette-ready="true"]')).toBeTruthy())
  const readyMarkup = document.querySelector('pre')!.innerHTML
  await act(async () => { reject(new Error('late previous preview failure')) })
  expect(document.querySelector('pre')!.innerHTML).toBe(readyMarkup)
  expect(appearance.queryByRole('status')).toBeNull()
  expect(appearance.queryByRole('button', { name: 'Reload page' })).toBeNull()
})

test('lists existing keys as read only without secret material', async () => {
  renderPage()
  expect((await screen.findByText('Investigation agent')).parentElement?.textContent).toContain('read only')
  expect(screen.getByText('Read-only client').parentElement?.textContent).toContain('read only')
  expect(screen.queryByText('investigation:write')).toBeNull()
  expect(screen.queryByText(/secret/)).toBeNull()
})

test('managed code-navigation indexing boundary is administrator-only', async () => {
  const { unmount } = renderPage()
  await screen.findByText('Read-only client')
  expect(screen.queryByRole('region', { name: 'Code navigation indexing' })).toBeNull()

  unmount()
  renderPage(true)
  expect(await screen.findByRole('region', { name: 'Code navigation indexing' })).toBeTruthy()
})

test('key creation is read only', async () => {
  renderPage()
  await screen.findByText('Read-only client')
  fireEvent.change(screen.getByRole('textbox', { name: 'Key name' }), {
    target: { value: 'Default client' },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Create key' }))
  await waitFor(() => {
    expect(api.createAPIKey).toHaveBeenCalledWith('Default client')
  })
  expect(await screen.findByText(/Read-only key/)).toBeTruthy()
  expect(screen.queryByRole('checkbox', { name: /Allow Investigation writes/ })).toBeNull()
})

async function createKey(name = 'Clipboard client') {
  await waitFor(() => expect((screen.getByRole('button', { name: 'Create key' }) as HTMLButtonElement).disabled).toBe(false))
  fireEvent.change(screen.getByRole('textbox', { name: 'Key name' }), { target: { value: name } })
  fireEvent.click(screen.getByRole('button', { name: 'Create key' }))
  expect((await screen.findByRole('textbox', { name: 'New API key token' }) as HTMLInputElement).value).toBe('phebs_new.secret')
}

test.each(['missing', 'rejected', 'throws'] as const)('key copy reports %s clipboard failure and permits recovery (T44.2b)', async (failure) => {
  const writeText = vi.fn()
  if (failure === 'rejected') writeText.mockRejectedValue(new Error('private clipboard details'))
  if (failure === 'throws') writeText.mockImplementation(() => { throw new Error('private clipboard details') })
  vi.stubGlobal('navigator', { clipboard: failure === 'missing' ? undefined : { writeText } })
  renderPage()
  await createKey()
  fireEvent.click(screen.getByRole('button', { name: 'Copy API key' }))
  expect(await screen.findByText('Copy failed. Select and copy the key above.')).toBeTruthy()
  expect(screen.queryByText('API key copied.')).toBeNull()
  expect(screen.queryByText('private clipboard details')).toBeNull()
  expect((screen.getByRole('textbox', { name: 'New API key token' }) as HTMLInputElement).readOnly).toBe(true)
  expect(api.createAPIKey).toHaveBeenCalledTimes(1)
  expect(api.fetchAPIKeys).toHaveBeenCalledTimes(1)
  writeText.mockResolvedValue(undefined)
  vi.stubGlobal('navigator', { clipboard: { writeText } })
  fireEvent.click(screen.getByRole('button', { name: 'Copy API key' }))
  expect(await screen.findByText('API key copied.')).toBeTruthy()
  expect(writeText).toHaveBeenLastCalledWith('phebs_new.secret')
})

test('copy success waits for the clipboard and an old key cannot mark a replacement copied (T44.2b)', async () => {
  let resolve!: () => void
  const writeText = vi.fn().mockReturnValue(new Promise<void>(done => { resolve = done }))
  vi.stubGlobal('navigator', { clipboard: { writeText } })
  renderPage()
  await createKey()
  fireEvent.click(screen.getByRole('button', { name: 'Copy API key' }))
  expect(screen.queryByText('API key copied.')).toBeNull()
  expect((screen.getByRole('button', { name: 'Copy API key' }) as HTMLButtonElement).disabled).toBe(true)
  expect(screen.getByText('Copying API key…').getAttribute('role')).toBe('status')
  api.createAPIKey.mockResolvedValue({ key: { id: 'replacement', name: 'Replacement', prefix: 'phebs_replacement', capabilities: [], created_at: '2026-10-18T00:00:00Z' }, token: 'phebs_replacement.secret' })
  fireEvent.change(screen.getByRole('textbox', { name: 'Key name' }), { target: { value: 'Replacement' } })
  fireEvent.click(screen.getByRole('button', { name: 'Create key' }))
  await waitFor(() => expect((screen.getByRole('textbox', { name: 'New API key token' }) as HTMLInputElement).value).toBe('phebs_replacement.secret'))
  await act(async () => { resolve() })
  expect(screen.queryByText('API key copied.')).toBeNull()
  expect(screen.queryByText('Copying API key…')).toBeNull()
  expect((screen.getByRole('button', { name: 'Copy API key' }) as HTMLButtonElement).disabled).toBe(false)
  writeText.mockResolvedValue(undefined)
  fireEvent.click(screen.getByRole('button', { name: 'Copy API key' }))
  expect(await screen.findByText('API key copied.')).toBeTruthy()
  expect(writeText).toHaveBeenLastCalledWith('phebs_replacement.secret')
})

test('administrator sees bounded lifecycle pressure without owner content', async () => {
  renderPage(true)
  expect(await screen.findByRole('heading', { name: 'Lifecycle maintenance' })).toBeTruthy()
  expect(await screen.findByText('Collecting')).toBeTruthy()
  expect(screen.getByText('81% allocated (810 B of 1000 B)')).toBeTruthy()
  expect(screen.getByText('4 / 13')).toBeTruthy()
  // T43.10 cards: the per-turn bounds and watermark ladder live on the
  // Owner progress and Pressure cards.
  expect(screen.getByText(/at most 64 candidates, 16 deletions, 16 store queries/)).toBeTruthy()
  expect(screen.getByText(/Accelerates at 80% · refuses new derived work at 90% · resumes below 75%/)).toBeTruthy()
  expect(screen.getByRole('region', { name: 'Pressure' })).toBeTruthy()
  expect(screen.getByRole('region', { name: 'Owner progress' })).toBeTruthy()
  expect(screen.getByRole('region', { name: 'Backlog' })).toBeTruthy()
  // Card semantics: only failed/backlog owners are named (source-free
  // operational identifiers from the delivered payload); healthy owners
  // stay counts-only.
  const backlogCard = screen.getByRole('region', { name: 'Backlog' })
  expect(backlogCard.textContent).toContain('owner-00')
  expect(screen.getByText('no failed owners')).toBeTruthy()
  expect(screen.queryByText(/owner-05/)).toBeNull()
  // Retained results are not cycle-scoped: StatusMonitor keeps each owner's
  // most recent result across rotations, and the payload carries no cycle
  // authority — the caption must not claim one.
  expect(screen.getByText('owners with recorded results')).toBeTruthy()
  expect(screen.queryByText(/observed this cycle/)).toBeNull()
  // Charter timestamp rule: relative and absolute rendered together as
  // visible text (title-only absolutes are unreachable by touch and AT).
  expect(screen.getByRole('region', { name: 'Pressure' }).textContent).toMatch(/observed .+ · .+/)
  expect(backlogCard.textContent).toMatch(/attempted .+ · .+/)
})

test('capacity-unavailable uses the closed blue status tone', async () => {
  const current = await api.fetchLifecycleStatus()
  api.fetchLifecycleStatus.mockResolvedValueOnce({
    ...current,
    capacity: {
      completeness: 'unavailable',
      pressure: 'unavailable',
      observed_at: '2026-08-05T20:00:00Z',
    },
  })
  renderPage(true)
  const matches = await screen.findAllByText('Capacity unavailable')
  const badge = matches.find((match) => match.getAttribute('role') === 'status')
  expect(badge).toBeTruthy()
  expect(badge?.getAttribute('data-tone')).toBe('blue')
})

test('key creation waits for the initial inventory instead of racing its snapshot', async () => {
  let resolve!: (value: { keys: [] }) => void
  api.fetchAPIKeys.mockImplementationOnce(() => new Promise<{ keys: [] }>(done => { resolve = done }))
  renderPage()
  fireEvent.change(screen.getByRole('textbox', { name: 'Key name' }), { target: { value: 'New client' } })
  expect((screen.getByRole('button', { name: 'Create key' }) as HTMLButtonElement).disabled).toBe(true)
  fireEvent.submit(screen.getByRole('button', { name: 'Create key' }).closest('form')!)
  expect(api.createAPIKey).not.toHaveBeenCalled()
  await act(async () => { resolve({ keys: [] }) })
  fireEvent.click(screen.getByRole('button', { name: 'Create key' }))
  expect(await screen.findByText('New client')).toBeTruthy()
})

test('a failed key inventory has an exact retry and does not enable creation', async () => {
  api.fetchAPIKeys.mockRejectedValueOnce(new Error('key listing offline'))
  renderPage()
  await screen.findByText(/key listing offline/)
  expect((screen.getByRole('button', { name: 'Create key' }) as HTMLButtonElement).disabled).toBe(true)
  fireEvent.click(screen.getByRole('button', { name: 'Retry API keys' }))
  await screen.findByText('Read-only client')
  expect(screen.queryByText(/key listing offline/)).toBeNull()
  expect(api.fetchAPIKeys).toHaveBeenCalledTimes(2)
  expect(api.createAPIKey).not.toHaveBeenCalled()
})

test.each(['new-key', 'read-key'])('revoking %s removes only its matching one-time secret', async id => {
  renderPage()
  await screen.findByText('Read-only client')
  await createKey('New client')
  fireEvent.click(screen.getByRole('button', { name: `Revoke ${id === 'new-key' ? 'New client' : 'Read-only client'}` }))
  fireEvent.click(screen.getByRole('button', { name: 'Revoke' }))
  await waitFor(() => expect(screen.queryByRole('button', { name: 'Revoke' })).toBeNull())
  if (id === 'new-key') expect(screen.queryByRole('textbox', { name: 'New API key token' })).toBeNull()
  else expect(screen.getByRole('textbox', { name: 'New API key token' })).toBeTruthy()
})

test('lifecycle failures retry only the administrator status read', async () => {
  api.fetchLifecycleStatus.mockRejectedValueOnce(new Error('lifecycle offline'))
  renderPage(true)
  await screen.findByText(/lifecycle offline/)
  fireEvent.click(screen.getByRole('button', { name: 'Retry lifecycle status' }))
  await screen.findByText('Collecting')
  expect(api.fetchLifecycleStatus).toHaveBeenCalledTimes(2)
  expect(api.fetchAPIKeys).toHaveBeenCalledTimes(1)
})

test('a completed key creation preserves the next name drafted during the request', async () => {
  let resolve!: (value: unknown) => void
  api.createAPIKey.mockImplementationOnce(() => new Promise(done => { resolve = done }))
  renderPage()
  await screen.findByText('Read-only client')
  fireEvent.change(screen.getByRole('textbox', { name: 'Key name' }), { target: { value: 'First client' } })
  fireEvent.click(screen.getByRole('button', { name: 'Create key' }))
  fireEvent.change(screen.getByRole('textbox', { name: 'Key name' }), { target: { value: 'Next client' } })
  await act(async () => resolve({ key: { id: 'new-key', name: 'First client', prefix: 'phebs_new', created_at: '2026-10-19T12:00:00Z' }, token: 'phebs_new.secret' }))
  expect((screen.getByRole('textbox', { name: 'Key name' }) as HTMLInputElement).value).toBe('Next client')
})
