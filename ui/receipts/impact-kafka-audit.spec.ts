import { expect, test, type Page, type TestInfo } from '@playwright/test'
import { createRequire } from 'node:module'
import { impactKafkaFixture, impactReport, SAVED_IMPACT_PATH } from './impactKafkaFixture'
import { OPERATION_IMPACT_PATH, OPERATION_IMPACT_REPOSITORY } from './operationImpactFixture'

const require = createRequire(import.meta.url)
async function check(page: Page, fixture: Awaited<ReturnType<typeof impactKafkaFixture>>, info: TestInfo, image = 'navigation.png') {
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
  expect(fixture.stableUnexpected).toEqual([])
  await page.screenshot({ path: info.outputPath(image), animations: 'disabled', caret: 'hide' })
}

test.beforeEach(async ({ page }, info) => {
  const width = info.title.endsWith('390') ? 390 : 1280
  await page.setViewportSize({ width, height: 900 })
  await page.emulateMedia({ colorScheme: width === 390 ? 'dark' : 'light', reducedMotion: 'reduce' })
  await page.clock.setFixedTime(new Date('2026-10-18T12:00:00Z'))
})

for (const width of [1280, 390]) {
  test(`abandoned manual Impact cannot redirect from Topics ${width}`, async ({ page }, info) => {
    const fixture = await impactKafkaFixture(page)
    fixture.hold('/api/contract_impact_report')
    await page.goto('/#/impact?operation=%2Fshop.Cart%2FGet')
    await page.getByRole('button', { name: 'Build report' }).press('Enter')
    await expect.poll(fixture.held).toBe(true)
    await page.getByRole('link', { name: 'Topics', exact: true }).press('Enter')
    await expect(page.getByRole('heading', { name: 'Topic inventory', exact: true })).toBeVisible()
    await fixture.release()
    await expect(page).toHaveURL(/#\/topics\?inventory=pb_inv$/)
    await check(page, fixture, info)
  })

  test(`saved Impact refuses crossed identity and retries the exact bundle ${width}`, async ({ page }, info) => {
    const fixture = await impactKafkaFixture(page)
    fixture.badSaved(true)
    await page.goto('/#' + SAVED_IMPACT_PATH)
    await expect(page.getByText(/different saved report/)).toBeVisible()
    await expect(page.getByText(impactReport.conclusion.text, { exact: true })).toHaveCount(0)
    await check(page, fixture, info, 'saved-error.png')
    fixture.badSaved(false)
    const started = Date.now()
    await page.getByRole('button', { name: 'Retry', exact: true }).press('Enter')
    await expect(page.getByText(impactReport.conclusion.text, { exact: true })).toBeVisible()
    console.log(JSON.stringify({ width, savedRetryMillis: Date.now() - started }))
    const citation = page.getByRole('link', { name: /github.com\/acme\/cart-client\/client\/cart.go:27/ })
    await expect(citation).toHaveAttribute('href', /ref=0123456789abcdef0123456789abcdef01234567&L=27$/)
    expect(fixture.reads).toEqual(Array(2).fill('/api/contract_impact_reports/' + impactReport.bundle_id))
    fixture.fail('/api/contract_impact_reports/pb_replacement')
    await page.evaluate(() => { window.location.hash = '#/impact?bundle=pb_replacement' })
    await expect(page.getByText(/temporary audit failure/)).toBeVisible()
    await expect(page.getByText(impactReport.conclusion.text, { exact: true })).toHaveCount(0)
    // Chromium logs this deliberately injected HTTP failure independently of the app.
    expect(fixture.errors.splice(0)).toEqual(['Failed to load resource: the server responded with a status of 503 (Service Unavailable)'])
    await check(page, fixture, info, 'replacement-error.png')
    fixture.fail('')
    await page.goBack()
    await expect(page.getByText(impactReport.conclusion.text, { exact: true })).toBeVisible()
  })

  test(`operation handoffs follow back and forward without requests ${width}`, async ({ page }, info) => {
    const fixture = await impactKafkaFixture(page)
    await page.goto('/#/impact?operation=%2Fshop.Cart%2FGet')
    await expect(page.getByLabel('Canonical operation')).toHaveValue('/shop.Cart/Get')
    await page.evaluate(() => { window.location.hash = '#/impact?operation=%2Fshop.Cart%2FPut' })
    await expect(page.getByLabel('Canonical operation')).toHaveValue('/shop.Cart/Put')
    await page.goBack()
    await expect(page.getByLabel('Canonical operation')).toHaveValue('/shop.Cart/Get')
    await page.goForward()
    await expect(page.getByLabel('Canonical operation')).toHaveValue('/shop.Cart/Put')
    expect(fixture.reads).toEqual([])
    await check(page, fixture, info)
  })

  test(`Topics abandons composition and malformed detail preserves every citation ${width}`, async ({ page }, info) => {
    const fixture = await impactKafkaFixture(page)
    fixture.hold('/api/list_kafka_topics')
    fixture.badDetail(true)
    await page.goto('/#/topics')
    await expect.poll(fixture.held).toBe(true)
    await page.getByLabel('Kafka topic', { exact: true }).fill('orders-v1')
    await page.getByRole('button', { name: 'Query', exact: true }).press('Enter')
    await expect(page.getByTestId('topic-usage')).toBeVisible()
    await fixture.release()
    await expect(page).toHaveURL(/#\/topics\?topic=orders-v1$/)
    await expect(page.getByRole('link', { name: 'example.com/app/svc/producer.go:12', exact: true })).toHaveAttribute('href', /ref=c0ffee&L=12$/)
    await expect(page.getByRole('link', { name: 'example.com/app/svc/producer.go:30', exact: true })).toHaveAttribute('href', /ref=c0ffee&L=30$/)
    await expect(page.getByTestId('unresolved-census')).toContainText('At least 3 producer source sites')
    await check(page, fixture, info)
  })

  test(`Topics keeps cursor navigation current and clears usage errors on return ${width}`, async ({ page }, info) => {
    const fixture = await impactKafkaFixture(page)
    await page.goto('/#/topics?inventory=pb_inv')
    await expect(page.getByRole('link', { name: 'orders-v1', exact: true })).toBeVisible()
    fixture.hold('/api/topic_inventory/pb_inv')
    await page.getByRole('button', { name: 'Next page' }).press('Enter')
    await expect.poll(fixture.held).toBe(true)
    await page.goBack()
    await expect(page.getByRole('link', { name: 'orders-v1', exact: true })).toBeVisible()
    await fixture.release()
    await expect(page.getByRole('link', { name: 'orders-v1', exact: true })).toBeVisible()
    fixture.badTopic(true)
    await page.getByRole('link', { name: 'orders-v1', exact: true }).press('Enter')
    await expect(page.getByText(/different topic/)).toBeVisible()
    await expect(page.getByTestId('topic-usage')).toHaveCount(0)
    await check(page, fixture, info, 'topic-error.png')
    fixture.badTopic(false)
    await page.getByRole('button', { name: 'Query', exact: true }).press('Enter')
    await expect(page.getByTestId('topic-usage')).toBeVisible()
    await page.goBack()
    await expect(page.getByRole('link', { name: 'orders-v1', exact: true })).toBeVisible()
    await expect(page.getByText(/different topic/)).toHaveCount(0)
    await check(page, fixture, info)
  })

  test(`isolated operation Impact keeps exact citation and claim boundary ${width}`, async ({ page }, info) => {
    const fixture = await impactKafkaFixture(page)
    await page.goto('/#' + OPERATION_IMPACT_PATH)
    await expect(page.getByTestId('caller-map-page')).toBeVisible()
    const citation = page.getByRole('button', { name: `${OPERATION_IMPACT_REPOSITORY}/client/cart.go:1`, exact: true })
    await citation.press('Enter')
    await expect(page.getByLabel(/Exact cited bytes for/)).toHaveText('client.Get(ctx, request)')
    expect(fixture.sources).toEqual([])
    await check(page, fixture, info)
  })
}
