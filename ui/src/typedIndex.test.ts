import { afterEach, expect, test, vi } from 'vitest'
import { setCSRFToken } from './authSession'
import { enqueueTypedIndex, fetchTypedView, validateTypedPreview, validateTypedProviders, validateTypedView } from './typedIndex'
const repo = 'example.test/repo'
const h = 'sha256:' + 'a'.repeat(64)
const view = { schema: 'phebs-typed-index-status-v1', repository: repo, commit: 'a'.repeat(40), revision: h, available: true, state: 'absent', provider: 'bazel-rules-go-scip-v1', profile: 'reduced', target_profile: 'reduced', config_profile: 'ordinary', resource_profile: 'native-arm64-bounded-v1', request_digest: '', job_state: '', current_commit: '', reason: '', checked_purpose: '' }
const selection = { repository: repo, provider: view.provider, profile: view.profile, expected_revision: h, purpose: 'publish' as const }
const preview = { schema: 'phebs-typed-index-preview-v1', selection, commit: view.commit, request_digest: h, idempotency_key: 'a'.repeat(64), resource_profile: view.resource_profile }
afterEach(() => { vi.restoreAllMocks(); setCSRFToken() })
test('refuses crossed, unknown, malformed and oversized status; accepts closed refusal', () => {
  expect(validateTypedView(view, repo)).toEqual(view)
  expect(validateTypedView({ ...view, reason: 'capacity_refused' }, repo).reason).toBe('capacity_refused')
  for (const changed of [{ repository: 'other' }, { commit: 'HEAD' }, { revision: 'unknown' }, { state: 'probably current' }, { job_state: 'bad' }, { reason: 'private raw error' }, { profile: 'x'.repeat(129) }, { source_path: '/private' }, { target_profile: 'other' }]) expect(() => validateTypedView({ ...view, ...changed }, repo)).toThrow()
  expect(() => validateTypedProviders({ schema: 'phebs-typed-index-providers-v1', providers: [] })).toThrow()
  expect(validateTypedPreview(preview, selection, view.commit)).toEqual(preview)
  expect(() => validateTypedPreview({ ...preview, selection: { ...selection, purpose: 'canary' } }, selection, view.commit)).toThrow()
})
test('bounds streamed response bytes and never renders server diagnostics', async () => {
  vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('x'.repeat(32769)))
  await expect(fetchTypedView(repo)).rejects.toThrow('Indexing response unavailable')
  vi.mocked(fetch).mockResolvedValue(new Response('private worker secret', { status: 503 }))
  await expect(fetchTypedView(repo)).rejects.toThrow('Indexing unavailable')
})
test('enqueue carries CSRF and exact preview identity', async () => {
  setCSRFToken('neutral-csrf')
  vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify(view)))
  await enqueueTypedIndex(preview)
  const init = vi.mocked(fetch).mock.calls[0][1]!
  expect(init.headers).toMatchObject({ 'X-CSRF-Token': 'neutral-csrf' })
  expect(JSON.parse(init.body as string)).toEqual({ ...selection, request_digest: h, idempotency_key: 'a'.repeat(64) })
})
