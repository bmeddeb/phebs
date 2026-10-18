const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require('/Users/ben/.codex/worktrees/t422n-mac-native-closure/phebs.com/ui/node_modules/playwright');
const out = process.env.T442_B_OUT;
const result = { method: 'regular cached Playwright with installed headless Chrome', freshContext: false, freshPage: false, mutablePreflight: false, injection: false, userVisibleOverlay: false, console: [], pageErrors: [], requests: [], unexpectedRequests: [] };
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
const assert = (condition, message) => { if (!condition) throw new Error(message); };
(async () => {
  const browser = await chromium.connectOverCDP(process.env.T442_CDP);
  let context;
  try {
    context = await browser.newContext({ viewport: { width: 1280, height: 800 }, colorScheme: 'light', reducedMotion: 'reduce' });
    result.freshContext = true;
    const page = await context.newPage(); result.freshPage = true;
    page.setDefaultTimeout(10000);
    page.on('pageerror', error => result.pageErrors.push(error.message));
    page.on('console', message => result.console.push({ type: message.type(), text: message.text() }));
    await page.route('**/api/**', async route => {
      const request = route.request(); const url = new URL(request.url());
      const key = request.method() + ' ' + url.pathname + url.search;
      result.requests.push(key);
      let value;
      if (key === 'GET /api/auth/status') value = { authenticated: true, auth_required: true, setup_required: false, oidc_enabled: false, password_enabled: true, csrf_token: 'appearance-receipt-csrf', user: { id: 'appearance-reader', email: 'appearance@localhost.test', display_name: 'Receipt reader', is_admin: false } };
      else if (key === 'GET /api/version') value = { version: 'receipt', capabilities: [] };
      else if (key === 'GET /api/auth/keys') value = { keys: [] };
      else { result.unexpectedRequests.push(key); value = { error: 'unexpected appearance fixture request' }; }
      await route.fulfill({ status: result.unexpectedRequests.includes(key) ? 501 : 200, contentType: 'application/json', body: JSON.stringify(value) });
    });
    await page.addInitScript(() => { localStorage.setItem('phebs-theme', 'light'); localStorage.setItem('phebs-density', 'comfortable'); localStorage.setItem('phebs-palette', 'phebs'); });
    await page.goto(process.env.PHEBS_RECEIPTS_URL + '/#/settings');
    await page.locator('[data-palette-ready="true"]').waitFor({ state: 'visible' });
    assert(await page.title() === 'phebs · Settings', 'wrong settings page');
    assert(await page.getByText('No API keys.', { exact: true }).isVisible(), 'fixture not settled');
    assert(result.unexpectedRequests.length === 0, 'unexpected API fixture request');
    result.mutablePreflight = await page.evaluate(() => {
      document.title = '[Human] phebs · Settings';
      const script = document.createElement('script'); script.id = 'impeccable-mutable-preflight'; script.textContent = 'window.__impeccableMutablePreflight = true'; document.head.appendChild(script);
      return document.title === '[Human] phebs · Settings' && document.querySelector('#impeccable-mutable-preflight') !== null && window.__impeccableMutablePreflight === true;
    });
    assert(result.mutablePreflight, 'mutable script preflight failed');
    fs.writeFileSync(path.join(out, 'preflight.json'), JSON.stringify({ passed: true, mutablePreflight: true }));
    const readyDeadline = Date.now() + 20000;
    while (!fs.existsSync(path.join(out, 'live.json'))) { assert(Date.now() < readyDeadline, 'live-server handoff timeout'); await sleep(50); }
    const live = JSON.parse(fs.readFileSync(path.join(out, 'live.json')));
    await page.evaluate(() => scrollTo(0, 0));
    const before = result.console.length;
    await page.addScriptTag({ url: live.detectURL });
    result.injection = await page.evaluate(url => Array.from(document.scripts).some(script => script.src === url), live.detectURL);
    assert(result.injection, 'detect.js script tag absent after injection');
    await sleep(2500);
    result.postInjectionConsole = result.console.slice(before);
    result.impeccableConsole = result.postInjectionConsole.filter(message => /impeccable/i.test(message.text));
    result.overlayDOM = await page.evaluate(() => Array.from(document.querySelectorAll('[id*="impeccable"], [class*="impeccable"], [data-impeccable]')).map(node => ({ tag: node.tagName, id: node.id, className: typeof node.className === 'string' ? node.className : '' })).slice(0,100));
    await page.screenshot({ path: path.join(out, 'headless-injected.png'), fullPage: false });
    result.title = await page.title(); result.url = page.url(); result.outcome = 'passed';
  } catch (error) { result.outcome = 'stopped'; result.error = String(error.stack || error); process.exitCode = 1; }
  finally {
    fs.writeFileSync(path.join(out, 'browser-result.json'), JSON.stringify(result,null,2)+'\n');
    if (context) await context.close();
    await browser.close();
  }
})();
