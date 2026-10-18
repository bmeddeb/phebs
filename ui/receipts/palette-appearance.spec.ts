import { expect, test, type Page } from '@playwright/test'
import { DEFAULT_PALETTE, PALETTES, PALETTE_NAMES, type PaletteName } from '../src/palette'
import { scrollBelowHeader } from './fixture'
import { DENSITIES, FROZEN_NOW, THEMES } from './routes'

const widths = [1280, 390] as const
const specimenText = '// verified span\nfunc Count(limit int) error {\n\treturn fmt.Errorf("bound=%d", 42)\n}'

// Presentation-only signed-in reader: no authentication backend, administrator
// plane, provider, installed generation or release/signing authority is exercised.
async function appearanceFixture(page: Page) {
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

async function prepare(page: Page, width: typeof widths[number], theme: typeof THEMES[number], density: typeof DENSITIES[number]) {
  const errors: string[] = []
  page.on('pageerror', error => errors.push(error.message))
  page.on('console', message => {
    if (message.type() === 'error' || message.type() === 'warning') errors.push(message.text())
  })
  const f = await appearanceFixture(page)
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
      // Open the native menu before moving its highlight; Enter commits it.
      await page.keyboard.press('Space')
      await page.keyboard.press(index === 0 ? 'Home' : 'ArrowDown')
      await page.keyboard.press('Enter')
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
