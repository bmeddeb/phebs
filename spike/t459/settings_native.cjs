'use strict'

// The unchanged Settings UI admits a request; the installed production runtime
// produces the native bytes. This is a functional bridge, not a pixel gate.
const assert = require('node:assert/strict')
const { spawn } = require('node:child_process')
const fs = require('node:fs')
const net = require('node:net')
const path = require('node:path')
const readline = require('node:readline')
const { once } = require('node:events')
const [dependencies, chrome, sshConfig, fixture, assets, installation, configDigest, receipt] = process.argv.slice(2)
for (const value of [dependencies, chrome, sshConfig, fixture, assets, installation, receipt]) assert(value && path.isAbsolute(value), 'explicit absolute paths required')
for (const value of [fixture, assets]) assert(/^\/[A-Za-z0-9/_.-]+$/.test(value), 'unsafe remote fixture path')
assert(/^\/var\/lib\/phebs-typed-acceptance\/[a-z][a-z0-9-]{0,31}$/.test(installation), 'closed neutral installation required')
assert(/^sha256:[a-f0-9]{64}$/.test(configDigest), 'explicit installation digest required')
const { chromium, expect } = require(path.join(dependencies, '@playwright/test'))
const sshArgs = ['-F', sshConfig, '-o', 'BatchMode=yes', '-o', 'ControlMaster=no', '-o', 'ControlPath=none', 'colima-phebs-t451a']
const child = spawn('ssh', [...sshArgs, `sudo -n env PATH=${path.dirname(fixture)}:/usr/bin:/bin TMPDIR=${installation}/tmp ${fixture} -test.run '^TestTypedSettingsNativeLinux$' -test.v -test.timeout 15m -typed-settings-browser-ui ${assets} -typed-settings-native-root ${installation} -typed-settings-native-config-sha256 ${configDigest}`], { stdio: ['pipe', 'pipe', 'pipe'] })
let tunnel, browser, ready, phase = 'fixture startup', outputBytes = 0, lineBytes = 0, diagnostics = '', fatal, wake
const frames = [], browserErrors = []
const exited = once(child, 'exit').catch(() => [-1])
const lines = readline.createInterface({ input: child.stdout })
child.stdout.on('data', bytes => {
  outputBytes += bytes.length
  const last = bytes.lastIndexOf(10)
  lineBytes = last < 0 ? lineBytes + bytes.length : bytes.length - last - 1
  if (outputBytes > 524288 || lineBytes > 65536) { fatal ??= new Error('fixture output bound'); child.stdin.end(); wake?.() }
})
child.stderr.on('data', bytes => {
  diagnostics = (diagnostics + bytes.toString()).slice(-65536); outputBytes += bytes.length
  if (outputBytes > 524288) { fatal ??= new Error('fixture output bound'); child.stdin.end(); wake?.() }
})
lines.on('line', line => {
  if (!line.startsWith('PHEBS_SETTINGS ')) { diagnostics = (diagnostics + '\n' + line).slice(-65536); return }
  try { assert(line.length <= 65536 && frames.length < 32); frames.push(JSON.parse(line.slice(15))) }
  catch { fatal ??= new Error('invalid fixture frame') }
  wake?.()
})
for (const stream of [child, child.stdin]) stream.on('error', () => { fatal ??= new Error('fixture transport failed'); wake?.() })
child.on('exit', code => { if (code !== 0) fatal ??= new Error('fixture failed'); wake?.() })
async function frame() {
  if (!frames.length && !fatal) await new Promise((resolve, reject) => {
    const timeout = setTimeout(() => { wake = undefined; reject(new Error('fixture frame timeout')) }, 360000)
    wake = () => { clearTimeout(timeout); wake = undefined; resolve() }
  })
  if (fatal) throw fatal
  assert(frames.length, 'fixture exited without frame')
  return frames.shift()
}
async function command(command) {
  child.stdin.write(JSON.stringify({ command }) + '\n')
  const next = await frame()
  assert.equal(next.event, 'done'); assert.equal(next.command, command)
  return next
}
async function unusedPort() {
  const listener = net.createServer(); listener.listen(0, '127.0.0.1'); await once(listener, 'listening')
  const port = listener.address().port
  await new Promise(resolve => listener.close(resolve)); return port
}
async function join(proc) {
  if (!proc?.pid || proc.exitCode !== null || proc.signalCode !== null) return
  const done = once(proc, 'exit'); proc.kill('SIGTERM')
  const timeout = setTimeout(() => proc.kill('SIGKILL'), 10000)
  try { await done } finally { clearTimeout(timeout) }
}
const results = { schema: 'phebs-t459-settings-native-v1', t459_acceptance: 'OPEN', native_execution: true, canonical_pixel_comparison: false, checks: [] }
const check = name => { results.checks.push(name); phase = name }
async function run() {
  ready = await frame(); assert.equal(ready.event, 'ready')
  assert(Number.isInteger(ready.port) && ready.port > 0 && ready.port < 65536)
  const port = await unusedPort()
  tunnel = spawn('ssh', [...sshArgs.slice(0, -1), '-o', 'ExitOnForwardFailure=yes', '-N', '-L', `${port}:127.0.0.1:${ready.port}`, 'colima-phebs-t451a'], { stdio: 'ignore' })
  tunnel.on('error', () => { fatal ??= new Error('fixture tunnel failed'); child.stdin.end(); wake?.() })
  const origin = `https://127.0.0.1:${port}`
  browser = await chromium.launch({ executablePath: chrome, headless: true })
  results.browser = browser.version()
  const anonymous = await browser.newContext({ ignoreHTTPSErrors: true })
  await expect.poll(async () => { try { return (await anonymous.request.get(origin + '/api/auth/status')).status() } catch { return 0 } }, { timeout: 10000 }).toBe(200)
  assert.equal((await anonymous.request.get(origin + '/api/code-navigation-indexing/providers')).status(), 401)
  await anonymous.close()
  let unexpected = 0, deliberate = 0
  async function login(email) {
    const context = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 900 }, reducedMotion: 'reduce' })
    const page = await context.newPage()
    page.on('pageerror', error => { unexpected++; if (browserErrors.length < 16) browserErrors.push(error.message.slice(0, 160)) })
    page.on('console', message => {
      if (message.type() !== 'error') return
      let pathname
      try { pathname = new URL(message.location().url || origin).pathname } catch { unexpected++; return }
      if ((/server responded with a status of (403|409) /.test(message.text()) && /^\/api\/code-navigation-indexing\/(providers|plan|enqueue)$/.test(pathname)) || (message.text().includes('net::ERR_FAILED') && pathname === '/api/code-navigation-indexing/enqueue')) deliberate++
      else { unexpected++; if (browserErrors.length < 16) browserErrors.push(message.text().slice(0, 160)) }
    })
    await page.goto(origin + '/#/settings?' + new URLSearchParams({ repo: ready.repo, section: 'code-navigation-indexing' }))
    await page.getByLabel('Email', { exact: true }).fill(email)
    await page.getByLabel('Password', { exact: true }).fill(ready.password)
    await page.getByRole('button', { name: 'Sign in', exact: true }).click()
    await expect(page.getByRole('heading', { name: 'Appearance', exact: true })).toBeVisible()
    const cookie = (await context.cookies()).find(cookie => cookie.name === 'phebs_session')
    assert(cookie?.secure && cookie.httpOnly && cookie.sameSite === 'Lax')
    return { context, page }
  }
  async function json(page, pathname, body, token = 'real') {
    return page.evaluate(async ({ pathname, body, token }) => {
      const headers = { 'Content-Type': 'application/json' }
      if (body !== undefined && token !== '') headers['X-CSRF-Token'] = token === 'real' ? (await (await fetch('/api/auth/status')).json()).csrf_token : token
      const response = await fetch(pathname, { credentials: 'same-origin', ...(body === undefined ? {} : { method: 'POST', headers, body: JSON.stringify(body) }) })
      const text = await response.text(); if (text.length > 32768) throw new Error('response bound')
      return { status: response.status, body: text ? JSON.parse(text) : null }
    }, { pathname, body, token })
  }
  const ordinary = await login(ready.ordinaryEmail)
  await expect(ordinary.page.getByRole('region', { name: 'Code navigation indexing' })).toHaveCount(0)
  assert.equal((await json(ordinary.page, '/api/code-navigation-indexing/providers')).status, 403)
  await ordinary.context.close(); check('real anonymous and ordinary session refusal')
  const admin = await login(ready.adminEmail), page = admin.page
  const section = page.getByRole('region', { name: 'Code navigation indexing' })
  let requests = 0
  const mutations = [], previews = []
  page.on('request', request => {
    if (!request.url().includes('/api/code-navigation-indexing')) return
    if (++requests > 128) { fatal ??= new Error('managed request bound'); child.stdin.end(); wake?.(); return }
    if (request.url().endsWith('/enqueue')) {
      const body = request.postData()
      if (mutations.length >= 8 || !body || Buffer.byteLength(body) > 4096) { fatal ??= new Error('mutation event bound'); child.stdin.end(); wake?.(); return }
      mutations.push(body)
    }
  })
  page.on('response', async response => {
    if (!response.url().endsWith('/api/code-navigation-indexing/plan') || !response.ok()) return
    try { const body = await response.body(); assert(previews.length < 4 && body.length <= 32768); previews.push(JSON.parse(body)) } catch { unexpected++ }
  })
  await expect(section.getByText('absent', { exact: true })).toBeVisible()
  await expect(section.locator(`[data-provider-id="${ready.provider}"]`)).toBeVisible()
  await section.getByRole('combobox', { name: 'Indexing action' }).selectOption('publish')
  await section.getByRole('button', { name: 'Review indexing plan' }).click()
  await expect(section.getByRole('button', { name: 'Generate navigation', exact: true })).toBeEnabled()
  await expect.poll(() => previews.length).toBe(1)
  const preview = previews[0], enqueue = { ...preview.selection, request_digest: preview.request_digest, idempotency_key: preview.idempotency_key }
  assert.equal(preview.selection.provider, ready.provider)
  for (const pathname of ['/plan', '/enqueue']) for (const token of ['', 'wrong']) assert.equal((await json(page, '/api/code-navigation-indexing' + pathname, pathname === '/plan' ? preview.selection : enqueue, token)).status, 403)
  await command('preview'); mutations.length = 0; check('pure preview and real CSRF refusal')
  await page.route('**/api/code-navigation-indexing/enqueue', async route => { await route.fetch(); await route.abort('failed'); await page.unroute('**/api/code-navigation-indexing/enqueue') }, { times: 1 })
  await section.getByRole('button', { name: 'Generate navigation', exact: true }).click()
  await expect(section.getByRole('button', { name: 'Retry exact request' })).toBeVisible()
  const first = await command('queued'); assert.equal(first.requestDigest, preview.request_digest)
  await section.getByRole('button', { name: 'Retry exact request' }).click()
  await expect(section.getByText('planning', { exact: true })).toBeVisible()
  assert.equal(mutations.length, 2); assert.equal(mutations[0], mutations[1]); assert.deepEqual(JSON.parse(mutations[0]), enqueue)
  const retry = await command('queued'); assert.equal(retry.requestDigest, first.requestDigest)
  check('lost admitted response retains exact request and one coordinator')
  phase = 'production native publication'
  const publication = await command('publish')
  assert.equal(publication.parentDigest, preview.request_digest)
  await section.getByRole('button', { name: 'Refresh indexing' }).click()
  await expect(section.getByText('current', { exact: true })).toBeVisible({ timeout: 15000 })
  function position(doc, line, character) { return '?' + new URLSearchParams({ repo: ready.repo, path: doc, ref: ready.commit, line: String(line), character: String(character), encoding: 'utf8' }) }
  async function reads() {
    const definition = await json(page, '/api/find_definitions' + position('a/a.go', 2, 15))
    const references = await json(page, '/api/find_references' + position('b/b.go', 1, 6))
    const hover = await json(page, '/api/hover' + position('a/a.go', 2, 15))
    assert.equal(definition.status, 200); assert.equal(definition.body.available, true); assert.equal(definition.body.location.path, 'b/b.go'); assert.equal(definition.body.location.range.start.line, 1); assert.equal(definition.body.location.range.start.character, 6)
    assert.equal(references.status, 200); assert.equal(references.body.available, true); assert.equal(references.body.locations.length, 1); assert.equal(references.body.locations[0].path, 'a/a.go'); assert.equal(references.body.locations[0].range.start.line, 2); assert.equal(references.body.locations[0].range.start.character, 15)
    assert.equal(hover.status, 200); assert(hover.body.hover?.symbol.includes('Answer'))
    return { definition: definition.body, references: references.body, hover: hover.body }
  }
  const cold = await reads(); assert.deepEqual(await reads(), cold)
  check('rendered native current and cold warm cross-member HTTP navigation')
  phase = 'ordinary runtime no-op boundary'; const warm = await command('warm')
  assert.deepEqual(await reads(), cold); check('ordinary polling preserves current without native replay')
  await command('stale')
  assert.equal((await json(page, '/api/code-navigation-indexing/enqueue', enqueue)).status, 409)
  for (const pathname of ['/api/find_definitions', '/api/find_references', '/api/hover']) {
    const query = pathname === '/api/find_references' ? position('b/b.go', 1, 6) : position('a/a.go', 2, 15)
    assert.equal((await json(page, pathname + query)).body.available, false)
  }
  await section.getByRole('button', { name: 'Refresh indexing' }).click(); await expect(section.getByText('stale', { exact: true })).toBeVisible()
  check('source transition fences rendered state and cached native reads')
  await admin.context.close(); await browser.close(); browser = undefined
  assert.equal(unexpected, 0); assert.equal(deliberate, 7)
  phase = 'native lifecycle teardown'; const cleanup = await command('finish'); child.stdin.end()
  const [code] = await exited; assert.equal(code, 0)
  await join(tunnel); tunnel = undefined
  Object.assign(results, { fixture: { repo: ready.repo, commit: ready.commit, provider: ready.provider, publication }, warm, cleanup, managed_requests: requests, unexpected_browser_errors: unexpected, deliberate_network_console_errors: deliberate, joined: true })
  fs.writeFileSync(receipt, JSON.stringify(results, null, 2) + '\n', { flag: 'wx', mode: 0o600 })
  console.log(JSON.stringify({ result: 'pass', checks: results.checks.length }))
}
const timer = setTimeout(() => { fatal ??= new Error('driver timeout'); child.stdin.end(); wake?.() }, 900000)
run().catch(error => {
  let recorded = diagnostics + '\nBrowser errors: ' + JSON.stringify(browserErrors)
  let summary = `Native Settings failed during ${phase}: ${fatal?.message || error.message}`
  for (const secret of [ready?.password, ready?.adminEmail, ready?.ordinaryEmail]) if (secret) {
    recorded = recorded.replaceAll(secret, '[redacted]')
    summary = summary.replaceAll(secret, '[redacted]')
  }
  fs.writeFileSync(receipt + '.failure.txt', recorded, { flag: 'wx', mode: 0o600 })
  console.error(summary); process.exitCode = 1
}).finally(async () => {
  clearTimeout(timer); lines.close(); child.stdin.end()
  if (browser) await browser.close()
  if (child.exitCode === null && child.signalCode === null) {
    let grace; await Promise.race([exited, new Promise(resolve => { grace = setTimeout(resolve, 30000) })]); clearTimeout(grace)
  }
  await join(child); await join(tunnel)
})
