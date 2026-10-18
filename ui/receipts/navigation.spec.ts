import { expect, test, type Page, type TestInfo } from '@playwright/test'
import { createRequire } from 'node:module'
import { navigationFixture, navigationRepositories as repos, navigationCommits as commits } from './navigationFixture'

const require = createRequire(import.meta.url)
const fileHash = (repo: string, path = 'main.go', ref = '') => '/file?' + new URLSearchParams({ repo, path, ...(ref ? { ref } : {}) })

async function check(page: Page, fixture: Awaited<ReturnType<typeof navigationFixture>>, info: TestInfo, image = 'navigation.png') {
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
  test(`search selection survives folding an earlier group ${width}`, async ({ page }, info) => {
    const fixture = await navigationFixture(page)
    await page.goto('/#/search?q=Main')
    await expect(page).toHaveTitle('phebs · Search')
    await expect(page.getByRole('button', { name: new RegExp(repos[1]), expanded: true })).toBeVisible()
    await check(page, fixture, info, 'search.png')
    await page.locator('main').click({ position: { x: 2, y: 2 } })
    for (let index = 0; index < 3; index++) await page.keyboard.press('j')
    const fold = page.getByRole('button', { name: new RegExp('^' + repos[0]) })
    await fold.focus()
    await page.keyboard.press('Enter')
    await expect(fold).toBeFocused()
    await expect(fold).toHaveAttribute('aria-expanded', 'false')
    await page.locator('main').click({ position: { x: 2, y: 2 } })
    const started = Date.now()
    await page.keyboard.press('Enter')
    await expect(page).toHaveURL(new RegExp('repo=example.test%2Fbeta.*ref=' + commits[1] + '&L=3'))
    await expect(page.locator('.cm-content')).toContainText('package beta')
    console.log(JSON.stringify({ width, selectedFileReadyMillis: Date.now() - started }))
    expect(fixture.sources).toEqual([{ repo: repos[1], path: 'main.go', ref: commits[1] }])
    await check(page, fixture, info)
  })

  test(`repository changes wait for exact metadata and survive back forward ${width}`, async ({ page }, info) => {
    const fixture = await navigationFixture(page)
    await page.goto('/#' + fileHash(repos[0]))
    await expect(page.locator('.cm-content')).toContainText('package alpha')
    await page.locator('.cm-line').first().click({ position: { x: 35, y: 8 } })
    await expect.poll(() => fixture.symbols.length).toBe(3)
    fixture.holdNextMetadata()
    await page.evaluate(hash => { window.location.hash = hash }, fileHash(repos[1]))
    await expect.poll(fixture.metadataHeld).toBe(true)
    await expect(page.getByRole('button', { name: 'Permalink', exact: true })).toBeDisabled()
    expect(fixture.sources).toHaveLength(1)
    expect(fixture.symbols).toHaveLength(3)
    await expect(page.getByRole('complementary', { name: 'Code navigation' })).toHaveCount(0)
    await fixture.releaseMetadata()
    await expect(page.locator('.cm-content')).toContainText('package beta')
    await expect(page.getByRole('button', { name: 'Permalink', exact: true })).toBeEnabled()
    await page.goBack()
    await expect(page.locator('.cm-content')).toContainText('package alpha')
    await page.goForward()
    await expect(page.locator('.cm-content')).toContainText('package beta')
    expect(fixture.sources.map(source => source.ref)).toEqual([commits[0], commits[1], commits[0], commits[1]])
    expect(fixture.symbols).toHaveLength(3)
    await check(page, fixture, info)
  })

  test(`line links distinguish invalid preview parameters from valid source lines ${width}`, async ({ page }, info) => {
    const fixture = await navigationFixture(page)
    const hash = fileHash(repos[0], 'README.md', commits[0])
    await page.goto('/#' + hash + '&view=preview&L=1.5')
    await expect(page.getByRole('heading', { name: 'Navigation preview' })).toBeVisible()
    await page.evaluate(hash => { window.location.hash = hash }, hash + '&view=preview&L=3')
    const sourceLine = page.locator('.cm-line').filter({ hasText: 'Third line.' })
    await expect(sourceLine).toHaveAttribute('style', /box-shadow/)
    await expect(page.getByRole('link', { name: 'Markdown', exact: true })).toHaveAttribute('aria-current', 'true')
    await page.goBack()
    await expect(page.getByRole('heading', { name: 'Navigation preview' })).toBeVisible()
    expect(fixture.sources).toHaveLength(1)
    await check(page, fixture, info)
  })
}
