import { expect, test, type Page, type TestInfo } from '@playwright/test'
import { createRequire } from 'node:module'
import { callerAtlasFixture, ATLAS_DECLARATION_PATH, CALLER_AUDIT_PATH } from './callerAtlasFixture'

const require = createRequire(import.meta.url)
async function check(page: Page, fixture: Awaited<ReturnType<typeof callerAtlasFixture>>, info: TestInfo, image = 'navigation.png') {
  await expect(page.locator('vite-error-overlay')).toHaveCount(0)
  await page.evaluate(async () => {
    await document.fonts.ready
    await Promise.all(document.getAnimations().filter(animation => animation.effect?.getComputedTiming().iterations !== Infinity).map(animation => animation.finished.catch(() => {})))
  })
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  await page.addScriptTag({ path: require.resolve('axe-core/axe.min.js') })
  const violations = await page.evaluate(async () => {
    const axe = (window as unknown as { axe: { run: (options: unknown) => Promise<{ violations: unknown[] }> } }).axe
    return (await axe.run({ runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa'] } })).violations
  })
  expect(violations).toEqual([])
  expect(fixture.errors).toEqual([])
  expect(fixture.unexpected).toEqual([])
  await page.screenshot({ path: info.outputPath(image), animations: 'disabled', caret: 'hide' })
}

test.beforeEach(async ({ page }, info) => {
  const width = info.title.endsWith('390') ? 390 : 1280
  await page.setViewportSize({ width, height: 900 })
  await page.emulateMedia({ colorScheme: width === 390 ? 'dark' : 'light', reducedMotion: 'reduce' })
  await page.clock.setFixedTime(new Date('2026-10-18T12:00:00Z'))
})

for (const width of [1280, 390]) {
  test(`caller view and filter navigation issue only their intended reads ${width}`, async ({ page }, info) => {
    const fixture = await callerAtlasFixture(page)
    await page.goto('/#' + CALLER_AUDIT_PATH)
    await expect(page).toHaveTitle(/phebs · Caller/)
    await expect(page.getByText('Rows 1–1 of 1')).toBeVisible()
    const started = Date.now()
    await page.getByRole('button', { name: 'Unit view', exact: true }).press('Enter')
    await expect(page.getByLabel('Caller rows grouped by attributed unit')).toBeVisible()
    await page.goBack()
    await expect(page.getByRole('button', { name: 'Source view', exact: true })).toHaveAttribute('aria-pressed', 'true')
    expect(fixture.callers).toHaveLength(1)
    console.log(JSON.stringify({ width, callerViewRoundTripMillis: Date.now() - started }))
    await page.getByLabel('Owner', { exact: true }).fill('team-demo')
    await page.getByRole('button', { name: 'Apply filters', exact: true }).press('Enter')
    await expect.poll(() => fixture.callers.length).toBe(2)
    await expect(page.getByText('Rows 1–1 of 1')).toBeVisible()
    expect(fixture.callers.map(query => query.get('owner'))).toEqual([null, 'team-demo'])
    await check(page, fixture, info)
  })

  test(`caller citations reject crossed bytes and refresh only the caller page ${width}`, async ({ page }, info) => {
    const fixture = await callerAtlasFixture(page)
    await page.goto('/#' + CALLER_AUDIT_PATH)
    const citation = page.getByRole('button', { name: /example.invalid\/demo-client\/client.go:1/ })
    fixture.badCitation(true)
    await citation.press('Enter')
    await expect(page.getByText(/Exact citation unavailable: Exact citation response did not match/)).toBeVisible()
    await check(page, fixture, info, 'citation-error.png')
    fixture.badCitation(false)
    await page.getByRole('button', { name: 'Refresh caller rows' }).press('Enter')
    await expect.poll(() => fixture.callers.length).toBe(2)
    await expect(page.getByRole('button', { name: 'Refresh caller rows' })).toHaveCount(0)
    await expect(page.getByRole('status', { name: 'Loading Caller Map' })).toHaveCount(0)
    await citation.press('Enter')
    await expect(page.getByLabel('Exact cited bytes for example.invalid/demo-client/client.go:1')).toHaveText('client.Get(ctx)')
    expect(fixture.sources).toEqual([])
    await check(page, fixture, info)
  })

  test(`Atlas clear and back forward keep selection requests exact ${width}`, async ({ page }, info) => {
    const fixture = await callerAtlasFixture(page)
    fixture.holdSelection()
    await page.goto('/#' + ATLAS_DECLARATION_PATH)
    await expect(page).toHaveTitle('phebs · Contract atlas')
    await expect.poll(fixture.selectionHeld).toBe(true)
    await page.getByRole('button', { name: 'Clear', exact: true }).press('Enter')
    await expect(page.getByText('Select an operation', { exact: true })).toBeVisible()
    await fixture.releaseSelection()
    await expect(page.getByTestId('contract-operation-detail')).toHaveCount(0)
    await page.goBack()
    await expect(page.getByTestId('contract-operation-detail')).toBeVisible()
    await expect(page.getByTestId('contract-operation-detail').getByRole('link', { name: 'example.invalid/atlas-receipt/api/catalog.proto:3', exact: true }).first()).toHaveAttribute('href', /ref=a{40}/)
    await page.goForward()
    await expect(page.getByText('Select an operation', { exact: true })).toBeVisible()
    expect(fixture.selections).toHaveLength(2)
    await check(page, fixture, info)
  })

  test(`Atlas protocol and legacy identity failures have exact selection retry ${width}`, async ({ page }, info) => {
    const fixture = await callerAtlasFixture(page)
    await page.goto('/#' + ATLAS_DECLARATION_PATH)
    await expect(page.getByTestId('contract-operation-detail')).toBeVisible()
    await page.evaluate(() => { window.location.hash = window.location.hash.replace('sel_protocol=protobuf', 'sel_protocol=thrift') })
    await expect(page.getByText('Operation evidence authority does not match its pinned source. Retry the selection.')).toBeVisible()
    await expect(page.getByTestId('contract-operation-detail')).toHaveCount(0)
    await check(page, fixture, info, 'protocol-error.png')
    fixture.badSelection(true)
    await page.goBack()
    await expect(page.getByRole('button', { name: 'Retry selection' })).toBeVisible()
    await expect(page.getByTestId('contract-operation-detail')).toHaveCount(0)
    fixture.badSelection(false)
    await page.getByRole('button', { name: 'Retry selection' }).press('Enter')
    await expect(page.getByTestId('contract-operation-detail')).toBeVisible()
    expect(fixture.selections).toHaveLength(4)
    expect(new Set(fixture.selections.map(query => query.toString())).size).toBe(1)
    await check(page, fixture, info)
  })

  test(`Atlas failed replacement queries hide old rows and retry the current page ${width}`, async ({ page }, info) => {
    const fixture = await callerAtlasFixture(page)
    await page.goto('/#/contracts')
    await expect(page.getByRole('listitem', { name: /Get/ })).toBeVisible()
    fixture.badCatalog(true)
    await page.getByLabel('Package', { exact: true }).fill('missing')
    await page.getByRole('button', { name: 'Apply', exact: true }).press('Enter')
    await expect(page.getByText('Catalog declaration authority is inconsistent. Retry the catalog.')).toBeVisible()
    await expect(page.getByRole('listitem', { name: /Get/ })).toHaveCount(0)
    await check(page, fixture, info, 'catalog-error.png')
    fixture.badCatalog(false)
    await page.getByRole('button', { name: 'Retry catalog page' }).press('Enter')
    await expect(page.getByText(/No declaration rows were returned/)).toBeVisible()
    expect(fixture.lists.map(query => query.get('package'))).toEqual([null, 'missing', 'missing'])
    await check(page, fixture, info)
  })
}
