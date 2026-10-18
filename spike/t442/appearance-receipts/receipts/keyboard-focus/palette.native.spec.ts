import { expect, test, type Page } from "/var/folders/wc/7grj940960386yt8vjsvv4dm0000gn/T/phebs-t442-appearance-835thdfq/runner/native-fixture.ts"
import { DEFAULT_PALETTE, PALETTES, PALETTE_NAMES, type PaletteName } from "/Users/ben/.codex/worktrees/t422n-mac-native-closure/phebs.com/ui/src/palette"
import { scrollBelowHeader } from "/Users/ben/.codex/worktrees/t422n-mac-native-closure/phebs.com/ui/receipts/fixture"
import { DENSITIES, FROZEN_NOW, THEMES } from "/Users/ben/.codex/worktrees/t422n-mac-native-closure/phebs.com/ui/receipts/routes"

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

test('native keyboard diagnostic', async ({page}) => {
  const f = await prepare(page, 1280, 'light', 'comfortable')
  for (let tab=0;tab<24;tab++) { await page.keyboard.press('Tab'); if(await f.select.evaluate(n=>n===document.activeElement))break }
  const samples:any[]=[]
  const record=async(key:string)=>samples.push({key,...await f.select.evaluate(n=>({focused:n===document.activeElement,value:(n as HTMLSelectElement).value,index:(n as HTMLSelectElement).selectedIndex,expanded:n.matches(':open')}))})
  await record('initial')
  for(const key of ['p','q','c','h']) {
    await page.keyboard.press('Tab')
    await page.keyboard.press('Shift+Tab')
    await expect(f.select).toBeFocused()
    await page.keyboard.press(key)
    await expect(f.select).toHaveValue(PALETTE_NAMES[['p','q','c','h'].indexOf(key)])
    await record(key)
  }
  console.log(JSON.stringify({samples}))
})
