// Explicit neutral probe: unchanged Settings HTTP client, real loopback handler.
// This does not render Settings or execute an indexer.
'use strict'
const assert = require('node:assert/strict')
const fs = require('node:fs')
const os = require('node:os')
const path = require('node:path')
const [ui, base, repository, phase, dark, denied] = process.argv.slice(2)
const ts = require(path.join(ui, 'node_modules/typescript'))
const temporary = fs.mkdtempSync(path.join(os.tmpdir(), 'phebs-settings-client-'))

async function main() {
  for (const name of ['typedIndex', 'authSession']) {
    const source = fs.readFileSync(path.join(ui, 'src', name + '.ts'), 'utf8')
    const result = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } })
    fs.writeFileSync(path.join(temporary, name + '.js'), result.outputText, { mode: 0o600 })
  }
  const client = require(path.join(temporary, 'typedIndex.js'))
  require(path.join(temporary, 'authSession.js')).setCSRFToken('neutral-settings-client')
  const nativeFetch = globalThis.fetch
  let origin = base
  // Browsers resolve these relative URLs against their page origin.
  globalThis.fetch = (url, init) => nativeFetch(new URL(url, origin), init)
  const changed = 'Indexing selection changed. Refresh before trying again.'
  const unavailable = 'Indexing unavailable. Refresh to retry.'
  if (phase === 'plan') {
    const providers = await client.fetchTypedProviders()
    assert.equal(providers.providers.length, 3)
    assert(providers.providers.every(provider => provider.available))
    const view = await client.fetchTypedView(repository)
    assert.equal(view.state, 'absent')
    assert.equal(view.available, true)
    const selection = { repository, expected_revision: view.revision, provider: view.provider, profile: view.profile, purpose: 'publish' }
    const preview = await client.planTypedIndex(selection, view.commit)
    assert.equal((await client.fetchTypedView(repository)).request_digest, '')
    for (const mutation of [{ expected_revision: 'sha256:' + 'b'.repeat(64) }, { provider: 'go-module-scip-v1' }, { profile: 'other' }]) {
      await assert.rejects(client.planTypedIndex({ ...selection, ...mutation }, view.commit), { message: changed })
    }
    origin = dark
    assert((await client.fetchTypedProviders()).providers.every(provider => !provider.available))
    const darkView = await client.fetchTypedView(repository)
    assert.equal(darkView.available, false)
    assert.equal(darkView.state, 'absent')
    await assert.rejects(client.planTypedIndex(selection, view.commit), { message: unavailable })
    origin = denied
    await assert.rejects(client.fetchTypedProviders(), { message: unavailable })
    await assert.rejects(client.fetchTypedView(repository), { message: unavailable })
    await assert.rejects(client.planTypedIndex(selection, view.commit), { message: unavailable })
    process.stdout.write(JSON.stringify(preview))
    return
  }
  const preview = JSON.parse(fs.readFileSync(0, 'utf8'))
  if (phase === 'enqueue') {
    const first = await client.enqueueTypedIndex(preview)
    assert.equal(first.state, 'planning')
    assert.equal(first.job_state, 'pending')
    assert.equal(first.request_digest, preview.request_digest)
    assert.deepEqual(await client.enqueueTypedIndex(preview), first)
    assert.deepEqual(await client.fetchTypedView(repository), first)
    await assert.rejects(client.planTypedIndex(preview.selection, preview.commit), { message: client.TYPED_REQUEST_RECORDED })
    await assert.rejects(client.enqueueTypedIndex({ ...preview, request_digest: 'sha256:' + 'b'.repeat(64) }), { message: changed })
  } else if (phase === 'stale') {
    const view = await client.fetchTypedView(repository)
    assert.equal(view.state, 'stale')
    assert.equal(view.commit, 'b'.repeat(40))
    assert.notEqual(view.revision, preview.selection.expected_revision)
    await assert.rejects(client.planTypedIndex(preview.selection, preview.commit), { message: changed })
    await assert.rejects(client.enqueueTypedIndex(preview), { message: changed })
  } else if (phase === 'restore') {
    const view = await client.fetchTypedView(repository)
    assert.equal(view.state, 'stale')
    assert.equal(view.available, false)
    assert.equal(view.request_digest, '')
    await assert.rejects(client.planTypedIndex(preview.selection, preview.commit), { message: unavailable })
    await assert.rejects(client.enqueueTypedIndex(preview), { message: unavailable })
  } else {
    throw new Error('unknown neutral probe phase')
  }
}

main().finally(() => fs.rmSync(temporary, { recursive: true, force: true })).catch(error => {
  console.error(error)
  process.exitCode = 1
})
