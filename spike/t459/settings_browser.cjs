'use strict'

// Functional composition, using unchanged UI assets and real TLS/auth/API/store.
// Publication is fixture-authored; this is not a native-generation or pixel gate.
const assert = require('node:assert/strict')
const { spawn } = require('node:child_process')
const fs = require('node:fs')
const net = require('node:net')
const path = require('node:path')
const readline = require('node:readline')
const { once } = require('node:events')
const [dependencies, chrome, sshConfig, fixture, assets, receipt] = process.argv.slice(2)
for (const value of [dependencies, chrome, sshConfig, fixture, assets, receipt]) assert(value && path.isAbsolute(value), 'explicit absolute paths required')
// ssh assembles a remote shell command: the two remote paths are deliberately closed.
for (const value of [fixture, assets]) assert(/^\/[A-Za-z0-9/_.-]+$/.test(value), 'unsafe remote fixture path')
const { chromium, expect } = require(path.join(dependencies, '@playwright/test'))
const sshArgs = ['-F', sshConfig, '-o', 'BatchMode=yes', '-o', 'ControlMaster=no', '-o', 'ControlPath=none', 'colima-phebs-t451a']
const child = spawn('ssh', [...sshArgs, `env PATH=${path.dirname(fixture)}:/usr/bin:/bin ${fixture} -test.run '^TestTypedSettingsBrowserLinux$' -test.v -test.timeout 10m -typed-settings-browser-ui ${assets}`], { stdio: ['pipe', 'pipe', 'pipe'] })
let tunnel, browser, ready, phase = 'fixture startup', outputBytes = 0, lineBytes = 0, diagnostics = ''
let fatal
const statusReads = []
const browserDiagnostics = []
const frames = []
let wake
const exited = once(child, 'exit').catch(() => [-1])
const lines = readline.createInterface({ input: child.stdout })
child.stdout.on('data', bytes => {
  outputBytes += bytes.length
  const last = bytes.lastIndexOf(10)
  lineBytes = last < 0 ? lineBytes + bytes.length : bytes.length - last - 1
  if (outputBytes > 524288 || lineBytes > 65536) { fatal ??= new Error('fixture output bound'); child.stdin.end(); wake?.() }
})
child.stderr.on('data', bytes => { diagnostics = (diagnostics + bytes.toString()).slice(-65536); outputBytes += bytes.length; if (outputBytes > 524288) { fatal ??= new Error('fixture output bound'); child.stdin.end(); wake?.() } })
lines.on('line', line => {
  if (!line.startsWith('PHEBS_SETTINGS ')) { diagnostics = (diagnostics + '\n' + line).slice(-65536); return }
  try {
    assert(line.length <= 65536 && frames.length < 32, 'fixture frame bound')
    frames.push(JSON.parse(line.slice(15)))
  } catch { fatal ??= new Error('invalid fixture frame') }
  wake?.()
})
child.on('error', () => { fatal ??= new Error('fixture launch failed'); wake?.() })
child.stdin.on('error', () => { fatal ??= new Error('fixture pipe failed'); wake?.() })
child.on('exit', code => { if (code !== 0) fatal ??= new Error('fixture failed'); wake?.() })
async function frame() {
  if (!frames.length && !fatal) await new Promise((resolve, reject) => {
    const timer = setTimeout(() => { wake = undefined; reject(new Error('fixture frame timeout')) }, 180000)
    wake = () => { clearTimeout(timer); wake = undefined; resolve() }
  })
  if (fatal) throw fatal
  assert(frames.length, 'fixture exited without frame')
  return frames.shift()
}
function send(command) { child.stdin.write(JSON.stringify({ command }) + '\n') }
async function command(name) {
  send(name)
  const next = await frame()
  assert.equal(next.event, 'done'); assert.equal(next.command, name)
  return next
}
async function unusedPort() {
  const listener = net.createServer()
  listener.listen(0, '127.0.0.1'); await once(listener, 'listening')
  const port = listener.address().port
  await new Promise(resolve => listener.close(resolve))
  return port
}
async function join(proc) {
  if (!proc || !proc.pid || proc.exitCode !== null || proc.signalCode !== null) return
  const done = once(proc, 'exit')
  proc.kill('SIGTERM')
  const timer = setTimeout(() => proc.kill('SIGKILL'), 10000)
  try { await done } finally { clearTimeout(timer) }
}
const results = { schema: 'phebs-t459-settings-browser-v1', native_execution: false, canonical_pixel_comparison: false, checks: [], states: [], viewports: [], managed_request_counts: [] }
const check = name => { results.checks.push(name); phase = name }
const allowedStates = ['absent', 'planning', 'indexing', 'validating', 'publishing', 'current', 'failed', 'canceled', 'stale']
let timer
async function run() {
  ready = await frame(); assert.equal(ready.event, 'ready')
  for (const key of ['port', 'darkPort']) assert(Number.isInteger(ready[key]) && ready[key] > 0 && ready[key] < 65536)
  const port = await unusedPort(), darkPort = await unusedPort()
  tunnel = spawn('ssh', [...sshArgs.slice(0, -1), '-o', 'ExitOnForwardFailure=yes', '-N', '-L', `${port}:127.0.0.1:${ready.port}`, '-L', `${darkPort}:127.0.0.1:${ready.darkPort}`, 'colima-phebs-t451a'], { stdio: 'ignore' })
  tunnel.on('error', () => { fatal ??= new Error('fixture tunnel failed'); child.stdin.end(); wake?.() })
  const origin = `https://127.0.0.1:${port}`, darkOrigin = `https://127.0.0.1:${darkPort}`
  browser = await chromium.launch({ executablePath: chrome, headless: true })
  results.browser = browser.version()
  let unexpectedBrowserErrors = 0
  let expectedNetworkErrors = 0
  const settings = '/#/settings?' + new URLSearchParams({ repo: ready.repo, section: 'code-navigation-indexing' })
  const file = '/#/file?' + new URLSearchParams({ repo: ready.repo, path: ready.sourcePath, ref: ready.commit })
  const anonymous = await browser.newContext({ ignoreHTTPSErrors: true })
  await expect.poll(async () => { try { return (await anonymous.request.get(origin + '/api/auth/status')).status() } catch { return 0 } }, { timeout: 10000 }).toBe(200)
  assert.equal((await anonymous.request.get(origin + '/api/code-navigation-indexing/providers')).status(), 401)
  await anonymous.close(); check('anonymous boundary')
  async function login(email, url = origin) {
    const context = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 900 }, reducedMotion: 'reduce' })
    const managedReads = []
    const census = { context: email === ready.ordinaryEmail ? 'ordinary' : url === darkOrigin ? 'dark_admin' : 'admin', requests: 0 }
    results.managed_request_counts.push(census)
    context.on('request', request => {
      if (!request.url().includes('/api/code-navigation-indexing')) return
      // The complete state/appearance flow needs about 66 requests; allow one
      // fixed 128-event envelope including active polling, never an open list.
      if (managedReads.length >= 128) { fatal ??= new Error('managed request event bound'); child.stdin.end(); wake?.(); return }
      managedReads.push(new URL(request.url()).pathname)
      census.requests++
    })
    const page = await context.newPage()
    page.on('pageerror', error => {
      unexpectedBrowserErrors++
      if (browserDiagnostics.length < 16) browserDiagnostics.push({ kind: 'pageerror', message: error.message.slice(0, 160) })
    })
    page.on('console', message => {
      if (message.type() !== 'error') return
      let pathname
      try { pathname = new URL(message.location().url || url).pathname } catch { unexpectedBrowserErrors++; return }
      const deliberateHTTP = /server responded with a status of (403|409|503) /.test(message.text()) && /^\/api\/code-navigation-indexing\/(providers|plan|enqueue)$/.test(pathname)
      const deliberateDrop = message.text().includes('net::ERR_FAILED') && /^\/api\/code-navigation-indexing\/(enqueue|status)$/.test(pathname)
      if (deliberateHTTP || deliberateDrop) expectedNetworkErrors++
      else {
        unexpectedBrowserErrors++
        if (browserDiagnostics.length < 16) browserDiagnostics.push({ kind: 'console', pathname: pathname.slice(0, 160), message: message.text().slice(0, 160) })
      }
    })
    await page.goto(url + settings)
    await page.getByLabel('Email', { exact: true }).fill(email)
    await page.getByLabel('Password', { exact: true }).fill(ready.password)
    await page.getByRole('button', { name: 'Sign in', exact: true }).click()
    await expect(page.getByRole('heading', { name: 'Appearance', exact: true })).toBeVisible()
    const cookie = (await context.cookies()).find(cookie => cookie.name === 'phebs_session')
    assert(cookie?.secure && cookie.httpOnly && cookie.sameSite === 'Lax', 'real secure session cookie required')
    return { context, page, managedReads }
  }
  async function fetchJSON(page, pathname, body, token = 'real') {
    return page.evaluate(async ({ pathname, body, token }) => {
      const headers = { 'Content-Type': 'application/json' }
      if (body !== undefined && token !== '') headers['X-CSRF-Token'] = token === 'real' ? (await (await fetch('/api/auth/status')).json()).csrf_token : token
      const response = await fetch(pathname, { credentials: 'same-origin', ...(body === undefined ? {} : { method: 'POST', headers, body: JSON.stringify(body) }) })
      const text = await response.text()
      if (text.length > 32768) throw new Error('response bound')
      return { status: response.status, body: text ? JSON.parse(text) : null }
    }, { pathname, body, token })
  }
  const ordinary = await login(ready.ordinaryEmail)
  await expect(ordinary.page.getByRole('region', { name: 'Code navigation indexing' })).toHaveCount(0)
  assert.deepEqual(ordinary.managedReads, [])
  assert.equal((await fetchJSON(ordinary.page, '/api/code-navigation-indexing/providers')).status, 403)
  const refusedSelection = { repository: ready.repo, expected_revision: 'sha256:' + 'a'.repeat(64), provider: 'bazel-rules-go-scip-v1', profile: 'reader', purpose: 'publish' }
  const refusedEnqueue = { ...refusedSelection, request_digest: 'sha256:' + 'b'.repeat(64), idempotency_key: 'c'.repeat(64) }
  for (const pathname of ['/plan', '/enqueue']) assert.equal((await fetchJSON(ordinary.page, '/api/code-navigation-indexing' + pathname, pathname === '/plan' ? refusedSelection : refusedEnqueue)).status, 403)
  await ordinary.page.goto(origin + file)
  await ordinary.page.locator('.cm-line').first().click()
  await expect(ordinary.page.getByText('SCIP data is not available for this revision.')).toBeVisible()
  await expect(ordinary.page.getByRole('link', { name: 'Open code navigation indexing' })).toHaveCount(0)
  await ordinary.context.close(); check('ordinary session and File boundary')
  const dark = await login(ready.adminEmail, darkOrigin)
  const darkSection = dark.page.getByRole('region', { name: 'Code navigation indexing' })
  await expect(darkSection.getByText('Managed generation is not registered in this build. Committed SCIP artifacts remain the code-navigation source.')).toBeVisible()
  await expect(darkSection.getByRole('button', { name: 'Review indexing plan' })).toHaveCount(0)
  assert.equal((await fetchJSON(dark.page, '/api/code-navigation-indexing/plan', refusedSelection)).status, 503)
  await dark.context.close(); check('real dark handler boundary')
  const admin = await login(ready.adminEmail), page = admin.page
  const mutationBodies = [], plans = []
  page.on('request', request => {
    if (request.url().endsWith('/api/code-navigation-indexing/enqueue')) {
      if (mutationBodies.length >= 8) { fatal ??= new Error('mutation event bound'); child.stdin.end(); wake?.(); return }
      mutationBodies.push(request.postData())
    }
  })
  page.on('response', async response => {
    if (response.url().includes('/api/code-navigation-indexing/status?')) {
      try {
        const view = await response.json()
        statusReads.push({ status: response.status(), state: view.state, available: view.available, reason: view.reason })
        if (statusReads.length > 16) statusReads.shift()
      } catch {
        // UI generation changes cancel obsolete reads. A diagnostic clone
        // losing its body is not an application console/page error.
        statusReads.push({ status: response.status(), diagnostic: 'body_unavailable' })
        if (statusReads.length > 16) statusReads.shift()
      }
    }
    if (response.url().endsWith('/api/code-navigation-indexing/plan') && response.ok()) {
      try { assert(plans.length < 16); plans.push(await response.json()) } catch { unexpectedBrowserErrors++ }
    }
  })
  const section = page.getByRole('region', { name: 'Code navigation indexing' })
  async function state(value, refresh = true, available) {
    const updated = refresh ? page.waitForResponse(async response => {
      if (!response.url().includes('/api/code-navigation-indexing/status?') || response.status() !== 200) return false
      try {
        const view = await response.json()
        return view.state === value && (available === undefined || view.available === available)
      } catch { return false }
    }, { timeout: 15000 }) : undefined
    if (refresh) await Promise.all([updated, section.getByRole('button', { name: 'Refresh indexing' }).click()])
    await expect(section.getByText(value, { exact: true })).toBeVisible({ timeout: 15000 })
    if (!results.states.includes(value)) results.states.push(value)
  }
  await state('absent', false)
  assert.deepEqual(await section.getByRole('article').evaluateAll(nodes => nodes.map(n => n.getAttribute('data-provider-id'))), ['bazel-rules-go-scip-v1', 'go-module-scip-v1', 'imported-artifact-scip-v1'])
  await expect(section.getByText(ready.commit, { exact: true })).toBeVisible()
  await page.goto(origin + file)
  await page.locator('.cm-line').first().click()
  await page.getByRole('link', { name: 'Open code navigation indexing' }).click()
  await expect(section.getByRole('combobox', { name: 'Indexing repository' })).toHaveValue(ready.repo)
  await expect(page.getByRole('heading', { name: 'Code navigation indexing', exact: true })).toBeFocused()
  check('administrator File deep link and provider order')
  for (const purpose of ['canary', 'dry-run', 'publish']) {
    await section.getByRole('combobox', { name: 'Indexing action' }).selectOption(purpose)
    await section.getByRole('button', { name: 'Review indexing plan' }).click()
    await expect(section.getByText('Exact commit:', { exact: false })).toBeVisible()
    await expect.poll(() => plans.at(-1)?.selection.purpose).toBe(purpose)
  }
  const preview = plans.at(-1)
  const enqueue = { ...preview.selection, request_digest: preview.request_digest, idempotency_key: preview.idempotency_key }
  for (const pathname of ['/plan', '/enqueue']) for (const token of ['', 'wrong']) {
    assert.equal((await fetchJSON(page, '/api/code-navigation-indexing' + pathname, pathname === '/plan' ? preview.selection : enqueue, token)).status, 403)
  }
  await command('preview'); assert.equal(mutationBodies.length, 2) // the two deliberately refused enqueue attempts
  mutationBodies.length = 0; check('three pure previews and real CSRF refusals')
  // Every transported response is real; this one is discarded after admission.
  await page.route('**/api/code-navigation-indexing/enqueue', async route => {
    await route.fetch(); await route.abort('failed')
    await page.unroute('**/api/code-navigation-indexing/enqueue')
  }, { times: 1 })
  await section.getByRole('button', { name: 'Generate navigation', exact: true }).click()
  await expect(section.getByRole('button', { name: 'Retry exact request' })).toBeVisible()
  const admitted = await command('queued')
  assert.equal(admitted.requestDigest, preview.request_digest)
  await section.getByRole('button', { name: 'Retry exact request' }).click()
  await state('planning', false)
  assert.equal(mutationBodies.length, 2); assert.equal(mutationBodies[0], mutationBodies[1])
  const retried = await command('queued')
  assert.equal(retried.requestDigest, admitted.requestDigest)
  assert.deepEqual(JSON.parse(mutationBodies[0]), enqueue)
  check('lost admitted response and exact retry')
  send('publish')
  let publication
  for (;;) {
    const next = await frame()
    if (next.event === 'done') { assert.equal(next.command, 'publish'); publication = next; break }
    assert.equal(next.event, 'stage'); assert(['planning', 'indexing', 'validating', 'publishing'].includes(next.state))
    await state(next.state)
    if (next.state === 'indexing') {
      await page.route('**/api/code-navigation-indexing/status?*', async route => { await route.fetch(); await route.abort('failed') }, { times: 1 })
      await expect(section.getByRole('alert')).toContainText('Last confirmed state is retained', { timeout: 8000 })
      await expect(section.getByText('indexing', { exact: true })).toBeVisible()
      await expect(section.getByRole('alert')).toHaveCount(0, { timeout: 8000 })
      check('last-good active polling and recovery')
    }
    send('continue')
  }
  await state('current')
  assert.equal(publication.parentDigest, preview.request_digest)
  function position(doc, line, character) { return '?' + new URLSearchParams({ repo: ready.repo, path: doc, ref: ready.commit, line: String(line), character: String(character), encoding: 'utf16' }) }
  const definition = await fetchJSON(page, '/api/find_definitions' + position(publication.referencePath, 1, 0))
  const references = await fetchJSON(page, '/api/find_references' + position(publication.definitionPath, 0, 2))
  const hover = await fetchJSON(page, '/api/hover' + position(publication.referencePath, 1, 0))
  assert.equal(definition.status, 200); assert.equal(definition.body.available, true); assert.equal(definition.body.location.path, publication.definitionPath); assert.equal(definition.body.location.range.start.character, 2)
  assert.equal(references.status, 200); assert.equal(references.body.available, true); assert.equal(references.body.locations.length, 1); assert.equal(references.body.locations[0].path, publication.referencePath); assert.equal(references.body.locations[0].range.start.line, 1); assert.equal(references.body.locations[0].range.start.character, 0)
  assert.equal(hover.status, 200); assert.deepEqual(hover.body.hover.documentation, ['generated'])
  check('real routed cross-member definition references hover')
  await section.getByRole('combobox', { name: 'Indexing action' }).selectOption('canary')
  const beforeCanary = plans.length
  await section.getByRole('button', { name: 'Review indexing plan' }).click()
  await expect(section.getByRole('button', { name: 'Run canary', exact: true })).toBeEnabled()
  await expect.poll(() => plans.length).toBe(beforeCanary + 1)
  await expect.poll(() => plans.at(-1)?.selection.purpose).toBe('canary')
  const failedPreview = plans.at(-1)
  await section.getByRole('button', { name: 'Run canary', exact: true }).click()
  await state('planning', false); await command('failed'); await state('failed')
  const beforeRerun = plans.length
  await section.getByRole('button', { name: 'Review indexing plan' }).click()
  await expect.poll(() => plans.length).toBe(beforeRerun + 1)
  await expect.poll(() => plans.at(-1)?.request_digest).not.toBe(failedPreview.request_digest)
  assert.notEqual(plans.at(-1).idempotency_key, failedPreview.idempotency_key)
  await expect(section.getByRole('button', { name: 'Run canary', exact: true })).toBeEnabled()
  await section.getByRole('combobox', { name: 'Indexing action' }).selectOption('dry-run')
  await section.getByRole('button', { name: 'Review indexing plan' }).click()
  await expect(section.getByRole('button', { name: 'Run dry run', exact: true })).toBeEnabled()
  await section.getByRole('button', { name: 'Run dry run', exact: true }).click()
  await state('planning', false); await command('canceled'); await state('canceled')
  assert(!await section.innerText().then(text => text.includes('/unretained/'))); check('failed re-run preview and canceled coordinator')
  for (const width of [1280, 390]) for (const theme of ['light', 'dark']) for (const density of ['comfortable', 'dense']) {
    await page.setViewportSize({ width, height: 900 })
    await page.evaluate(({ theme, density }) => { localStorage.setItem('phebs-theme', theme); localStorage.setItem('phebs-density', density) }, { theme, density })
    await page.reload()
    await expect(page.getByRole('heading', { name: 'Code navigation indexing', exact: true })).toBeFocused()
    await expect.poll(() => page.evaluate(() => document.documentElement.style.colorScheme)).toBe(theme)
    await expect(page.getByRole('button', { name: density === 'dense' ? 'Switch to comfortable rows' : 'Switch to dense rows', exact: true })).toBeVisible()
    await page.keyboard.press('Tab'); await expect(section.getByRole('button', { name: 'Refresh indexing' })).toBeFocused()
    await page.keyboard.press('Tab'); await expect(section.getByRole('combobox', { name: 'Indexing repository' })).toBeFocused()
    await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    assert(await page.evaluate(() => matchMedia('(prefers-reduced-motion: reduce)').matches))
    assert(await section.locator('select').evaluateAll(nodes => nodes.every(node => !!node.labels?.length)))
    results.viewports.push({ width, theme, density })
  }
  check('eight functional responsive keyboard motion appearances')
  await command('stale'); await state('stale')
  assert.equal((await fetchJSON(page, '/api/code-navigation-indexing/enqueue', enqueue)).status, 409)
  assert.equal((await fetchJSON(page, '/api/find_definitions' + position(publication.referencePath, 1, 0))).body.available, false)
  await command('restore')
  const restored = await fetchJSON(page, '/api/code-navigation-indexing/status?' + new URLSearchParams({ repository: ready.repo }))
  assert.equal(restored.status, 200); assert.equal(restored.body.available, false)
  await state('stale', true, false)
  await expect(section.getByText('No installed profile is available for this repository.')).toBeVisible({ timeout: 15000 })
  await expect(section.getByRole('button', { name: 'Review indexing plan' })).toHaveCount(0)
  assert.equal((await fetchJSON(page, '/api/find_definitions' + position(publication.referencePath, 1, 0))).body.available, false)
  check('source fence and restore clearance')
  assert.deepEqual([...results.states].sort(), [...allowedStates].sort())
  await admin.context.close(); await browser.close(); browser = undefined
  assert.equal(unexpectedBrowserErrors, 0)
  assert.equal(expectedNetworkErrors, 11) // three ordinary, one dark, four CSRF, one stale, two dropped responses
  phase = 'fixture teardown'; const finished = await command('finish'); child.stdin.end()
  const [code] = await exited; assert.equal(code, 0)
  await join(tunnel); tunnel = undefined
  results.fixture = { repo: ready.repo, commit: ready.commit, ...publication }
  results.audit_enqueues = finished.auditEnqueues
  results.unexpected_browser_errors = unexpectedBrowserErrors
  results.deliberate_network_console_errors = expectedNetworkErrors
  results.joined = true
  fs.writeFileSync(receipt, JSON.stringify(results, null, 2) + '\n', { flag: 'wx', mode: 0o600 })
  console.log(JSON.stringify({ result: 'pass', checks: results.checks.length, states: results.states.length, appearances: results.viewports.length }))
}
timer = setTimeout(() => { fatal ??= new Error('driver timeout'); child.stdin.end(); wake?.() }, 600000)
run().catch(error => {
  let recorded = diagnostics + '\nRecent status: ' + JSON.stringify(statusReads) + '\nRequest counts: ' + JSON.stringify(results.managed_request_counts) + '\nBrowser errors: ' + JSON.stringify(browserDiagnostics)
  let summary = `Settings browser failed during ${phase}: ${fatal?.message || error.message}`
  for (const secret of [ready?.password, ready?.adminEmail, ready?.ordinaryEmail]) if (secret) {
    recorded = recorded.replaceAll(secret, '[redacted]')
    summary = summary.replaceAll(secret, '[redacted]')
  }
  fs.writeFileSync(receipt + '.failure.txt', recorded, { flag: 'wx', mode: 0o600 })
  console.error(summary.slice(0, 1024)); process.exitCode = 1
}).finally(async () => {
  clearTimeout(timer); lines.close(); child.stdin.end()
  if (browser) await browser.close()
  // EOF gives the owned test time to close TLS, join auth, and close its engine.
  if (child.exitCode === null && child.signalCode === null) {
    let grace
    await Promise.race([exited, new Promise(resolve => { grace = setTimeout(resolve, 20000) })])
    clearTimeout(grace)
  }
  await join(child); await join(tunnel)
})
