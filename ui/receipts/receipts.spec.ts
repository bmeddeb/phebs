import { expect, test, type Page } from '@playwright/test'
import { DENSITIES, FROZEN_NOW, ROUTES, THEMES, type ReceiptRoute } from './routes'

type ReceiptWindow = typeof window & { __phebsReceiptPending?: number }

async function waitForReceiptReady(page: Page) {
  // Let lazy-route effects begin before observing the request counter.
  await page.evaluate(() => new Promise<void>((resolve) => {
    requestAnimationFrame(() => requestAnimationFrame(() => resolve()))
  }))
  await expect.poll(
    () => page.evaluate(() => (window as ReceiptWindow).__phebsReceiptPending ?? 0),
    { message: 'receipt route still has an active API request', timeout: 30_000 },
  ).toBe(0)

  await expect.poll(async () => {
    const states = await page.locator('main [role="status"]').evaluateAll((nodes) =>
      nodes.map((node) => `${node.getAttribute('aria-label') ?? ''} ${node.textContent ?? ''}`.trim()),
    )
    return states.some((state) => /\b(loading|reading|checking|searching)\b/i.test(state))
  }, { message: 'receipt route still renders a loading state', timeout: 30_000 }).toBe(false)

  let prior = ''
  let stableSamples = 0
  await expect.poll(async () => {
    const next = await page.locator('main').evaluate((node) => node.innerHTML)
    stableSamples = next === prior ? stableSamples + 1 : 0
    prior = next
    return stableSamples
  }, {
    message: 'receipt route did not reach a stable rendered state',
    timeout: 30_000,
    intervals: [250],
  }).toBeGreaterThanOrEqual(3)
}

async function capture(page: Page, route: ReceiptRoute, theme: (typeof THEMES)[number], name: string) {
  const { path } = route
  if (route.viewport) await page.setViewportSize(route.viewport)
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
  await page.emulateMedia({ colorScheme: theme })
  await page.goto(`/#${path}`)
  await page.waitForSelector('main', { state: 'visible' })
  await page.evaluate(() => document.fonts.ready)
  await waitForReceiptReady(page)
  await expect(page).toHaveScreenshot(`${name}.png`, {
    mask: route.mask?.map((selector) => page.locator(selector)),
  })
}

for (const theme of THEMES) {
  for (const density of DENSITIES) {
    for (const route of ROUTES) {
      test(`${route.name} · ${theme} · ${density}`, async ({ page }) => {
        await capture(page, route, theme, `${route.name}--${theme}--${density}`)
      })
    }
  }
}
