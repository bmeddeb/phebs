import { expect, test } from '@playwright/test'
import { DEFAULT_PALETTE } from '../src/palette'
import { installIdentityFixture, scrollBelowHeader } from './fixture'
import { FROZEN_NOW, THEMES } from './routes'
import { ATLAS_DECLARATION_PATH, installAtlasDeclarationFixture } from './atlasFixture'

for (const theme of THEMES) for (const [name, width, height] of [
  ['desktop', 1280, 800], ['mobile', 390, 844],
] as const) {
  test(`contract-atlas-declaration-only-${name} · ${theme} · comfortable`, async ({ page }) => {
    await page.setViewportSize({ width, height })
    await installIdentityFixture(page)
    await installAtlasDeclarationFixture(page)
    await page.clock.setFixedTime(FROZEN_NOW)
    await page.addInitScript(({ theme, palette }) => {
      localStorage.setItem('phebs-theme', theme)
      localStorage.setItem('phebs-density', 'comfortable')
      localStorage.setItem('phebs-palette', palette)
    }, { theme, palette: DEFAULT_PALETTE })
    await page.emulateMedia({ colorScheme: theme })
    await page.goto('/#' + ATLAS_DECLARATION_PATH)
    await page.evaluate(() => document.fonts.ready)
    const detail = page.getByTestId('contract-operation-detail')
    await expect(detail).toBeVisible()
    await expect(detail.getByText('current', { exact: true })).toBeVisible()
    await expect(detail.getByText(/Relationship evidence unavailable: implementations, callers, unresolved candidates/)).toBeVisible()
    await expect(detail.getByRole('link', { name: 'Analyze impact' })).toHaveCount(0)
    await expect(detail.getByRole('link', { name: 'View callers' })).toHaveCount(0)
    await scrollBelowHeader(detail)
    await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth > innerWidth)).toBe(false)
    await expect(page).toHaveScreenshot(`contract-atlas-declaration-only-${name}--${theme}--comfortable.png`)
  })
}
