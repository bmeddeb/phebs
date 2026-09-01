import { existsSync } from 'node:fs'
import { expect, test as setup, type Browser } from '@playwright/test'

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

// Authenticates against the local dev instance and stores the cookie session
// for the receipts project. A still-valid stored session is reused so that
// repeated receipt runs do not append fresh login events to the audit trail
// they capture. Credentials are never committed: supply the dev instance's
// operator login via environment.
setup('authenticate', async ({ browser, playwright, baseURL }) => {
  // Chromium honors Secure cookies on trustworthy loopback origins, while an
  // APIRequestContext does not. Validate reuse through the same browser path
  // that will capture the receipts so a valid local session is not discarded.
  if (existsSync(STATE) && (await storedSessionIsAuthenticated(browser, baseURL))) return

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
})
