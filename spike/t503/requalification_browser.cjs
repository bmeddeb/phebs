// Real local browser only; no route interception or generated payloads.
const fs = require('node:fs');
const path = require('node:path');
const assert = require('node:assert/strict');
const crypto = require('node:crypto');

async function main() {
  const input = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'));
  const { chromium } = require(path.join(input.node_modules, 'playwright'));
  const { expect } = require(path.join(input.node_modules, '@playwright/test'));
  const version = require(path.join(input.node_modules, 'playwright/package.json')).version;
  let browser, context;
  const result = {outcome: 'running', playwright_version: version, browser_path: 'Browser plugin not available; installed Chrome and existing Playwright, without downloads'};
  const errors = [];
  try {
    browser = await chromium.connectOverCDP(input.cdp, {timeout: 5000});
    result.chrome_version = browser.version();
    if (input.preflight) {
      result.outcome = 'passed';
      return;
    }
    context = await browser.newContext({viewport: {width: 1280, height: 900}, extraHTTPHeaders: {Authorization: `Bearer ${input.token}`}});
    const page = await context.newPage();
    page.setDefaultTimeout(5000);
    page.on('pageerror', error => errors.push(String(error)));
    page.on('console', message => { if (message.type() === 'error') errors.push(message.text()); });
    const q = input.query;
    const endpoint = new URLSearchParams({protocol: q.old_protocol, repository: q.old_repository, lineage: q.old_lineage, operation: q.old_operation});
    await page.goto(`${input.base}/#/callers?${endpoint}`, {waitUntil: 'domcontentloaded'});
    await expect(page.getByTestId('caller-map-page')).toBeVisible();
    await page.getByRole('link', {name: 'Compare replacement', exact: true}).click();
    await expect(page.getByRole('heading', {name: 'Select a replacement endpoint', exact: true})).toBeVisible();
    await page.getByRole('textbox', {name: 'Replacement repository', exact: true}).fill(input.repository);
    await page.getByRole('button', {name: 'Search', exact: true}).click();
    const handle = await page.waitForFunction(expected => [...document.querySelectorAll('a')].map(a => a.getAttribute('href')).find(href => {
      if (!href || !href.includes('replacement_operation=')) return false;
      const params = new URLSearchParams(href.split('?')[1]);
      return params.get('replacement_repository') === expected.repository && params.get('replacement_operation') === expected.operation;
    }), {repository: input.repository, operation: q.replacement_operation});
    const target = await handle.jsonValue();
    assert.ok(target, 'actual replacement discovery must expose selected canonical endpoint');
    await page.locator('a[href=' + JSON.stringify(target) + ']').click();
    await expect(page.getByTestId('caller-comparison-page')).toBeVisible();
    await expect(page.getByTestId('caller-comparison-row')).toHaveCount(5);
    assert.equal(new URLSearchParams(page.url().split('?')[1]).get('replacement_repository'), input.repository);
    result.unit_rows = 4; result.occurrence_rows = 5; result.actual_endpoint_selection = true;
    result.unit_classes = {old_only_evidence: 1, both_evidence: 1, new_only_evidence: 1, unresolved: 1};
    result.real_path_filter = {prefix: 'features/multiplex/client/', unit_rows: 1, classification: 'both_evidence', reset_rows: 4};
    fs.mkdirSync(input.screens);
    result.viewports = [];
    for (const width of [1280, 390]) {
      await page.setViewportSize({width, height: 900});
      const rows = page.getByTestId('caller-comparison-row');
      await page.getByLabel('Level', {exact: true}).selectOption('occurrence');
      await page.getByRole('button', {name: 'Apply filters', exact: true}).click();
      await expect(rows).toHaveCount(5);
      for (const [label, count] of [['Old plane only', 2], ['New plane only', 3], ['Both planes', 0], ['Unresolved', 0]]) {
        await expect(rows.filter({has: page.getByText(label, {exact: true})})).toHaveCount(count);
      }
      await page.getByLabel('Level', {exact: true}).selectOption('unit');
      await page.getByRole('button', {name: 'Apply filters', exact: true}).click();
      await expect(rows).toHaveCount(4);
      for (const label of ['Old plane only', 'Both planes', 'New plane only', 'Unresolved']) {
        await expect(rows.filter({has: page.getByText(label, {exact: true})})).toHaveCount(1);
      }
      await page.getByRole('textbox', {name: 'Path prefix', exact: true}).fill('features/multiplex/client/');
      await page.getByRole('button', {name: 'Apply filters', exact: true}).click();
      await expect(rows).toHaveCount(1);
      await expect(rows.getByText('Both planes', {exact: true})).toBeVisible();
      await page.getByRole('textbox', {name: 'Path prefix', exact: true}).fill('');
      await page.getByRole('button', {name: 'Apply filters', exact: true}).click();
      await expect(rows).toHaveCount(4);
      const boundary = page.getByTestId('caller-comparison-page').locator('details').filter({
        has: page.locator('summary', {hasText: /^Repository-overlay static source evidence only/}),
      });
      await expect(boundary).toHaveCount(1);
      if (await boundary.getAttribute('open') === null) {
        const summary = boundary.locator(':scope > summary');
        await summary.focus();
        await expect(summary).toBeFocused();
        await page.keyboard.press('Enter');
      }
      await expect(boundary).toHaveJSProperty('open', true);
      await expect(boundary.locator('p').filter({hasText: /runtime.*completeness/i})).toBeVisible();
      const text = await page.getByTestId('caller-comparison-page').innerText();
      assert.match(text, /runtime/i);
      assert.match(text, /completeness/i);
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth + 1);
      assert.equal(overflow, false, `horizontal overflow at ${width}`);
      const row = page.getByTestId('caller-comparison-row').first();
      const chip = row.getByRole('button', {name: /evaluation\.local\/.*\.go:\d+/}).first();
      if (await chip.getAttribute('aria-expanded') === 'true') {
        await chip.focus();
        await page.keyboard.press('Enter');
        await expect(chip).toHaveAttribute('aria-expanded', 'false');
      }
      await chip.focus();
      await expect(chip).toBeFocused();
      await page.keyboard.press('Enter');
      await expect(chip).toHaveAttribute('aria-expanded', 'true');
      const citation = row.getByTestId('caller-map-exact-citation').first();
      await expect(citation).toBeVisible();
      const content = await citation.locator('pre').innerText();
      assert.ok(input.selectors.includes(content), 'browser citation must show actual frozen selector bytes');
      const screenshot = path.join(input.screens, `comparison-${width}.png`);
      await page.screenshot({path: screenshot, fullPage: true});
      result.viewports.push({width, occurrence_rows: 5, unit_rows: 4, unit_classes: result.unit_classes, real_path_filter: result.real_path_filter, no_horizontal_overflow: true, keyboard_source_opening: true, selector: content, screenshot, screenshot_sha256: crypto.createHash('sha256').update(fs.readFileSync(screenshot)).digest('hex')});
    }
    assert.deepEqual(errors, [], 'unexpected browser console/page errors');
    result.console_errors = errors;
    result.outcome = 'passed';
  } finally {
    if (context) await context.close();
    if (browser) await browser.close();
    fs.writeFileSync(input.out, JSON.stringify(result, null, 2) + '\n');
  }
}
main().catch(error => { process.stderr.write(String(error) + '\n'); process.exitCode = 1; });
