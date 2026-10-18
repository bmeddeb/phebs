import { expect, test, type Locator } from '@playwright/test'
import { DEFAULT_PALETTE } from '../src/palette'
import { installIdentityFixture } from './fixture'
import { FROZEN_NOW, THEMES } from './routes'
import {
  OPERATION_IMPACT_OPERATION, OPERATION_IMPACT_PATH, OPERATION_IMPACT_REPOSITORY,
  installOperationImpactFixture,
} from './operationImpactFixture'

async function disclose(summary: Locator) {
  await expect(summary).toBeVisible()
  await summary.focus()
  await expect(summary).toBeFocused()
  if (!await summary.evaluate(node => node.parentElement?.hasAttribute('open'))) await summary.press('Enter')
  await expect(summary.locator('..')).toHaveAttribute('open', '')
}

// Four new-shape presentation frames, independent of retained legacy-v2
// Impact pixels and the T54.3 public-source quality population. The house
// config supplies 0 retries, 0.1% pixel tolerance, fonts, scale and motion.
// Author + fresh comparison must bind the actual renderer/host; these files
// do not claim an Ubuntu/Noble renderer or a source-quality result.
for (const theme of THEMES) for (const state of ['current', 'unavailable'] as const) {
  test(`operation-impact-${state}-desktop · ${theme} · comfortable`, async ({ page }) => {
    const consoleErrors: string[] = []
    page.on('pageerror', error => consoleErrors.push(error.message))
    page.on('console', message => {
      if (message.type() === 'error' || message.type() === 'warning') consoleErrors.push(message.text())
    })
    await page.setViewportSize({ width: 1280, height: 800 })
    await installIdentityFixture(page)
    const fixture = await installOperationImpactFixture(page, state)
    await page.clock.setFixedTime(FROZEN_NOW)
    await page.addInitScript(({ theme, palette }) => {
      localStorage.setItem('phebs-theme', theme)
      localStorage.setItem('phebs-density', 'comfortable')
      localStorage.setItem('phebs-palette', palette)
    }, { theme, palette: DEFAULT_PALETTE })
    await page.emulateMedia({ colorScheme: theme })
    await page.goto('/#' + OPERATION_IMPACT_PATH)
    await page.evaluate(() => document.fonts.ready)
    await expect(page).toHaveTitle('phebs · Contract impact')
    const report = page.getByTestId('caller-map-page')
    await expect(report.getByRole('heading', { name: OPERATION_IMPACT_OPERATION, exact: true })).toBeVisible()
    await expect(report.getByRole('form', { name: 'Caller Map filters' })).toBeVisible()
    await expect(page.getByText('ux-audit@localhost.test', { exact: true })).toBeVisible()
    await expect(page.getByRole('tablist', { name: 'Impact report kind' })).toHaveCount(0)

    if (state === 'current') {
      await expect(report.getByText('Rows 1–2 of 2', { exact: true })).toBeVisible()
      await expect(report.getByTestId('caller-map-row')).toHaveCount(2)
      await expect(report.getByTestId('caller-map-unit-candidate')).toHaveCount(2)
      await expect(report.getByText('target: //client:cart', { exact: true })).toBeVisible()
      await expect(report.getByText('mapping has no attribution', { exact: true })).toBeVisible()
      const citation = report.getByRole('button', { name: `${OPERATION_IMPACT_REPOSITORY}/client/cart.go:1`, exact: true })
      await citation.focus()
      await expect(citation).toBeFocused()
      await citation.press('Enter')
      await expect(report.getByTestId('caller-map-exact-citation')).toBeVisible()
      await expect(report.getByLabel(`Exact cited bytes for ${OPERATION_IMPACT_REPOSITORY}/client/cart.go:1`)).toHaveText('client.Get(ctx, request)')
    } else {
      await expect(report.getByText('Caller totals unavailable', { exact: true })).toBeVisible()
      await expect(report.getByText('Caller evidence is unavailable for this generation; no zero-caller or absence conclusion is made.', { exact: true })).toBeVisible()
      await expect(report.getByTestId('caller-map-row')).toHaveCount(0)
      await expect(report.getByRole('link', { name: 'Declaration', exact: true })).toHaveCount(0)
      await expect(report.getByText('No matching static evidence', { exact: true })).toHaveCount(0)
    }

    await disclose(report.locator('summary').filter({ hasText: /^Source authority and scope$/ }))
    await expect(report.getByTestId('caller-map-generation')).toHaveAttribute('data-matching-rows-state', state === 'current' ? 'exact' : 'unavailable')
    await expect(report.getByTestId('analysis-scope-panel')).toBeVisible()
    await disclose(report.locator('summary').filter({ hasText: /^Other report sections · unavailable$/ }))
    for (const section of ['field references', 'proposed change', 'implementations', 'wire verdict', 'matching syntactic calls', 'saved report']) {
      await expect(report.getByText(`${section}: not in this supported report shape.`, { exact: true })).toBeVisible()
    }
    await disclose(report.locator('summary').filter({ hasText: state === 'current'
      ? /^Repository-overlay static source evidence only/
      : /^Caller totals and absence are unavailable until/ }))
    await expect(report.getByText(state === 'current'
      ? 'a complete generation is not runtime use, completeness, migration completion, or decommissioning safety.'
      : 'Caller totals and absence are unavailable until one exact complete repository-overlay generation is current.', { exact: true })).toBeVisible()
    // Restore the header to the first viewport after keyboard disclosures.
    await page.evaluate(() => window.scrollTo(0, 0))
    await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth > innerWidth)).toBe(false)
    expect(fixture.unexpected).toEqual([])
    expect(consoleErrors).toEqual([])
    await expect(page).toHaveScreenshot(`operation-impact-${state}-desktop--${theme}--comfortable.png`, { fullPage: true })
  })
}
