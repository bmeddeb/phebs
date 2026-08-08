import { existsSync } from 'node:fs'
import { expect, test as setup } from '@playwright/test'

// Keep reusable auth outside Playwright's outputDir: that directory is
// cleared at the start of every run.
const STATE = 'receipts/.auth/auth.json'

// Authenticates against the local dev instance and stores the cookie session
// for the receipts project. A still-valid stored session is reused so that
// repeated receipt runs do not append fresh login events to the audit trail
// they capture. Credentials are never committed: supply the dev instance's
// operator login via environment.
setup('authenticate', async ({ playwright, baseURL }) => {
  if (existsSync(STATE)) {
    const stored = await playwright.request.newContext({ baseURL, storageState: STATE })
    const status = await stored.get('/api/auth/status')
    if (status.ok() && (await status.json()).authenticated === true) {
      await stored.dispose()
      return
    }
    await stored.dispose()
  }
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
