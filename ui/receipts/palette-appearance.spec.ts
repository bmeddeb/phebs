import { expect, test, type Page } from '@playwright/test'
import { DEFAULT_PALETTE, PALETTES, PALETTE_NAMES, type PaletteName } from '../src/palette'
import { scrollBelowHeader } from './fixture'
import { DENSITIES, FROZEN_NOW, THEMES } from './routes'

const widths = [1280, 390] as const
const specimenText = '// verified span\nfunc Count(limit int) error {\n\treturn fmt.Errorf("bound=%d", 42)\n}'
const syntheticToken = 'phebs_receipt.synthetic-key-for-presentation-only'

// Presentation-only signed-in reader: no authentication backend, administrator
// plane, provider, installed generation or release/signing authority is exercised.
async function appearanceFixture(page: Page, createKey = false) {
  const requests: string[] = []
  const unexpected: string[] = []
  let pending = 0
  await page.route('**/api/**', async route => {
    const request = route.request()
    const url = new URL(request.url())
    const key = request.method() + ' ' + url.pathname + url.search
    requests.push(key)
    pending++
    let value: unknown
    if (key === 'GET /api/auth/status') {
      value = {
        authenticated: true, auth_required: true, setup_required: false,
        oidc_enabled: false, password_enabled: true, csrf_token: 'appearance-receipt-csrf',
        user: { id: 'appearance-reader', email: 'appearance@localhost.test', display_name: 'Receipt reader', is_admin: false },
      }
    } else if (key === 'GET /api/version') {
      value = { version: 'receipt', capabilities: [] }
    } else if (key === 'GET /api/auth/keys') {
      value = { keys: [] }
    } else if (createKey && key === 'POST /api/auth/keys' && request.postDataJSON().name === 'Receipt client') {
      value = { key: { id: 'receipt-key', name: 'Receipt client', prefix: 'phebs_receipt', capabilities: [], created_at: FROZEN_NOW }, token: syntheticToken }
    } else {
      unexpected.push(key)
      value = { error: 'unexpected appearance fixture request' }
    }
    try {
      await route.fulfill({ status: unexpected.includes(key) ? 501 : 200, contentType: 'application/json', body: JSON.stringify(value) })
    } finally {
      pending--
    }
  })
  return { requests, unexpected, pending: () => pending }
}

function rgb(hex: string): string {
  return 'rgb(' + [1, 3, 5].map(offset => Number.parseInt(hex.slice(offset, offset + 2), 16)).join(', ') + ')'
}

async function prepare(page: Page, width: typeof widths[number], theme: typeof THEMES[number], density: typeof DENSITIES[number], createKey = false) {
  const errors: string[] = []
  page.on('pageerror', error => errors.push(error.message))
  page.on('console', message => {
    if (message.type() === 'error' || message.type() === 'warning') errors.push(message.text())
  })
  const f = await appearanceFixture(page, createKey)
  await page.setViewportSize({ width, height: width === 390 ? 900 : 800 })
  await page.emulateMedia({ colorScheme: theme, reducedMotion: 'reduce' })
  await page.clock.setFixedTime(FROZEN_NOW)
  // Seed once per test tab, so an actual reload tests the user's persisted
  // choice instead of an init script silently overwriting it on every load.
  await page.addInitScript(({ theme, density, palette }) => {
    if (sessionStorage.getItem('appearance-receipt-seeded') === 'true') return
    localStorage.setItem('phebs-theme', theme)
    localStorage.setItem('phebs-density', density)
    localStorage.setItem('phebs-palette', palette)
    sessionStorage.setItem('appearance-receipt-seeded', 'true')
  }, { theme, density, palette: DEFAULT_PALETTE })
  const navigationStart = Date.now()
  await page.goto('/#/settings')
  const appearance = page.getByRole('region', { name: 'Appearance', exact: true })
  const select = appearance.getByRole('combobox', { name: 'Code highlight palette', exact: true })
  const specimen = appearance.locator('pre')
  const keyword = specimen.locator('span').filter({ hasText: /^func$/ })
  async function ready(palette: PaletteName) {
    await expect(page).toHaveTitle('phebs · Settings')
    await expect(appearance.getByRole('heading', { name: 'Appearance', exact: true })).toBeVisible()
    await expect(select).toHaveValue(palette)
    await expect(specimen).toHaveAttribute('data-palette-ready', 'true')
    await expect(specimen).toHaveJSProperty('textContent', specimenText)
    await expect(keyword).toHaveCSS('color', rgb(PALETTES[palette][theme].keyword.color))
    await expect(page.getByText('No API keys.', { exact: true })).toBeVisible()
    await expect.poll(f.pending).toBe(0)
    await expect(page.getByRole('region', { name: 'Code navigation indexing' })).toHaveCount(0)
    await expect(page.locator('vite-error-overlay')).toHaveCount(0)
    await expect(appearance.getByRole('status')).toHaveCount(0)
    await expect(appearance.getByRole('button', { name: 'Reload page' })).toHaveCount(0)
  }
  await ready(DEFAULT_PALETTE)
  const readyMillis = Date.now() - navigationStart
  await page.evaluate(() => document.fonts.ready)
  const clearance = await scrollBelowHeader(appearance)
  // Fixed viewport, whole region and whole specimen. Never enlarge a frame or
  // accept an element-only crop that hides the heading/control or clipping.
  await expect.poll(() => appearance.evaluate((node, top) => {
    const bounds = node.getBoundingClientRect()
    const pre = node.querySelector('pre')!
    const code = pre.getBoundingClientRect()
    return bounds.top >= top && bounds.bottom <= innerHeight - 12 && bounds.left >= 0 && bounds.right <= innerWidth &&
      code.top >= bounds.top && code.bottom <= bounds.bottom && pre.scrollWidth <= pre.clientWidth
  }, clearance)).toBe(true)
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  await expect(select.locator('option')).toHaveText(PALETTE_NAMES.map(name => PALETTES[name].label))
  expect(f.unexpected).toEqual([])
  expect(errors).toEqual([])
  return { ...f, appearance, select, ready, errors, readyMillis }
}

