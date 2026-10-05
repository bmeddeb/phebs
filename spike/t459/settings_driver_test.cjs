'use strict'

// Exercise each actual failure callback without launching SSH or a browser.
const assert = require('node:assert/strict')
const fs = require('node:fs')
const path = require('node:path')
const vm = require('node:vm')
for (const name of ['settings_browser.cjs', 'settings_native.cjs']) {
  const source = fs.readFileSync(path.join(__dirname, name), 'utf8')
  const start = source.indexOf('run().catch(error => {')
  const end = source.indexOf('}).finally', start)
  assert(start >= 0 && end > start, 'actual driver failure callback required')
  const captured = []
  const ready = { password: 'private-passphrase', adminEmail: 'admin@private.invalid', ordinaryEmail: 'reader@private.invalid' }
  const privateText = Object.values(ready).join(' ')
  const context = {
    ready, diagnostics: privateText, browserErrors: [privateText], statusReads: [], browserDiagnostics: [], results: { managed_request_counts: [] },
    phase: 'login', fatal: undefined, error: { message: privateText }, receipt: '/unused', process: {},
    fs: { writeFileSync: (_path, content) => captured.push(content) }, console: { error: content => captured.push(content) },
  }
  vm.runInNewContext(source.slice(start + 'run().catch(error => {'.length, end), context, { timeout: 1000 })
  assert.equal(captured.length, 2); assert.equal(context.process.exitCode, 1)
  for (const output of captured) for (const secret of Object.values(ready)) assert(!output.includes(secret), `${name} leaked a readiness credential`)
}
console.log('Both driver failure callbacks redact credentials.')
