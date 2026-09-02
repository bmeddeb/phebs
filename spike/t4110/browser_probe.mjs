import { chromium } from '../../ui/node_modules/playwright/index.mjs'

const inputKeys = [
  'base_url',
  'bearer_token',
  'browser_path',
  'catalog_control_revision',
  'catalog_generation',
  'repository',
  'service_key',
  'state_control_revision',
]
const apiPaths = [
  '/api/auth/status',
  '/api/version',
  '/api/services',
  '/api/service',
]

function assert(condition) {
  if (!condition) throw new Error('live UI assertion failed')
}

async function readInput() {
  const chunks = []
  let size = 0
  for await (const chunk of process.stdin) {
    size += chunk.length
    assert(size <= 64 * 1024)
    chunks.push(chunk)
  }
  const value = JSON.parse(Buffer.concat(chunks).toString('utf8'))
  assert(value && typeof value === 'object' && !Array.isArray(value))
  assert(JSON.stringify(Object.keys(value).sort()) === JSON.stringify(inputKeys))
  for (const key of [
    'base_url', 'bearer_token', 'browser_path', 'catalog_generation',
    'repository', 'service_key',
  ]) {
    assert(typeof value[key] === 'string' && value[key].length > 0)
  }
  for (const key of ['catalog_control_revision', 'state_control_revision']) {
    assert(Number.isSafeInteger(value[key]) && value[key] > 0)
  }
  const base = new URL(value.base_url)
  assert(base.protocol === 'http:' && base.hostname === '127.0.0.1')
  assert(base.pathname === '/' && base.search === '' && base.hash === '')
  assert(value.browser_path.startsWith('/'))
  return { ...value, base }
}

function exactAuthority(repository, input) {
  assert(repository && typeof repository === 'object')
  assert(repository.repository === input.repository)
  assert(repository.catalog_generation === input.catalog_generation)
  assert(repository.catalog_control_revision === input.catalog_control_revision)
  assert(repository.state_control_revision === input.state_control_revision)
  assert(repository.catalog_service_count === 10_000)
}

function isGateOrigin(rawURL, gateOrigin) {
  const url = new URL(rawURL)
  if (url.protocol === 'ws:') url.protocol = 'http:'
  if (url.protocol === 'wss:') url.protocol = 'https:'
  return url.origin === gateOrigin
}

