'use strict'

// Exercise actual driver failure callbacks and shared tunnel events without
// launching SSH, a browser, writing files or opening a network listener.
const assert = require('node:assert/strict')
const fs = require('node:fs')
const path = require('node:path')
const vm = require('node:vm')
const { EventEmitter } = require('node:events')
const { reportFailure } = require('./settings_driver.cjs')

const ready = { password: 'private-passphrase', adminEmail: 'admin@private.invalid', ordinaryEmail: 'reader@private.invalid' }
const privateText = Object.values(ready).join(' ')
const { writeFileSync } = fs, { error } = console, exitCode = process.exitCode
try {
  const nativeSource = fs.readFileSync(path.join(__dirname, 'settings_native.cjs'), 'utf8')
  assert(nativeSource.includes('PHEBS_TYPED_NATIVE_TRANSPORT') && nativeSource.includes('PHEBS_TYPED_NATIVE_SSH_TARGET'), 'native rehearsal transport parameters required')
  assert(!nativeSource.includes('colima-phebs-t451a'), 'native rehearsal still uses a retired host parameter')
  assert(nativeSource.includes('StrictHostKeyChecking=yes') && nativeSource.includes("transport === 'direct'"), 'native rehearsal transport branch missing')
  for (const name of ['settings_browser.cjs', 'settings_native.cjs']) {
    const source = fs.readFileSync(path.join(__dirname, name), 'utf8')
    const marker = 'run().catch(error => ', start = source.indexOf(marker), end = source.indexOf(')).finally', start)
    assert(start >= 0 && end > start, `${name} actual failure callback required`)
    for (const direct of ['console.error', '.failure.txt']) assert(!source.includes(direct), `${name} writes failure output directly`)
    const captured = []
    fs.writeFileSync = (_path, content) => captured.push(content)
    console.error = content => captured.push(content)
    vm.runInNewContext(source.slice(start + marker.length, end + 1), {
      ready, receipt: '/unused', reportFailure, diagnostics: privateText,
      browserErrors: [privateText], statusReads: [], browserDiagnostics: [privateText], results: { managed_request_counts: [] },
      phase: 'login', fatal: undefined, error: { message: privateText + 'x'.repeat(2048) },
    }, { timeout: 1000 })
    assert.equal(process.exitCode, 1); assert.equal(captured.length, 2); assert(captured[1].length <= 1024)
    for (const output of captured) for (const secret of Object.values(ready)) assert(!output.includes(secret), `${name} failure path leaked a readiness credential`)
  }
} finally {
  fs.writeFileSync = writeFileSync; console.error = error; process.exitCode = exitCode
}

const helperSource = fs.readFileSync(path.join(__dirname, 'settings_driver.cjs'), 'utf8')
function tunnelFixture() {
  const proc = new EventEmitter()
  proc.pid = 1; proc.exitCode = null; proc.signalCode = null
  proc.kill = signal => { queueMicrotask(() => { proc.signalCode = signal; proc.emit('exit', null, signal) }); return true }
  const context = {
    module: { exports: {} }, setTimeout, clearTimeout,
    require: name => name === 'node:child_process' ? { spawn: () => proc } : require(name),
  }
  vm.runInNewContext(helperSource, context, { timeout: 1000 })
  let failures = 0
  const helper = context.module.exports
  assert.equal(helper.forward(['host'], ['1:127.0.0.1:2'], () => failures++), proc)
  return { proc, helper, failures: () => failures }
}
async function checkTunnels() {
  const unexpected = tunnelFixture()
  unexpected.proc.exitCode = 1; unexpected.proc.emit('exit', 1)
  assert.equal(unexpected.failures(), 1)
  const duplicate = tunnelFixture()
  duplicate.proc.emit('error', new Error('transport')); duplicate.proc.exitCode = 1; duplicate.proc.emit('exit', 1)
  assert.equal(duplicate.failures(), 1)
  const deliberate = tunnelFixture()
  await deliberate.helper.join(deliberate.proc)
  assert.equal(deliberate.failures(), 0)
  for (const name of ['settings_browser.cjs', 'settings_native.cjs']) {
    const source = fs.readFileSync(path.join(__dirname, name), 'utf8')
    const start = source.indexOf('  const [code] = await exited;'), end = source.indexOf('\n}', start)
    assert(start >= 0 && end > start, `${name} actual receipt tail required`)
    const context = {
      assert, exited: Promise.resolve([0]), tunnel: {}, fatal: undefined,
      ready: {}, publication: {}, warm: {}, cleanup: {}, finished: {}, requests: 0,
      unexpected: 0, deliberate: 7, unexpectedBrowserErrors: 0, expectedNetworkErrors: 11,
      results: { checks: [], states: [], viewports: [] }, receipt: '/unused', console: { log: () => {} },
      fs: { writeFileSync: () => { throw new Error('receipt sealed after tunnel failure') } },
    }
    context.join = async () => { context.fatal = new Error('fixture tunnel failed') }
    await assert.rejects(vm.runInNewContext('(async () => {' + source.slice(start, end) + '})()', context, { timeout: 1000 }), /fixture tunnel failed/)
  }
  console.log('Both actual callbacks redact credentials; tunnel failures report once, deliberate joins stay quiet, and late failures refuse receipt sealing.')
}
checkTunnels().catch(error => { console.error(error.message); process.exitCode = 1 })
