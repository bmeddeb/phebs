# Instructions

- Following Playwright test failed.
- Explain why, be concise, respect Playwright best practices.
- Provide a snippet of code with the fix, if possible.

# Test info

- Name: preparation-01/profile.native.spec.ts >> native profile preview native-arm64-bounded-v1 light 390
- Location: ../../../../../../private/tmp/phebs-t4510a-qa-z3azm2qg/preparation-01/profile.native.spec.ts:103:3

# Error details

```
Error: expect(received).toBeGreaterThanOrEqual(expected)

Expected: >= 117
Received:    116.5

Call Log:
- Timeout 5000ms exceeded while waiting on the predicate
```

```
Error: expect(received).toBe(expected) // Object.is equality

Expected: "phebs"
Received: "phebs · Settings"
```

# Page snapshot

```yaml
- generic [ref=e4]:
  - banner [ref=e5]:
    - link "phebs" [ref=e6] [cursor=pointer]:
      - /url: "#/"
    - navigation "Code" [ref=e11]:
      - link "Search" [ref=e12] [cursor=pointer]:
        - /url: "#/"
      - link "Repos" [ref=e13] [cursor=pointer]:
        - /url: "#/repos"
    - generic [ref=e14]:
      - navigation "Instance" [ref=e15]:
        - link "Audit log" [ref=e16] [cursor=pointer]:
          - /url: "#/audit"
        - link "Analytics" [ref=e20] [cursor=pointer]:
          - /url: "#/analytics"
        - link "Settings" [ref=e24] [cursor=pointer]:
          - /url: "#/settings"
      - group "Session and display controls" [ref=e29]:
        - button "Switch to dense rows" [ref=e30] [cursor=pointer]
        - button "Switch to dark theme" [ref=e33] [cursor=pointer]
        - button "Sign out" [ref=e36] [cursor=pointer]
  - main [ref=e40]:
    - generic [ref=e41]:
      - region [ref=e42]:
        - generic [ref=e43]:
          - generic [ref=e44]:
            - heading "Lifecycle maintenance" [level=1] [ref=e45]
            - generic [ref=e46]: Bounded owner turns and allocated-disk pressure. This view never lists source paths or retained content.
          - status [ref=e48]: Collection disabled
        - generic [ref=e49]:
          - region "Pressure" [ref=e50]:
            - heading "Pressure" [level=2] [ref=e51]
            - generic [ref=e52]: collection disabled
            - generic [ref=e56]: Capacity unavailable
            - generic [ref=e57]: Accelerates at 80% · refuses new derived work at 90% · resumes below 75%
          - region "Owner progress" [ref=e58]:
            - heading "Owner progress" [level=2] [ref=e59]
            - generic [ref=e60]: 0 / 0
            - generic [ref=e61]: owners with recorded results
            - generic [ref=e62]: no failed owners
            - generic [ref=e66]: "Per turn: at most 64 candidates, 16 deletions, 16 store queries"
          - region "Backlog" [ref=e67]:
            - heading "Backlog" [level=2] [ref=e68]
            - generic [ref=e69]: "0"
            - generic [ref=e70]: owners carrying backlog into the next turn
      - region [ref=e72]:
        - heading "Code navigation indexing" [level=1] [ref=e73]
        - paragraph [ref=e74]: Generate precise navigation for the repository’s indexed HEAD.
        - button "Refresh indexing" [ref=e76] [cursor=pointer]
        - generic [ref=e77]:
          - article "Bazel" [ref=e78]:
            - generic [ref=e79]:
              - generic [ref=e80]: 01 · Bazel first
              - heading "Bazel" [level=2] [ref=e81]
            - status [ref=e82]: Available
          - article "Go module / workspace" [ref=e83]:
            - generic [ref=e84]:
              - generic [ref=e85]: 02 · Go module / workspace
              - heading "Go module / workspace" [level=2] [ref=e86]
            - status [ref=e87]: Unavailable
          - article "Existing artifact" [ref=e88]:
            - generic [ref=e89]:
              - generic [ref=e90]: 03 · Existing artifact
              - heading "Existing artifact" [level=2] [ref=e91]
            - status [ref=e92]: Unavailable
        - generic [ref=e93]:
          - generic [ref=e94]:
            - text: Repository
            - combobox "Indexing repository" [ref=e95]:
              - option "Choose a repository"
              - option "example.test/neutral" [selected]
          - generic [ref=e96]:
            - generic [ref=e97]: absent
            - paragraph [ref=e98]:
              - text: "Indexed HEAD:"
              - code [ref=e99]: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
          - generic [ref=e100]:
            - text: Target profile
            - combobox "Target profile" [disabled] [ref=e101]:
              - option "neutral-canary" [selected]
          - generic [ref=e102]:
            - text: Configuration profile
            - combobox "Configuration profile" [disabled] [ref=e103]:
              - option "ordinary" [selected]
          - generic [ref=e104]:
            - text: Resource profile
            - combobox "Resource profile" [disabled] [ref=e105]:
              - option "native-arm64-bounded-v1" [selected]
          - generic [ref=e106]:
            - text: Action
            - combobox "Indexing action" [ref=e107]:
              - option "Generate navigation" [selected]
              - option "Canary"
              - option "Dry run"
          - paragraph [ref=e108]: Canary and dry run validate without publishing. Only a complete generation replaces current navigation.
          - button "Review indexing plan" [ref=e109] [cursor=pointer]
          - generic [ref=e110]:
            - paragraph [ref=e111]:
              - text: "Exact commit:"
              - code [ref=e112]: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
            - paragraph [ref=e113]: This preview selects the sealed profile. Native package planning runs after enqueue.
            - group [ref=e114]:
              - generic "Request authority" [ref=e115]
            - button "Generate navigation" [ref=e116] [cursor=pointer]
      - region [ref=e117]:
        - heading "Appearance" [level=1] [ref=e118]
        - generic [ref=e119]: Code highlighting for the viewer, search results, and citations. Every palette meets the AA contrast floor in both themes; the choice is stored in this browser.
        - generic [ref=e120]:
          - generic [ref=e121]:
            - text: Code highlight palette
            - combobox "Code highlight palette" [ref=e122]:
              - option "Phebs" [selected]
              - option "Quiet"
              - option "Classic"
              - option "High contrast"
          - generic [ref=e123]:
            - generic [ref=e124]: // verified span
            - generic [ref=e125]: "func Count(limit int) error {"
            - generic [ref=e126]: return fmt.Errorf("bound=%d", 42)
            - generic [ref=e127]: "}"
      - heading "API keys" [level=1] [ref=e132]
      - generic [ref=e134]:
        - textbox "Key name" [ref=e138]
        - button "Create key" [ref=e139] [cursor=pointer]
      - generic [ref=e140]: No API keys.
```

