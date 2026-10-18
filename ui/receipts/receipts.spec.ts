import { expect, test, type Page } from '@playwright/test'
import { DENSITIES, FROZEN_NOW, REPO_SEARCH_GENERATION, ROUTES, THEMES, type ReceiptRoute } from './routes'
import { DEFAULT_PALETTE } from '../src/palette'
import { installIdentityFixture, installReceiptFixture, scrollBelowHeader } from './fixture'

type ReceiptWindow = typeof window & { __phebsReceiptPending?: number }

async function waitForReceiptReady(page: Page) {
  const applicationMain = page.locator('main').first()
  // Let lazy-route effects begin before observing the request counter.
  await page.evaluate(() => new Promise<void>((resolve) => {
    requestAnimationFrame(() => requestAnimationFrame(() => resolve()))
  }))
  await expect.poll(
    () => page.evaluate(() => (window as ReceiptWindow).__phebsReceiptPending ?? 0),
    { message: 'receipt route still has an active API request', timeout: 30_000 },
  ).toBe(0)

  await expect.poll(async () => {
    const states = await applicationMain.locator('[role="status"]').evaluateAll((nodes) =>
      nodes.map((node) => `${node.getAttribute('aria-label') ?? ''} ${node.textContent ?? ''}`.trim()),
    )
    return states.some((state) => /\b(loading|reading|checking|searching|rendering)\b/i.test(state))
  }, { message: 'receipt route still renders a loading state', timeout: 30_000 }).toBe(false)

  // Keep full markup comparisons in the renderer; only the settled boolean
  // crosses the browser protocol, rather than every intermediate HTML tree.
  await page.waitForFunction((state) => {
    const main = document.querySelector('main')
    const next = main?.innerHTML ?? ''
    state.samples = main && next === state.prior ? state.samples + 1 : 0
    state.prior = next
    return state.samples >= 3
  }, { prior: '', samples: 0 }, { timeout: 30_000, polling: 250 })
}

async function capture(page: Page, route: ReceiptRoute, theme: (typeof THEMES)[number], density: (typeof DENSITIES)[number], name: string) {
  const { path } = route
  if (route.viewport) await page.setViewportSize(route.viewport)
  // Pin the neutral presentation identity on every captured page: the header
  // renders the operator email on desktop viewports, and without this the
  // bootstrap operator's address would reach the pixels.
  await installIdentityFixture(page)
  await installReceiptFixture(page, route.fixture)
  // Fixed Date only — timers, rAF, and CodeMirror layout stay live; age copy
  // stops drifting between capture and check runs.
  await page.clock.setFixedTime(FROZEN_NOW)
  await page.addInitScript(() => {
    const receiptWindow = window as ReceiptWindow
    receiptWindow.__phebsReceiptPending = 0
    const originalFetch = window.fetch.bind(window)
    window.fetch = (...args: Parameters<typeof window.fetch>) => {
      receiptWindow.__phebsReceiptPending = (receiptWindow.__phebsReceiptPending ?? 0) + 1
      return originalFetch(...args).finally(() => {
        receiptWindow.__phebsReceiptPending = Math.max(0, (receiptWindow.__phebsReceiptPending ?? 1) - 1)
      })
    }
  })
  await page.addInitScript((mode) => localStorage.setItem('phebs-theme', mode as string), theme)
  await page.addInitScript((value) => localStorage.setItem('phebs-density', value as string), density)
  // Receipts pin the product default, never a reusable session's personal
  // palette preference (T44.2).
  await page.addInitScript((value) => localStorage.setItem('phebs-palette', value as string), DEFAULT_PALETTE)
  await page.emulateMedia({ colorScheme: theme })
  await page.goto(`/#${path}`)
  await expect(page.locator('main').first()).toBeVisible({ timeout: 30_000 })
  await page.evaluate(() => document.fonts.ready)
  await waitForReceiptReady(page)
  if (route.awaitVisible) {
    for (const selector of route.awaitVisible) {
      await expect(page.locator(selector).first(), `required ready state missing: ${selector}`).toBeVisible({ timeout: 30_000 })
    }
  }
  if (route.mask?.includes(REPO_SEARCH_GENERATION)) {
    await expect(page.locator(REPO_SEARCH_GENERATION)).toHaveText(/^sha256:[0-9a-f]{64}$/)
  }
  if (route.awaitAbsent) {
    for (const text of route.awaitAbsent) {
      await expect.poll(
        () => page.locator('main').innerText().then((body) => body.includes(text)),
        { message: `route still renders a state excluded from baselines: "${text}"`, timeout: 180_000, intervals: [3_000] },
      ).toBe(false)
    }
  }
  if (route.press) {
    for (const chord of route.press) {
      await page.keyboard.press(chord)
    }
    await waitForReceiptReady(page)
  }
  if (route.click) {
    for (const selector of route.click) {
      const target = page.locator(selector).first()
      await expect(target, `required interaction target missing: ${selector}`).toBeVisible()
      await target.click()
    }
    await waitForReceiptReady(page)
  }
  if (route.scrollIntoView) {
    const target = page.locator(route.scrollIntoView).first()
    await expect(target, `required scroll target missing: ${route.scrollIntoView}`).toBeVisible()
    const clearance = await scrollBelowHeader(target)
    await expect.poll(() => target.evaluate(node => node.getBoundingClientRect().top)).toBeGreaterThanOrEqual(clearance)
  }
  if (route.expand) {
    await page.$$eval('main details', (nodes) => nodes.forEach((node) => { (node as HTMLDetailsElement).open = true }))
  }
  await expect(page).toHaveScreenshot(`${name}.png`, {
    mask: route.mask?.map((selector) => page.locator(selector)),
  })
}

