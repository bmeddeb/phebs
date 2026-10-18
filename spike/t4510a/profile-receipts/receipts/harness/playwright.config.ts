import { defineConfig } from "/Users/ben/.codex/worktrees/t512-field-registration/phebs.com/ui/node_modules/@playwright/test/index.mjs"
import house from "/Users/ben/.codex/worktrees/t512-field-registration/phebs.com/ui/playwright.config"
export default defineConfig(house, {
  testDir: "/tmp/phebs-t4510a-qa-z3azm2qg/runner",
  testMatch: /profile\.native\.spec\.ts/,
  outputDir: process.env.T4510_OUTPUT!,
  reporter: [['list'], ['json', { outputFile: process.env.T4510_REPORT! }]],
  workers: 1,
  retries: 0,
  projects: [{ name: 'typed-index', testMatch: /profile\.native\.spec\.ts/ }],
  use: { ...house.use, baseURL: process.env.PHEBS_RECEIPTS_URL, channel: undefined },
})
