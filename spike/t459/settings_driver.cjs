'use strict'

// Stateless pieces shared by both Settings drivers. Each driver keeps its own
// fixture frame protocol because the commands, bounds and phases differ.
const { spawn } = require('node:child_process')
const fs = require('node:fs')
const net = require('node:net')
const { once } = require('node:events')

async function unusedPort() {
  const listener = net.createServer()
  listener.listen(0, '127.0.0.1'); await once(listener, 'listening')
  const port = listener.address().port
  await new Promise(resolve => listener.close(resolve))
  return port
}

async function join(proc) {
  if (!proc?.pid || proc.exitCode !== null || proc.signalCode !== null) return
  proc.joining = true
  const done = once(proc, 'exit')
  proc.kill('SIGTERM')
  const timer = setTimeout(() => proc.kill('SIGKILL'), 10000)
  try { await done } finally { clearTimeout(timer) }
}

// A forward that fails to bind, or a link that drops, exits ssh; only join
// stops it deliberately. sshArgs ends with the host.
function forward(sshArgs, mappings, fail) {
  const proc = spawn('ssh', [...sshArgs.slice(0, -1), '-o', 'ExitOnForwardFailure=yes', '-N', ...mappings.flatMap(mapping => ['-L', mapping]), sshArgs.at(-1)], { stdio: 'ignore' })
  let failed = false
  const refused = () => { if (!failed && !proc.joining) { failed = true; fail() } }
  proc.on('error', refused)
  proc.on('exit', refused)
  return proc
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

// The only failure output path: readiness credentials never leave unredacted.
function reportFailure({ ready, receipt, recorded, summary }) {
  for (const secret of [ready?.password, ready?.adminEmail, ready?.ordinaryEmail]) if (secret) {
    recorded = recorded.replaceAll(secret, '[redacted]')
    summary = summary.replaceAll(secret, '[redacted]')
  }
  fs.writeFileSync(receipt + '.failure.txt', recorded, { flag: 'wx', mode: 0o600 })
  console.error(summary.slice(0, 1024)); process.exitCode = 1
}

module.exports = { unusedPort, join, forward, fetchJSON, reportFailure }