for (const theme of THEMES) for (const width of widths) for (const density of DENSITIES) {
  test(`native Appearance frame ${theme} ${width} ${density}`, async ({ page }, testInfo) => {
    const f = await prepare(page, width, theme, density)
    const capture = testInfo.outputPath(`palette-appearance--${theme}--${width}--${density}.png`)
    await page.screenshot({ path: capture, fullPage: false, animations: 'disabled', caret: 'hide' })
    await testInfo.attach('native-appearance-frame', { path: capture, contentType: 'image/png' })
    expect(f.unexpected).toEqual([])
    expect(f.errors).toEqual([])
    console.log(JSON.stringify({ theme, width, height: width === 390 ? 900 : 800, density, palette: DEFAULT_PALETTE, fixtureReadyMillis: f.readyMillis, nativeCapture: capture }))
  })
}

for (const width of widths) {
  test(`keyboard Appearance choice, persistence and guarded fallback ${width}`, async ({ page }) => {
    const f = await prepare(page, width, 'light', 'comfortable')
    const url = page.url()
    let navigations = 0
    page.on('framenavigated', frame => { if (frame === page.mainFrame()) navigations++ })
    for (let tab = 0; tab < 24; tab++) {
      await page.keyboard.press('Tab')
      if (await f.select.evaluate(node => node === document.activeElement)) break
    }
    await expect(f.select).toBeFocused()
    expect(await f.select.evaluate(node => node.matches(':focus-visible'))).toBe(true)
    const requests = [...f.requests]
    const interactionMillis: number[] = []
    for (let index = 0; index < PALETTE_NAMES.length; index++) {
      const start = Date.now()
      // Refocus resets the native type-ahead prefix between palette choices.
      await page.keyboard.press('Tab')
      await page.keyboard.press('Shift+Tab')
      await expect(f.select).toBeFocused()
      await page.keyboard.press(PALETTES[PALETTE_NAMES[index]].label[0].toLowerCase())
      await f.ready(PALETTE_NAMES[index])
      // Stop timing at the live identity's token/color ready state. Fonts,
      // captures, reloads and accessibility instrumentation are not in this cost.
      interactionMillis.push(Date.now() - start)
      await expect.poll(() => page.evaluate(() => localStorage.getItem('phebs-palette'))).toBe(PALETTE_NAMES[index])
      await expect(page).toHaveURL(url)
      expect(f.requests).toEqual(requests)
      expect(navigations).toBe(0)
    }
    await page.reload()
    await f.ready(PALETTE_NAMES.at(-1)!)
    await expect(page).toHaveURL(url)
    for (const invalid of ['unknown-neutral-palette', '__proto__']) {
      await page.evaluate(value => localStorage.setItem('phebs-palette', value), invalid)
      await page.reload()
      await f.ready(DEFAULT_PALETTE)
      await expect.poll(() => page.evaluate(() => localStorage.getItem('phebs-palette'))).toBe(DEFAULT_PALETTE)
      await expect(page).toHaveURL(url)
    }
    expect(f.unexpected).toEqual([])
    expect(f.errors).toEqual([])
    console.log(JSON.stringify({ width, paletteChanges: PALETTE_NAMES, fixtureInteractionMillis: interactionMillis, persistenceReload: true, refusedStoredValues: ['unknown-neutral-palette', '__proto__'] }))
  })
}

