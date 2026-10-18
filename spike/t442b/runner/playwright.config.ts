import { defineConfig } from "/Users/ben/.codex/worktrees/t442b-key-copy/phebs.com/ui/node_modules/@playwright/test/index.mjs"
import house from "/Users/ben/.codex/worktrees/t442b-key-copy/phebs.com/ui/playwright.config"
export default defineConfig(house, {
  testDir: "/var/folders/wc/7grj940960386yt8vjsvv4dm0000gn/T/phebs-t442b-qa-rg7rva8k/runner",
  testMatch: /palette\.native\.spec\.ts/,
  outputDir: process.env.T442_OUTPUT!,
  reporter: [['list'], ['json', { outputFile: process.env.T442_REPORT! }]],
  workers: 1,
  retries: 0,
  timeout: 20_000,
  projects: [{ name: 'appearance', testMatch: /palette\.native\.spec\.ts/ }],
  use: { ...house.use, baseURL: process.env.PHEBS_RECEIPTS_URL, channel: undefined },
})
