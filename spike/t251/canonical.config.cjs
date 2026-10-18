const { defineConfig, devices } = require('@playwright/test');

const baseURL = process.env.PHEBS_DOCS_RECEIPTS_URL;
if (!baseURL) throw new Error('PHEBS_DOCS_RECEIPTS_URL must name the live documentation auth fixture');

// Small T25 surface comparison in the existing digest-pinned receipt runtime.
// This standalone config never starts the ordinary 143-case product matrix.
module.exports = defineConfig({
  testDir: __dirname,
  testMatch: 'canonical.spec.cjs',
  outputDir: './.canonical-artifacts',
  snapshotPathTemplate: '{testDir}/baselines/{arg}{ext}',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 30_000,
  reporter: [['list']],
  expect: {
    toHaveScreenshot: {
      maxDiffPixelRatio: 0.001,
      animations: 'disabled',
      caret: 'hide',
    },
  },
  use: {
    ...devices['Desktop Chrome'],
    baseURL,
    deviceScaleFactor: 1,
    locale: 'en-US',
    timezoneId: 'UTC',
    reducedMotion: 'reduce',
    navigationTimeout: 15_000,
  },
});