for (const theme of THEMES) {
  for (const density of DENSITIES) {
    for (const route of ROUTES) {
      test(`${route.name} · ${theme} · ${density}`, async ({ page }) => {
        await capture(page, route, theme, density, `${route.name}--${theme}--${density}`)
      })
    }
  }
}

// Keep a real lazy chunk outstanding beyond Playwright's ordinary 5s assertion
// budget. DOM quiet alone must not turn an uncolored specimen into a receipt.
test('settings waits for its delayed palette chunk', async ({ page }) => {
  let delayed = false
  await page.route('**/assets/highlight-*.js', async (request) => {
    delayed = true
    await new Promise((resolve) => setTimeout(resolve, 8_000))
    await request.continue()
  })
  const route = ROUTES.find((candidate) => candidate.name === 'settings')!
  await capture(page, route, 'light', 'comfortable', 'settings--light--comfortable')
  expect(delayed, 'the production lazy highlight chunk was exercised').toBe(true)
})

test('waits for initial identity before requiring the application main', async ({ page }) => {
  await page.addInitScript(() => {
    const fetch = window.fetch.bind(window)
    window.fetch = async (...args: Parameters<typeof window.fetch>) => {
      if (args[0] === '/api/auth/status') await new Promise((resolve) => setTimeout(resolve, 8_000))
      return fetch(...args)
    }
  })
  const route = ROUTES.find((candidate) => candidate.name === 'audit')!
  await capture(page, route, 'dark', 'dense', 'audit--dark--dense')
})

test('DOM readiness waits for changing markup to settle', async ({ page }) => {
  await page.setContent('<main><span>starting</span></main>')
  await page.evaluate(() => {
    const span = document.querySelector('span')!
    let changes = 0
    const timer = setInterval(() => { span.textContent = String(++changes) }, 100)
    setTimeout(() => { clearInterval(timer); span.textContent = 'settled' }, 1_600)
  })
  await waitForReceiptReady(page)
  expect(await page.locator('span').textContent()).toBe('settled')
})
