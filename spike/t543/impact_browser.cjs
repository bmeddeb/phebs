// Actual local signed-source UI only. No intercepted routes or invented output.
const fs = require('node:fs');
const path = require('node:path');
const assert = require('node:assert/strict');
const crypto = require('node:crypto');

async function main() {
  const input = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'));
  const {chromium} = require(path.join(input.node_modules, 'playwright'));
  const {expect} = require(path.join(input.node_modules, '@playwright/test'));
  let browser, context, page;
  const result = {outcome: 'running', response_interception: false, truth_samples_added: 0, viewports: []};
  const errors = [];
  async function disclose(label) {
    const summary = page.locator('summary').filter({hasText: label});
    await expect(summary).toHaveCount(1);
    const detail = summary.locator('..');
    if (await detail.getAttribute('open') === null) {
      await summary.focus(); await expect(summary).toBeFocused(); await page.keyboard.press('Enter');
    }
    await expect(detail).toHaveJSProperty('open', true);
    return detail;
  }
  try {
    browser = await chromium.connectOverCDP(input.cdp, {timeout: 5000});
    result.chrome_version = browser.version();
    result.playwright_version = require(path.join(input.node_modules, 'playwright/package.json')).version;
    context = await browser.newContext({viewport: {width: 1280, height: 900}, colorScheme: 'light', reducedMotion: 'reduce', locale: 'en-US', timezoneId: 'UTC', extraHTTPHeaders: {Authorization: `Bearer ${input.token}`}});
    page = await context.newPage(); page.setDefaultTimeout(5000);
    page.on('pageerror', error => errors.push(String(error)));
    page.on('console', message => {if (message.type() === 'error') errors.push(message.text());});
    fs.mkdirSync(input.screens);
    const gold = input.query.query.endpoint;
    for (const width of [1280, 390]) {
      await page.setViewportSize({width, height: 900});
      const endpoint = new URLSearchParams({protocol: gold.protocol, repository: gold.repository, lineage: gold.declaration_lineage, operation: gold.operation});
      await page.goto(`${input.base}/#/callers?${endpoint}`, {waitUntil: 'domcontentloaded'});
      await page.getByRole('link', {name: 'Inspect operation impact', exact: true}).click();
      await expect(page.getByTestId('caller-map-page')).toBeVisible();
      await expect(page.getByRole('heading', {name: gold.operation, exact: true})).toBeVisible();
      const params = new URLSearchParams(page.url().split('?')[1]);
      assert.equal(params.get('shape'), 'operation_callers');
      assert.equal(params.get('lineage'), gold.declaration_lineage);
      // Caller Map's actual UI uses100. Preserve that real entry, then use
      // the report's declared URL page_size input for the paging journey.
      assert.equal(params.get('page_size'), '100');
      params.set('page_size', '1'); params.delete('cursor');
      await page.goto(`${input.base}/#/impact?${params}`, {waitUntil: 'domcontentloaded'});
      const rows = page.getByTestId('caller-map-row');
      await expect(rows).toHaveCount(1);
      await expect(page.getByText('Rows 1–1 of 3', {exact: true})).toBeVisible();
      await expect(rows.getByText('resolved', {exact: true})).toBeVisible();
      await expect(rows.getByText(/^(syntax|scip)$/)).toHaveCount(1);
      await expect(rows.getByText(/^(heuristic|derived)$/)).toHaveCount(1);
      await page.getByRole('button', {name: 'Next page', exact: true}).click();
      await expect(page.getByText('Rows 2–2 of 3', {exact: true})).toBeVisible();
      await expect(rows.getByText('unavailable', {exact: true})).toBeVisible();
      await expect(rows.getByText('mapping has no attribution', {exact: true})).toBeVisible();
      const chip = rows.getByRole('button', {name: /evaluation\.local\/.*\.go:\d+/}).first();
      await chip.focus(); await expect(chip).toBeFocused(); await page.keyboard.press('Enter');
      await expect(chip).toHaveAttribute('aria-expanded', 'true');
      const citation = rows.getByTestId('caller-map-exact-citation');
      await expect(citation).toBeVisible();
      assert.equal(await citation.locator('pre').innerText(), 'c.UnaryEcho');
      await page.getByRole('textbox', {name: 'Path prefix', exact: true}).fill('features/multiplex/client/');
      await page.getByRole('button', {name: 'Apply filters', exact: true}).click();
      await expect(page.getByText('Rows 1–1 of 1', {exact: true})).toBeVisible();
      await expect(rows.getByText('resolved', {exact: true})).toBeVisible();
      await page.getByRole('textbox', {name: 'Path prefix', exact: true}).fill('');
      await page.getByRole('button', {name: 'Apply filters', exact: true}).click();
      await expect(page.getByText('Rows 1–1 of 3', {exact: true})).toBeVisible();
      await page.getByRole('button', {name: 'Unit view', exact: true}).click();
      await expect(page.getByLabel('Caller rows grouped by attributed unit', {exact: true})).toBeVisible();
      await expect(rows).toHaveCount(1);
      // A URL view change performs an ordinary refetch and remounts the
      // disclosures. Wait for that response before opening the new tree.
      const sourceViewResponse = page.waitForResponse(response => new URL(response.url()).pathname === '/api/operation_impact_report' && response.request().method() === 'GET' && response.status() === 200);
      await page.getByRole('button', {name: 'Source view', exact: true}).click();
      await (await sourceViewResponse).finished();
      await expect(page.getByRole('button', {name: 'Source view', exact: true})).toHaveAttribute('aria-pressed', 'true');
      await expect(page.getByRole('status', {name: 'Loading operation impact', exact: true})).toHaveCount(0);
      await expect(rows).toHaveCount(1);
      const authority = await disclose(/^Source authority and scope$/);
      await expect(authority.getByText('Exact repository-overlay generation', {exact: true})).toBeVisible();
      const unavailable = await disclose(/^Other report sections · unavailable$/);
      for (const name of input.sections) await expect(unavailable.getByText(`${name.replaceAll('_', ' ')}: not in this supported report shape.`, {exact: false})).toBeVisible();
      const boundary = await disclose(/^Repository-overlay static source evidence only/);
      await expect(boundary.locator('p').filter({hasText: /runtime.*completeness/i})).toBeVisible();
      const firstChip = rows.getByRole('button', {name: /evaluation\.local\/.*\.go:\d+/}).first();
      await firstChip.focus(); await expect(firstChip).toBeFocused(); await page.keyboard.press('Enter');
      await expect(firstChip).toHaveAttribute('aria-expanded', 'true');
      assert.equal(await rows.getByTestId('caller-map-exact-citation').locator('pre').innerText(), 'client.UnaryEcho');
      await page.evaluate(() => document.fonts.ready);
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1), false, `Impact horizontal overflow at ${width}`);
      assert.equal(await page.locator('vite-error-overlay, nextjs-portal').count(), 0, 'framework error overlay');
      await page.addScriptTag({path: input.axe_source});
      const audit = await page.evaluate(async () => {
        const value = await axe.run(document, {runOnly: {type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']}});
        return {violations: value.violations.map(v => ({id: v.id, impact: v.impact, nodes: v.nodes.map(n => ({target: n.target, failureSummary: n.failureSummary}))})), passes: value.passes.length};
      });
      assert.deepEqual(audit.violations, [], 'Impact WCAG audit failed');
      const screenshot = path.join(input.screens, `operation-impact-${width}.png`);
      await page.screenshot({path: screenshot, fullPage: true});
      result.viewports.push({width, full_query_rows: 3, page_size: 1, actual_entry_link: true, exact_paging: true, source_and_unit_views: true, resolved_and_unavailable_units: true, path_filter_and_clear: true, keyboard_exact_source: true, selected_bytes_sha256: ['c.UnaryEcho', 'client.UnaryEcho'].map(value => crypto.createHash('sha256').update(value).digest('hex')), native_authority_and_unavailable_disclosures: true, no_horizontal_overflow: true, audit, screenshot, screenshot_sha256: crypto.createHash('sha256').update(fs.readFileSync(screenshot)).digest('hex')});
    }
    assert.deepEqual(errors, [], 'unexpected Impact console/page errors');
    result.console_errors = errors;
    result.outcome = 'passed';
  } catch (error) {
    result.reason = String(error);
    result.console_errors = errors;
    if (page) {
      try {await page.screenshot({path: path.join(input.screens, 'stopped.png'), fullPage: true});} catch (_) {}
    }
    throw error;
  } finally {
    if (context) await context.close();
    if (browser) await browser.close();
    fs.writeFileSync(input.out, JSON.stringify(result, null, 2) + '\n');
  }
}
main().catch(error => {process.stderr.write(String(error) + '\n'); process.exitCode = 1;});
