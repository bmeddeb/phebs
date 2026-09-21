import { existsSync } from 'node:fs'
import { expect, test as setup, type Browser, type Page } from '@playwright/test'
import {
  T307_REPOSITORY,
  T323_REPOSITORY,
  receiptCohortReadiness,
  type ReceiptCohortObservation,
} from './fixtureRoot'

// Keep reusable auth outside Playwright's outputDir: that directory is
// cleared at the start of every run.
const STATE = 'receipts/.auth/auth.json'

async function storedSessionIsAuthenticated(browser: Browser, baseURL: string | undefined): Promise<boolean> {
  if (!baseURL) return false

  const context = await browser.newContext({ baseURL, storageState: STATE })
  try {
    const page = await context.newPage()
    const status = await page.goto('/api/auth/status')
    return status?.ok() === true && (await status.json()).authenticated === true
  } finally {
    await context.close()
  }
}

class ReceiptCohortPending extends Error {}

async function readJSON(page: Page, path: string): Promise<unknown> {
  const response = await page.goto(path)
  if (!response) throw new Error(`receipt readiness request returned no response: ${path}`)
  if ([404, 409, 503].includes(response.status())) {
    throw new ReceiptCohortPending(`${path}: ${response.status()}`)
  }
  if (!response.ok()) throw new Error(`receipt readiness request failed: ${path}: ${response.status()}`)
  return response.json()
}

async function waitForReceiptCohort(browser: Browser, baseURL: string | undefined): Promise<void> {
  if (!baseURL) throw new Error('Playwright baseURL is required for receipt readiness')
  const context = await browser.newContext({ baseURL, storageState: STATE })
  const page = await context.newPage()
  const deadline = Date.now() + 10 * 60_000
  let detail = 'receipt cohort has not been observed'
  try {
    while (Date.now() < deadline) {
      try {
        const observation: ReceiptCohortObservation = {
          repositories: await readJSON(page, '/api/repo-status'),
        }
        const repositories = receiptCohortReadiness(observation)
        if (repositories.state === 'failed') throw new Error(repositories.detail)

        const params = (values: Record<string, string>) => new URLSearchParams(values).toString()
        observation.t307Services = await readJSON(page, `/api/services?${params({
          repository: T307_REPOSITORY,
          page_size: '100',
        })}`)
        observation.t323Services = await readJSON(page, `/api/services?${params({
          repository: T323_REPOSITORY,
          page_size: '100',
        })}`)
        observation.t307Relationships = await readJSON(page, `/api/service-relationships?${params({
          repository: T307_REPOSITORY,
          service_key: 'orders-api',
          view: 'all',
          page_size: '100',
        })}`)

        const readiness = receiptCohortReadiness(observation)
        if (readiness.state === 'ready') return
        if (readiness.state === 'failed') throw new Error(readiness.detail)
        detail = readiness.detail
      } catch (error) {
        if (!(error instanceof ReceiptCohortPending)) throw error
        detail = error.message
      }
      await new Promise((resolve) => setTimeout(resolve, 5_000))
    }
    throw new Error(`receipt cohort did not settle within 10 minutes: ${detail}`)
  } finally {
    await context.close()
  }
}

// Authenticates against the local dev instance and stores the cookie session
// for the receipts project. A still-valid stored session is reused so that
// repeated receipt runs do not append fresh login events to the audit trail
// they capture. Credentials are never committed: supply the dev instance's
// operator login via environment.
setup('authenticate', async ({ browser, playwright, baseURL }, testInfo) => {
  testInfo.setTimeout(12 * 60_000)
  // Chromium honors Secure cookies on trustworthy loopback origins, while an
  // APIRequestContext does not. Validate reuse through the same browser path
  // that will capture the receipts so a valid local session is not discarded.
  if (!existsSync(STATE) || !(await storedSessionIsAuthenticated(browser, baseURL))) {
    const email = process.env.PHEBS_RECEIPT_EMAIL
    const password = process.env.PHEBS_RECEIPT_PASSWORD
    if (!email || !password) {
      throw new Error('Set PHEBS_RECEIPT_EMAIL and PHEBS_RECEIPT_PASSWORD to the local dev instance operator login.')
    }
    const fresh = await playwright.request.newContext({ baseURL })
    const response = await fresh.post('/api/auth/login', { data: { email, password } })
    expect(response.ok(), `login failed: ${response.status()}`).toBeTruthy()
    await fresh.storageState({ path: STATE })
    await fresh.dispose()
  }
  await waitForReceiptCohort(browser, baseURL)
})