for (const theme of THEMES) for (const width of widths) {
  test(`native Appearance unavailable recovery ${theme} ${width}`, async ({ page }, testInfo) => {
    const f = await prepare(page, width, theme, 'comfortable')
    const url = page.url()
    const failedScripts: string[] = []
    const messages: { type: string; text: string; url: string }[] = []
    page.on('console', message => {
      if (message.type() === 'error' || message.type() === 'warning') {
        messages.push({ type: message.type(), text: message.text(), url: message.location().url })
      }
    })
    const pattern = '**/assets/highlight-*.js'
    await page.route(pattern, async route => {
      failedScripts.push(route.request().url())
      await route.fulfill({ status: 503, contentType: 'application/javascript', body: '/* deliberate receipt-only module failure */' })
    })
    await page.reload()
    const status = f.appearance.getByRole('status')
    const reload = f.appearance.getByRole('button', { name: 'Reload page', exact: true })
    await expect(status).toHaveText('Preview unavailable.')
    await expect(f.appearance.getByText('Finish Settings changes and copy any new API key before reloading.')).toBeVisible()
    await expect(f.appearance.locator('pre')).toHaveJSProperty('textContent', specimenText)
    await expect(f.appearance.locator('pre')).not.toHaveAttribute('data-palette-ready', 'true')
    await expect(page.getByText('No API keys.', { exact: true })).toBeVisible()
    await expect.poll(f.pending).toBe(0)
    await expect(page).toHaveTitle('phebs · Settings')
    await expect(page).toHaveURL(url)
    const requests = [...f.requests]
    for (let tab = 0; tab < 24; tab++) {
      await page.keyboard.press('Tab')
      if (await f.select.evaluate(node => node === document.activeElement)) break
    }
    await expect(f.select).toBeFocused()
    await page.keyboard.press('q')
    await expect(f.select).toHaveValue('quiet')
    await expect.poll(() => page.evaluate(() => localStorage.getItem('phebs-palette'))).toBe('quiet')
    await expect(status).toBeVisible()
    expect(f.requests).toEqual(requests)
    await page.evaluate(() => document.fonts.ready)
    const clearance = await scrollBelowHeader(f.appearance)
    expect(await f.appearance.evaluate((node, top) => {
      const bounds = node.getBoundingClientRect()
      return bounds.top >= top && bounds.bottom <= innerHeight - 12 && bounds.left >= 0 && bounds.right <= innerWidth
    }, clearance)).toBe(true)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    expect((await reload.boundingBox())!.height).toBeGreaterThanOrEqual(44)
    const capture = testInfo.outputPath(`palette-preview-unavailable--${theme}--${width}.png`)
    await page.screenshot({ path: capture, fullPage: false, animations: 'disabled', caret: 'hide' })
    await testInfo.attach('unavailable-preview-frame', { path: capture, contentType: 'image/png' })
    await page.unroute(pattern)
    await page.keyboard.press('Tab')
    await expect(reload).toBeFocused()
    expect(await reload.evaluate(node => node.matches(':focus-visible'))).toBe(true)
    await Promise.all([page.waitForEvent('domcontentloaded'), page.keyboard.press('Enter')])
    await f.ready('quiet')
    await expect(status).toHaveCount(0)
    await expect(reload).toHaveCount(0)
    await expect(page).toHaveURL(url)
    expect(f.unexpected).toEqual([])
    expect(failedScripts.length).toBeGreaterThan(0)
    expect(failedScripts.length).toBeLessThanOrEqual(2)
    expect(messages.length).toBe(failedScripts.length)
    for (const message of messages) {
      expect(message.type).toBe('error')
      expect(message.text).toMatch(/^Failed to load resource:.*503/)
      expect(failedScripts).toContain(message.url)
    }
    expect(f.errors).toEqual(messages.map(message => message.text))
    console.log(JSON.stringify({ width, theme, failedScripts, expectedFaultConsole: messages, reloadPreservedPalette: 'quiet', recovery: true }))
  })
}