# Test source

```ts
  1  | import { test as base, expect, chromium, type Page } from "/Users/ben/.codex/worktrees/t512-field-registration/phebs.com/ui/node_modules/@playwright/test/index.mjs"
  2  | import { writeFileSync } from 'node:fs'
  3  | export { expect }
  4  | export type { Page }
  5  | export const test = base.extend({
  6  |   browser: [async ({}, use) => {
  7  |     const browser = await chromium.connectOverCDP(process.env.T4510_CDP!)
  8  |     try { await use(browser) } finally { await browser.close() }
  9  |   }, { scope: 'worker' }],
  10 | })
  11 | const observations = new WeakMap<Page, { errors: string[], refusals: { path: string, status: number }[] }>()
  12 | test.beforeEach(async ({ context, page }) => {
  13 |   await context.addInitScript({ path: process.env.T4510_AXE! })
  14 |   await page.clock.setFixedTime(new Date('2026-08-07T20:00:00Z'))
  15 |   const record = { errors: [] as string[], refusals: [] as { path: string, status: number }[] }
  16 |   observations.set(page, record)
  17 |   page.on('pageerror', error => record.errors.push(error.message))
  18 |   page.on('console', message => { if (message.type() === 'error') record.errors.push(message.text()) })
  19 |   page.on('response', response => { if (response.status() >= 400) record.refusals.push({ path: new URL(response.url()).pathname, status: response.status() }) })
  20 | })
  21 | test.afterEach(async ({ page }, info) => {
  22 |   const report: any = { title: info.title, status: info.status, url: page.url(), pageTitle: await page.title(), ...observations.get(page) }
  23 |   try {
> 24 |     expect(report.pageTitle).toBe('phebs')
     |                              ^ Error: expect(received).toBe(expected) // Object.is equality
  25 |     expect((await page.locator('body').innerText()).length).toBeGreaterThan(50)
  26 |     expect(await page.locator('vite-error-overlay').count()).toBe(0)
  27 |     if (info.title.startsWith('native profile preview')) {
  28 |       expect(report.errors).toEqual([])
  29 |       report.viewport = page.viewportSize()
  30 |       report.audit = await page.evaluate(async () => {
  31 |         const axe = (window as any).axe
  32 |         const result = await axe.run(document, { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa'] } })
  33 |         return { version: axe.version, violations: result.violations, passes: result.passes.length, incomplete: result.incomplete.length }
  34 |       })
  35 |       expect(report.audit.version).toBe('4.12.1')
  36 |       expect(report.audit.violations).toEqual([])
  37 |     }
  38 |   } finally {
  39 |     writeFileSync(info.outputPath('native-qa.json'), JSON.stringify(report, null, 2) + '\n')
  40 |   }
  41 | })
  42 | 
```