async function main() {
  const input = await readInput()
  let browser
  let context
  try {
    browser = await chromium.launch({
      executablePath: input.browser_path,
      headless: true,
      args: [
        '--disable-background-networking',
        '--disable-component-update',
        '--disable-default-apps',
        '--no-first-run',
      ],
    })
    context = await browser.newContext({
      extraHTTPHeaders: { Authorization: `Bearer ${input.bearer_token}` },
      locale: 'en-US',
      reducedMotion: 'reduce',
      serviceWorkers: 'block',
      timezoneId: 'UTC',
      viewport: { width: 1280, height: 800 },
    })
    const requestCounts = Object.fromEntries(apiPaths.map((path) => [path, 0]))
    let unexpectedAPIRequests = 0
    let consoleErrors = 0
    let pageErrors = 0
    let requestFailures = 0
    let apiFailures = 0
    let externalRequests = 0

    await context.route('**/*', async (route) => {
      if (!isGateOrigin(route.request().url(), input.base.origin)) {
        externalRequests++
        await route.abort('blockedbyclient')
        return
      }
      await route.continue()
    })
    await context.routeWebSocket(
      (url) => !isGateOrigin(url, input.base.origin),
      async (webSocket) => {
        externalRequests++
        await webSocket.close({ code: 1008, reason: 'external origin refused' })
      },
    )
    const page = await context.newPage()

    page.on('console', (message) => {
      if (message.type() === 'error') consoleErrors++
    })
    page.on('pageerror', () => { pageErrors++ })
    page.on('requestfailed', () => { requestFailures++ })
    page.on('request', (request) => {
      const requestURL = new URL(request.url())
      if (requestURL.origin !== input.base.origin || !requestURL.pathname.startsWith('/api/')) return
      if (Object.hasOwn(requestCounts, requestURL.pathname)) {
        requestCounts[requestURL.pathname]++
      } else {
        unexpectedAPIRequests++
      }
    })
    page.on('response', (response) => {
      const responseURL = new URL(response.url())
      if (responseURL.origin === input.base.origin && responseURL.pathname.startsWith('/api/') && !response.ok()) {
        apiFailures++
      }
    })

    const responses = apiPaths.map((path) => page.waitForResponse((response) => {
      const responseURL = new URL(response.url())
      return responseURL.origin === input.base.origin && responseURL.pathname === path
    }))
    const route = new URL('/', input.base)
    route.hash = `/services?${new URLSearchParams({
      repository: input.repository,
      service_key: input.service_key,
    }).toString()}`
    await page.goto(route.href, { waitUntil: 'domcontentloaded' })
    const [authResponse, versionResponse, inventoryResponse, detailResponse] = await Promise.all(responses)
    assert(authResponse.ok() && versionResponse.ok() && inventoryResponse.ok() && detailResponse.ok())

    const auth = await authResponse.json()
    const version = await versionResponse.json()
    const inventory = await inventoryResponse.json()
    const detail = await detailResponse.json()
    assert(auth.authenticated === true && auth.auth_required === true && auth.setup_required === false)
    assert(Array.isArray(version.capabilities) && version.capabilities.includes('service-catalog-v2'))
    assert(inventory.schema === 'phebs-service-inventory-v1')
    exactAuthority(inventory.repository, input)
    assert(inventory.filters?.repository === input.repository)
    assert(Array.isArray(inventory.services) && inventory.services.length === 50)
    assert(inventory.services[0]?.key === input.service_key)
    assert(inventory.pagination?.order === 'service_key:asc')
    assert(inventory.pagination?.page_size === 50)
    assert(inventory.pagination?.returned === 50)
    assert(typeof inventory.pagination?.next_cursor === 'string' && inventory.pagination.next_cursor.length > 0)
    assert(detail.schema === 'phebs-service-detail-v1')
    exactAuthority(detail.repository, input)
    assert(detail.service?.key === input.service_key)

    await page.getByRole('heading', { name: 'Service directory', exact: true }).waitFor({ state: 'visible' })
    const catalogLabel = page.getByText('Catalog services', { exact: true })
    await catalogLabel.waitFor({ state: 'visible' })
    await catalogLabel.locator('..').getByText('10000', { exact: true }).waitFor({ state: 'visible' })
    await page.getByText('50 in this page', { exact: true }).waitFor({ state: 'visible' })
    await page.getByText(input.service_key, { exact: true }).first().waitFor({ state: 'visible' })
    await page.waitForLoadState('networkidle')

    assert(page.url() === route.href)
    assert(await page.locator('[role="alert"]').count() === 0)
    await context.close()
    context = undefined
    assert(consoleErrors === 0 && pageErrors === 0 && requestFailures === 0 && apiFailures === 0)
    assert(unexpectedAPIRequests === 0 && externalRequests === 0)
    for (const path of apiPaths) {
      assert(requestCounts[path] === (path === '/api/service' ? 2 : 1))
    }

    process.stdout.write(JSON.stringify({
      schema: 't4110-live-ui-probe-v1',
      auth_status_requests: requestCounts['/api/auth/status'],
      version_requests: requestCounts['/api/version'],
      inventory_requests: requestCounts['/api/services'],
      detail_requests: requestCounts['/api/service'],
      catalog_services: inventory.repository.catalog_service_count,
      page_services: inventory.services.length,
      console_errors: consoleErrors,
      page_errors: pageErrors,
      request_failures: requestFailures,
      api_failures: apiFailures,
      external_requests: externalRequests,
      passed: true,
    }) + '\n')
  } finally {
    if (context) await context.close()
    if (browser) await browser.close()
  }
}

main().catch(() => {
  process.stderr.write('live UI probe failed\n')
  process.exitCode = 1
})
