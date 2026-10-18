import { expect, test, type Page, type TestInfo } from '@playwright/test'
import { createRequire } from 'node:module'
import { repositoryServiceFixture } from './repositoryServiceFixture'
import { navigationRepositories as repos, navigationCommits as commits } from './navigationFixture'

const require = createRequire(import.meta.url)
async function check(page: Page, fixture: Awaited<ReturnType<typeof repositoryServiceFixture>>, info: TestInfo, image = 'navigation.png') {
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
  test(`repository browsing loads exact lazy folders and reuses expanded results ${width}`, async ({ page }, info) => {
    const fixture = await repositoryServiceFixture(page)
    await page.goto('/#/search')
    await expect(page).toHaveTitle('phebs · Search')
    if (width === 390) await page.getByRole('button', { name: 'Browse repositories' }).click()
    await page.getByRole('combobox', { name: 'Repository', exact: true }).selectOption(repos[1])
    const folder = page.getByRole('button', { name: 'src', exact: true })
    await folder.focus()
    await page.keyboard.press('Enter')
    const file = page.getByRole('navigation', { name: 'Repository files' }).getByRole('link', { name: 'main.go' })
    await expect(file).toHaveAttribute('href', `#/file?repo=example.test%2Fbeta&path=src%2Fmain.go&ref=${commits[1]}`)
    await folder.press('Enter')
    await folder.press('Enter')
    expect(fixture.folders.map(value => [value.repo, value.path])).toEqual([[repos[0], ''], [repos[1], ''], [repos[1], 'src']])
    await check(page, fixture, info)
  })

  test(`slow repository status settles then service navigation survives back forward ${width}`, async ({ page }, info) => {
    const fixture = await repositoryServiceFixture(page)
    fixture.holdNextMetadata()
    await page.clock.install()
    await page.goto('/#/repos')
    await expect(page).toHaveTitle('phebs · Repositories')
    await expect.poll(fixture.metadataHeld).toBe(true)
    await page.clock.fastForward(9000)
    expect(fixture.statusReads()).toBe(1)
    await fixture.releaseMetadata()
    const repository = page.getByRole('row').filter({ hasText: repos[1] })
    const link = repository.getByRole('link', { name: 'Services', exact: true })
    await expect(link).toHaveAttribute('href', '#/services?repository=example.test%2Fbeta')
    await check(page, fixture, info, 'repositories.png')
    await link.focus()
    const started = Date.now()
    await page.keyboard.press('Enter')
    const list = page.getByRole('listbox', { name: 'Services', exact: true })
    await list.focus()
    await page.keyboard.press('ArrowDown')
    await page.keyboard.press('ArrowUp')
    await page.keyboard.press('Enter')
    await expect(page.getByRole('heading', { name: 'Billing API', exact: true })).toBeVisible()
    console.log(JSON.stringify({ width, serviceReadyMillis: Date.now() - started }))
    const expected = '#/search?q=&scope=service&repository=example.test%2Fbeta&service_key=billing&scope_generation=sha256%3A' + 'd'.repeat(64)
    await expect(page.getByRole('link', { name: 'Search this service' })).toHaveAttribute('href', expected)
    await page.getByRole('link', { name: 'Close service detail' }).press('Enter')
    await expect(page.getByRole('heading', { name: 'Billing API', exact: true })).toHaveCount(0)
    await page.goBack()
    await expect(page.getByRole('heading', { name: 'Billing API', exact: true })).toBeVisible()
    await page.goForward()
    await expect(page.getByText('Select a service identity', { exact: true })).toBeVisible()
    expect(fixture.inventoryRequests.every(repo => repo === repos[1])).toBe(true)
    expect(fixture.detailRequests).toEqual(['billing', 'billing', 'billing', 'billing'])
    await check(page, fixture, info)
  })

  test(`crossed inventory is refused and exact retry restores navigation ${width}`, async ({ page }, info) => {
    const fixture = await repositoryServiceFixture(page)
    fixture.foreignInventory(true)
    await page.goto('/#/services?repository=example.test%2Falpha')
    await expect(page.getByText('Service inventory authority differs from the requested repository. Retry inventory.')).toBeVisible()
    await expect(page.getByRole('listbox', { name: 'Services', exact: true })).toHaveCount(0)
    await check(page, fixture, info, 'inventory-error.png')
    fixture.foreignInventory(false)
    await page.getByRole('button', { name: 'Retry', exact: true }).press('Enter')
    await expect(page.getByRole('option', { name: /Orders API/ })).toBeVisible()
    expect(fixture.inventoryRequests).toEqual([repos[0], repos[0]])
    await check(page, fixture, info)
  })

  test(`crossed detail cannot create service search links and retry retains its selection ${width}`, async ({ page }, info) => {
    const fixture = await repositoryServiceFixture(page)
    fixture.foreignDetail(true)
    await page.goto('/#/services?repository=example.test%2Falpha&service_key=orders')
    await expect(page.getByText('Service authority changed while loading. Retry detail.')).toBeVisible()
    await expect(page.getByRole('option', { name: /Orders API/ })).toBeVisible()
    await expect(page.getByRole('link', { name: 'Search this service' })).toHaveCount(0)
    await check(page, fixture, info, 'detail-error.png')
    fixture.foreignDetail(false)
    await page.getByRole('button', { name: 'Retry detail', exact: true }).press('Enter')
    await expect(page.getByRole('heading', { name: 'Orders API', exact: true })).toBeVisible()
    await expect(page.getByRole('link', { name: 'Search this service' })).toHaveAttribute('href', /service_key=orders&scope_generation=/)
    expect(fixture.detailRequests).toEqual(['orders', 'orders', 'orders'])
    await check(page, fixture, info)
  })

  test(`service transitions cannot carry the previous generation into scoped links ${width}`, async ({ page }, info) => {
    const fixture = await repositoryServiceFixture(page)
    await page.goto('/#/services?repository=example.test%2Falpha&service_key=orders')
    await expect(page.getByRole('heading', { name: 'Orders API', exact: true })).toBeVisible()
    const scope = page.getByRole('region', { name: 'Active scope', exact: true })
    await expect(scope.getByRole('link', { name: 'Directory', exact: true })).toHaveAttribute('href', /scope_generation=sha256%3Ac{64}$/)
    fixture.holdDetails('billing')
    await page.evaluate(() => { window.location.hash = '/services?repository=example.test%2Falpha&service_key=billing' })
    await expect.poll(fixture.detailsHeld).toBe(2)
    await expect(scope.getByRole('link', { name: 'Directory', exact: true })).toHaveAttribute('href', '#/services?repository=example.test%2Falpha&service_key=billing')
    await expect(scope.getByText('reading authority…')).toBeVisible()
    await check(page, fixture, info, 'scope-loading.png')
    await fixture.releaseDetails()
    await expect(page.getByRole('heading', { name: 'Billing API', exact: true })).toBeVisible()
    await expect(scope.getByRole('link', { name: 'Directory', exact: true })).toHaveAttribute('href', /scope_generation=sha256%3Ad{64}$/)
    await check(page, fixture, info)
  })
}
