import { test as base, expect, chromium, type Page } from "/Users/ben/.codex/worktrees/t512-field-registration/phebs.com/ui/node_modules/@playwright/test/index.mjs"
import { writeFileSync } from 'node:fs'
export { expect }
export type { Page }
export const test = base.extend({
  browser: [async ({}, use) => {
    const browser = await chromium.connectOverCDP(process.env.T4510_CDP!)
    try { await use(browser) } finally { await browser.close() }
  }, { scope: 'worker' }],
})
const errors = new WeakMap<Page, string[]>()
test.beforeEach(async ({ context, page }, info) => {
  await page.clock.setFixedTime(new Date('2026-08-07T20:00:00Z'))
  if (!info.title.startsWith('native profile preview')) return
  await context.addInitScript({ path: process.env.T4510_AXE! })
  const observed: string[] = []
  errors.set(page, observed)
  page.on('pageerror', error => observed.push(error.message))
  page.on('console', message => { if (message.type() === 'error') observed.push(message.text()) })
})
test.afterEach(async ({ page }, info) => {
  if (!info.title.startsWith('native profile preview')) return
  const report: any = { title: info.title, status: info.status, errors: errors.get(page), viewport: page.viewportSize() }
  try {
    expect(report.errors).toEqual([])
    report.audit = await page.evaluate(async () => {
      const axe = (window as any).axe
      const result = await axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa'] } })
      return { version: axe.version, violations: result.violations, passes: result.passes.length, incomplete: result.incomplete.length }
    })
    expect(report.audit.version).toBe('4.12.1')
    expect(report.audit.violations).toEqual([])
  } finally {
    writeFileSync(info.outputPath('native-qa.json'), JSON.stringify(report, null, 2) + '\n')
  }
})
