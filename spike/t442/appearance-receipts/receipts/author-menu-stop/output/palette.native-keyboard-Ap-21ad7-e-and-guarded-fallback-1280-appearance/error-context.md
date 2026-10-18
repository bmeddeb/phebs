# Instructions

- Following Playwright test failed.
- Explain why, be concise, respect Playwright best practices.
- Provide a snippet of code with the fix, if possible.

# Test info

- Name: palette.native.spec.ts >> keyboard Appearance choice, persistence and guarded fallback 1280
- Location: ../../../../../../private/var/folders/wc/7grj940960386yt8vjsvv4dm0000gn/T/phebs-t442-appearance-835thdfq/runner/palette.native.spec.ts:119:3

# Error details

```
Error: expect(locator).toHaveValue(expected) failed

Locator:  getByRole('region', { name: 'Appearance', exact: true }).getByRole('combobox', { name: 'Code highlight palette', exact: true })
Expected: "quiet"
Received: "phebs"
Timeout:  5000ms

Call log:
  - Expect "toHaveValue" with timeout 5000ms
  - waiting for getByRole('region', { name: 'Appearance', exact: true }).getByRole('combobox', { name: 'Code highlight palette', exact: true })
    14 × locator resolved to <select class="css-dUycxS">…</select>
       - unexpected value "phebs"

```

```yaml
- combobox "Code highlight palette":
  - option "Phebs" [selected]
  - option "Quiet"
  - option "Classic"
  - option "High contrast"
```

# Test source

