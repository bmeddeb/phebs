import { defineConfig } from "/Users/ben/.codex/worktrees/t442a-preview-recovery/phebs.com/ui/node_modules/@playwright/test/index.mjs"
import house from "/Users/ben/.codex/worktrees/t442a-preview-recovery/phebs.com/ui/playwright.config"
export default defineConfig(house, {
  testDir: "/var/folders/wc/7grj940960386yt8vjsvv4dm0000gn/T/phebs-t442a-qa-uz1tx612/runner",
  testMatch: /palette\.native\.spec\.ts/,
  outputDir: process.env.T442_OUTPUT!,
  reporter: [['list'], ['json', { outputFile: process.env.T442_REPORT! }]],
  workers: 1,
  retries: 0,
  timeout: 20_000,
  projects: [{ name: 'appearance', testMatch: /palette\.native\.spec\.ts/ }],
  use: { ...house.use, baseURL: process.env.PHEBS_RECEIPTS_URL, channel: undefined },
})