for (const theme of THEMES) for (const width of widths) {
  test(`native API key copying ${theme} ${width}`, async ({ page }, testInfo) => {
    const f = await prepare(page, width, theme, 'comfortable', true)
    const url = page.url()
    await page.getByRole('textbox', { name: 'Key name', exact: true }).fill('Receipt client')
    await page.keyboard.press('Tab')
    await expect(page.getByRole('button', { name: 'Create key', exact: true })).toBeFocused()
    await page.keyboard.press('Enter')
    const card = page.getByRole('group', { name: 'New API key', exact: true })
    const token = card.getByRole('textbox', { name: 'New API key token', exact: true })
    const copy = card.getByRole('button', { name: 'Copy API key', exact: true })
    async function focusCopy() {
      for (let tab = 0; tab < 24; tab++) {
        if (await copy.evaluate(node => node === document.activeElement)) break
        await page.keyboard.press('Tab')
      }
      await expect(copy).toBeFocused()
    }
    await expect(token).toHaveValue(syntheticToken)
    await expect(token).toHaveAttribute('readonly', '')
    await page.evaluate(() => Object.defineProperty(navigator, 'clipboard', { configurable: true, value: undefined }))
    await focusCopy()
    expect(await copy.evaluate(node => node.matches(':focus-visible'))).toBe(true)
    await page.keyboard.press('Enter')
    await expect(card.getByRole('status')).toHaveText('Copy failed. Select and copy the key above.')
    await expect(copy).toBeEnabled()
    await focusCopy()
    await page.keyboard.press('Shift+Tab')
    await expect(token).toBeFocused()
    await page.keyboard.press('ControlOrMeta+a')
    expect(await token.evaluate(node => [(node as HTMLInputElement).selectionStart, (node as HTMLInputElement).selectionEnd])).toEqual([0, syntheticToken.length])
    const privateFailure = 'private clipboard diagnostic must not render'
    await page.evaluate(message => Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: () => Promise.reject(new Error(message)) } }), privateFailure)
    await page.keyboard.press('Tab')
    await page.keyboard.press('Enter')
    await expect(card.getByRole('status')).toHaveText('Copy failed. Select and copy the key above.')
    await expect(page.getByText(privateFailure)).toHaveCount(0)
    await page.evaluate(() => document.fonts.ready)
    const clearance = await scrollBelowHeader(card)
    expect(await card.evaluate((node, top) => { const b = node.getBoundingClientRect(); return b.top >= top && b.bottom <= innerHeight - 12 && b.left >= 0 && b.right <= innerWidth }, clearance)).toBe(true)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    expect((await copy.boundingBox())!.height).toBeGreaterThanOrEqual(44)
    const failed = testInfo.outputPath(`api-key-copy-failed--${theme}--${width}.png`)
    await page.screenshot({ path: failed, fullPage: false, animations: 'disabled', caret: 'hide' })
    await testInfo.attach('api-key-copy-failed', { path: failed, contentType: 'image/png' })
    await page.evaluate(() => {
      const state = window as unknown as { clipboardResolve: () => void; clipboardText: string }
      Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: (text: string) => { state.clipboardText = text; return new Promise<void>(resolve => { state.clipboardResolve = resolve }) } } })
    })
    const requests = [...f.requests]
    const started = Date.now()
    await focusCopy()
    await page.keyboard.press('Enter')
    await expect(copy).toBeDisabled()
    await expect(card.getByRole('status')).toHaveText('Copying API key…')
    expect(await page.evaluate(() => (window as unknown as { clipboardText: string }).clipboardText)).toBe(syntheticToken)
    await page.evaluate(() => (window as unknown as { clipboardResolve: () => void }).clipboardResolve())
    await expect(card.getByRole('status')).toHaveText('API key copied.')
    await expect(copy).toBeEnabled()
    const settledMillis = Date.now() - started
    await expect(token).toHaveValue(syntheticToken)
    await expect(page).toHaveURL(url)
    expect(f.requests).toEqual(requests)
    expect(f.requests.filter(request => request === 'POST /api/auth/keys')).toHaveLength(1)
    expect(f.unexpected).toEqual([])
    expect(f.errors).toEqual([])
    const copied = testInfo.outputPath(`api-key-copy-done--${theme}--${width}.png`)
    await page.screenshot({ path: copied, fullPage: false, animations: 'disabled', caret: 'hide' })
    await testInfo.attach('api-key-copy-done', { path: copied, contentType: 'image/png' })
    console.log(JSON.stringify({ width, theme, missingClipboard: true, rejectedClipboard: true, keyboardManualSelection: true, deferredClipboardSuccess: true, settledMillis }))
  })
}