```ts
  1   | import { expect, test, type Page } from "/var/folders/wc/7grj940960386yt8vjsvv4dm0000gn/T/phebs-t442-appearance-835thdfq/runner/native-fixture.ts"
  2   | import { DEFAULT_PALETTE, PALETTES, PALETTE_NAMES, type PaletteName } from "/Users/ben/.codex/worktrees/t422n-mac-native-closure/phebs.com/ui/src/palette"
  3   | import { scrollBelowHeader } from "/Users/ben/.codex/worktrees/t422n-mac-native-closure/phebs.com/ui/receipts/fixture"
  4   | import { DENSITIES, FROZEN_NOW, THEMES } from "/Users/ben/.codex/worktrees/t422n-mac-native-closure/phebs.com/ui/receipts/routes"
  5   | 
  6   | const widths = [1280, 390] as const
  7   | const specimenText = '// verified span\nfunc Count(limit int) error {\n\treturn fmt.Errorf("bound=%d", 42)\n}'
  8   | 
  9   | // Presentation-only signed-in reader: no authentication backend, administrator
  10  | // plane, provider, installed generation or release/signing authority is exercised.
  11  | async function appearanceFixture(page: Page) {
  12  |   const requests: string[] = []
  13  |   const unexpected: string[] = []
  14  |   let pending = 0
  15  |   await page.route('**/api/**', async route => {
  16  |     const request = route.request()
  17  |     const url = new URL(request.url())
  18  |     const key = request.method() + ' ' + url.pathname + url.search
  19  |     requests.push(key)
  20  |     pending++
  21  |     let value: unknown
  22  |     if (key === 'GET /api/auth/status') {
  23  |       value = {
  24  |         authenticated: true, auth_required: true, setup_required: false,
  25  |         oidc_enabled: false, password_enabled: true, csrf_token: 'appearance-receipt-csrf',
  26  |         user: { id: 'appearance-reader', email: 'appearance@localhost.test', display_name: 'Receipt reader', is_admin: false },
  27  |       }
  28  |     } else if (key === 'GET /api/version') {
  29  |       value = { version: 'receipt', capabilities: [] }
  30  |     } else if (key === 'GET /api/auth/keys') {
  31  |       value = { keys: [] }
  32  |     } else {
  33  |       unexpected.push(key)
  34  |       value = { error: 'unexpected appearance fixture request' }
  35  |     }
  36  |     try {
  37  |       await route.fulfill({ status: unexpected.includes(key) ? 501 : 200, contentType: 'application/json', body: JSON.stringify(value) })
  38  |     } finally {
  39  |       pending--
  40  |     }
  41  |   })
  42  |   return { requests, unexpected, pending: () => pending }
  43  | }
  44  | 
  45  | function rgb(hex: string): string {
  46  |   return 'rgb(' + [1, 3, 5].map(offset => Number.parseInt(hex.slice(offset, offset + 2), 16)).join(', ') + ')'
  47  | }
  48  | 
  49  | async function prepare(page: Page, width: typeof widths[number], theme: typeof THEMES[number], density: typeof DENSITIES[number]) {
  50  |   const errors: string[] = []
  51  |   page.on('pageerror', error => errors.push(error.message))
  52  |   page.on('console', message => {
  53  |     if (message.type() === 'error' || message.type() === 'warning') errors.push(message.text())
  54  |   })
  55  |   const f = await appearanceFixture(page)
  56  |   await page.setViewportSize({ width, height: width === 390 ? 900 : 800 })
  57  |   await page.emulateMedia({ colorScheme: theme, reducedMotion: 'reduce' })
  58  |   await page.clock.setFixedTime(FROZEN_NOW)
  59  |   // Seed once per test tab, so an actual reload tests the user's persisted
  60  |   // choice instead of an init script silently overwriting it on every load.
  61  |   await page.addInitScript(({ theme, density, palette }) => {
  62  |     if (sessionStorage.getItem('appearance-receipt-seeded') === 'true') return
  63  |     localStorage.setItem('phebs-theme', theme)
  64  |     localStorage.setItem('phebs-density', density)
  65  |     localStorage.setItem('phebs-palette', palette)
  66  |     sessionStorage.setItem('appearance-receipt-seeded', 'true')
  67  |   }, { theme, density, palette: DEFAULT_PALETTE })
  68  |   const navigationStart = Date.now()
  69  |   await page.goto('/#/settings')
  70  |   const appearance = page.getByRole('region', { name: 'Appearance', exact: true })
  71  |   const select = appearance.getByRole('combobox', { name: 'Code highlight palette', exact: true })
  72  |   const specimen = appearance.locator('pre')
  73  |   const keyword = specimen.locator('span').filter({ hasText: /^func$/ })
  74  |   async function ready(palette: PaletteName) {
  75  |     await expect(page).toHaveTitle('phebs · Settings')
  76  |     await expect(appearance.getByRole('heading', { name: 'Appearance', exact: true })).toBeVisible()
> 77  |     await expect(select).toHaveValue(palette)
      |                          ^ Error: expect(locator).toHaveValue(expected) failed
  78  |     await expect(specimen).toHaveAttribute('data-palette-ready', 'true')
  79  |     await expect(specimen).toHaveJSProperty('textContent', specimenText)
  80  |     await expect(keyword).toHaveCSS('color', rgb(PALETTES[palette][theme].keyword.color))
  81  |     await expect(page.getByText('No API keys.', { exact: true })).toBeVisible()
  82  |     await expect.poll(f.pending).toBe(0)
  83  |     await expect(page.getByRole('region', { name: 'Code navigation indexing' })).toHaveCount(0)
  84  |     await expect(page.locator('vite-error-overlay')).toHaveCount(0)
  85  |   }
  86  |   await ready(DEFAULT_PALETTE)
  87  |   const readyMillis = Date.now() - navigationStart
  88  |   await page.evaluate(() => document.fonts.ready)
  89  |   const clearance = await scrollBelowHeader(appearance)
  90  |   // Fixed viewport, whole region and whole specimen. Never enlarge a frame or
  91  |   // accept an element-only crop that hides the heading/control or clipping.
  92  |   await expect.poll(() => appearance.evaluate((node, top) => {
  93  |     const bounds = node.getBoundingClientRect()
  94  |     const pre = node.querySelector('pre')!
  95  |     const code = pre.getBoundingClientRect()
  96  |     return bounds.top >= top && bounds.bottom <= innerHeight - 12 && bounds.left >= 0 && bounds.right <= innerWidth &&
  97  |       code.top >= bounds.top && code.bottom <= bounds.bottom && pre.scrollWidth <= pre.clientWidth
  98  |   }, clearance)).toBe(true)
  99  |   await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  100 |   await expect(select.locator('option')).toHaveText(PALETTE_NAMES.map(name => PALETTES[name].label))
  101 |   expect(f.unexpected).toEqual([])
  102 |   expect(errors).toEqual([])
  103 |   return { ...f, appearance, select, ready, errors, readyMillis }
  104 | }
  105 | 
  106 | for (const theme of THEMES) for (const width of widths) for (const density of DENSITIES) {
  107 |   test(`native Appearance frame ${theme} ${width} ${density}`, async ({ page }, testInfo) => {
  108 |     const f = await prepare(page, width, theme, density)
  109 |     const capture = testInfo.outputPath(`palette-appearance--${theme}--${width}--${density}.png`)
  110 |     await page.screenshot({ path: capture, fullPage: false, animations: 'disabled', caret: 'hide' })
  111 |     await testInfo.attach('native-appearance-frame', { path: capture, contentType: 'image/png' })
  112 |     expect(f.unexpected).toEqual([])
  113 |     expect(f.errors).toEqual([])
  114 |     console.log(JSON.stringify({ theme, width, height: width === 390 ? 900 : 800, density, palette: DEFAULT_PALETTE, fixtureReadyMillis: f.readyMillis, nativeCapture: capture }))
  115 |   })
  116 | }
  117 | 
  118 | for (const width of widths) {
  119 |   test(`keyboard Appearance choice, persistence and guarded fallback ${width}`, async ({ page }) => {
  120 |     const f = await prepare(page, width, 'light', 'comfortable')
  121 |     const url = page.url()
  122 |     let navigations = 0
  123 |     page.on('framenavigated', frame => { if (frame === page.mainFrame()) navigations++ })
  124 |     for (let tab = 0; tab < 24; tab++) {
  125 |       await page.keyboard.press('Tab')
  126 |       if (await f.select.evaluate(node => node === document.activeElement)) break
  127 |     }
  128 |     await expect(f.select).toBeFocused()
  129 |     expect(await f.select.evaluate(node => node.matches(':focus-visible'))).toBe(true)
  130 |     const requests = [...f.requests]
  131 |     const interactionMillis: number[] = []
  132 |     for (let index = 0; index < PALETTE_NAMES.length; index++) {
  133 |       const start = Date.now()
  134 |       // Open the native menu before moving its highlight; Enter commits it.
  135 |       await page.keyboard.press('Space')
  136 |       await page.keyboard.press(index === 0 ? 'Home' : 'ArrowDown')
  137 |       await page.keyboard.press('Enter')
  138 |       await f.ready(PALETTE_NAMES[index])
  139 |       // Stop timing at the live identity's token/color ready state. Fonts,
  140 |       // captures, reloads and accessibility instrumentation are not in this cost.
  141 |       interactionMillis.push(Date.now() - start)
  142 |       await expect.poll(() => page.evaluate(() => localStorage.getItem('phebs-palette'))).toBe(PALETTE_NAMES[index])
  143 |       await expect(page).toHaveURL(url)
  144 |       expect(f.requests).toEqual(requests)
  145 |       expect(navigations).toBe(0)
  146 |     }
  147 |     await page.reload()
  148 |     await f.ready(PALETTE_NAMES.at(-1)!)
  149 |     await expect(page).toHaveURL(url)
  150 |     for (const invalid of ['unknown-neutral-palette', '__proto__']) {
  151 |       await page.evaluate(value => localStorage.setItem('phebs-palette', value), invalid)
  152 |       await page.reload()
  153 |       await f.ready(DEFAULT_PALETTE)
  154 |       await expect.poll(() => page.evaluate(() => localStorage.getItem('phebs-palette'))).toBe(DEFAULT_PALETTE)
  155 |       await expect(page).toHaveURL(url)
  156 |     }
  157 |     expect(f.unexpected).toEqual([])
  158 |     expect(f.errors).toEqual([])
  159 |     console.log(JSON.stringify({ width, paletteChanges: PALETTE_NAMES, fixtureInteractionMillis: interactionMillis, persistenceReload: true, refusedStoredValues: ['unknown-neutral-palette', '__proto__'] }))
  160 |   })
  161 | }
  162 | 
```