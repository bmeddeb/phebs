import { expect, test, type Page } from '@playwright/test'
import { DENSITIES, FROZEN_NOW, ROUTES, THEMES, type ReceiptRoute } from './routes'

async function capture(page: Page, route: ReceiptRoute, theme: (typeof THEMES)[number], name: string) {
  const { path } = route
  if (route.viewport) await page.setViewportSize(route.viewport)
  // Fixed Date only — timers, rAF, and CodeMirror layout stay live; age copy
  // stops drifting between capture and check runs.
  await page.clock.setFixedTime(FROZEN_NOW)
  await page.addInitScript((mode) => localStorage.setItem('phebs-theme', mode as string), theme)
  await page.emulateMedia({ colorScheme: theme })
  await page.goto(`/#${path}`)
  await page.waitForSelector('main', { state: 'visible' })
  await page.evaluate(() => document.fonts.ready)
  // One settle beat for streaming results and CodeMirror paint; polling loops
  // re-render identical fixture data so this stays deterministic per boot.
  await page.waitForTimeout(1200)
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
