import { defineConfig, devices } from '@playwright/test'

// T43.4 deterministic screenshot receipts. Runs against an already-running
// dev instance (make dev ARGS="-config phebs-ux-dev.yaml" in the repo root);
// point PHEBS_RECEIPTS_URL elsewhere to bind a different instance. Uses the
// system Chrome channel so no browser download is required.
export default defineConfig({
  testDir: './receipts',
  outputDir: './receipts/.artifacts',
  fullyParallel: false,
  workers: 1,
  // Zero retries: every failure is a first-class signal (review ruling on
  // T43.5). Transient slow loads are absorbed by the longer navigation
  // timeout below instead of a retry that could blur one-frame drift.
  retries: 0,
  timeout: 60_000,
  reporter: [['list']],
  snapshotPathTemplate: '{testDir}/baselines/{arg}{ext}',
  expect: {
    toHaveScreenshot: {
      // Bounded drift threshold: anything above 0.1% of pixels fails the run
      // and emits a visual diff into receipts/.artifacts.
      maxDiffPixelRatio: 0.001,
      animations: 'disabled',
      caret: 'hide',
    },
  },
  use: {
    baseURL: process.env.PHEBS_RECEIPTS_URL ?? 'http://127.0.0.1:3073',
    ...devices['Desktop Chrome'],
    channel: 'chrome',
    viewport: { width: 1280, height: 800 },
    deviceScaleFactor: 1,
    locale: 'en-US',
    timezoneId: 'UTC',
    navigationTimeout: 30_000,
    contextOptions: { reducedMotion: 'reduce' },
  },
  projects: [
    { name: 'setup', testMatch: /auth\.setup\.ts/ },
    {
      name: 'receipts',
      testMatch: /receipts\.spec\.ts/,
      dependencies: ['setup'],
      use: { storageState: 'receipts/.artifacts/auth.json' },
    },
    { name: 'anon', testMatch: /anon\.spec\.ts/ },
  ],
})
