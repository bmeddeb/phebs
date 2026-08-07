import { expect, test } from '@playwright/test'
import { FROZEN_NOW, THEMES } from './routes'

// The unauthenticated surface: the sign-in page, both themes.
for (const theme of THEMES) {
  test(`login · ${theme}`, async ({ page }) => {
    await page.clock.setFixedTime(FROZEN_NOW)
    await page.addInitScript((mode) => localStorage.setItem('phebs-theme', mode as string), theme)
    await page.emulateMedia({ colorScheme: theme })
    await page.goto('/#/')
    await page.waitForSelector('text=Sign in')
    await page.evaluate(() => document.fonts.ready)
    await page.waitForTimeout(400)
    await expect(page).toHaveScreenshot(`login--${theme}.png`)
  })
}
