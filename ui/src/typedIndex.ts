import type { Schemas } from './api'
import { csrfHeaders, notifyAuthRequired } from './authSession'

export type TypedIndexView = Schemas['TypedIndexView']
export type TypedIndexSelection = Schemas['TypedIndexSelection']
export type TypedIndexPreview = Schemas['TypedIndexPreview']
export type TypedIndexProviders = Schemas['TypedIndexProviders'] & { providers: NonNullable<Schemas['TypedIndexProviders']['providers']> }
export type TypedIndexEnqueue = Schemas['TypedIndexEnqueue']
export const TYPED_PROVIDERS = ['bazel-rules-go-scip-v1', 'go-module-scip-v1', 'imported-artifact-scip-v1'] as const
export const TYPED_STATES = ['absent', 'current', 'stale', 'planning', 'indexing', 'validating', 'publishing', 'failed', 'canceled'] as const
export const TYPED_REQUEST_RECORDED = 'This exact request is already recorded. It cannot start another run. Change the indexed HEAD or installed profile to run this purpose again.'
const PATH = '/api/code-navigation-indexing'
const digest = (v: unknown) => typeof v === 'string' && /^sha256:[a-f0-9]{64}$/.test(v)
const commit = (v: unknown) => typeof v === 'string' && (v === '' || /^[a-f0-9]{40}$/.test(v))
const word = (v: unknown, limit = 128) => typeof v === 'string' && v.length <= limit && ![...v].some(char => char.charCodeAt(0) < 32 || char.charCodeAt(0) === 127)
const invalid = () => new Error('Indexing response unavailable. Refresh to retry.')
function record(v: unknown, fields: string[]): Record<string, unknown> {
  if (!v || typeof v !== 'object' || Array.isArray(v) || Object.keys(v).some(k => k !== '$schema' && !fields.includes(k))) throw invalid()
  return v as Record<string, unknown>
}
export function validateTypedProviders(value: unknown): TypedIndexProviders {
  const v = record(value, ['schema', 'providers'])
  if (v.schema !== 'phebs-typed-index-providers-v1' || !Array.isArray(v.providers) || v.providers.length !== 3) throw invalid()
  const names = ['Bazel', 'Go module / workspace', 'Existing artifact']
  v.providers.forEach((row, i) => {
    const p = record(row, ['id', 'name', 'available'])
    if (p.id !== TYPED_PROVIDERS[i] || p.name !== names[i] || typeof p.available !== 'boolean') throw invalid()
  })
  return value as TypedIndexProviders
}
export function validateTypedView(value: unknown, repository: string): TypedIndexView {
  const fields = ['schema', 'repository', 'commit', 'revision', 'available', 'state', 'provider', 'profile', 'target_profile', 'config_profile', 'resource_profile', 'request_digest', 'job_state', 'current_commit', 'reason', 'checked_purpose']
  const v = record(value, fields)
  if (v.schema !== 'phebs-typed-index-status-v1' || v.repository !== repository || !word(v.repository, 512) || !commit(v.commit) || !commit(v.current_commit) || typeof v.available !== 'boolean' || !TYPED_STATES.includes(v.state as TypedIndexView['state'])) throw invalid()
  for (const field of ['provider', 'profile', 'target_profile', 'config_profile', 'resource_profile']) if (!word(v[field])) throw invalid()
  for (const field of ['revision', 'request_digest']) if (v[field] !== '' && !digest(v[field])) throw invalid()
  if (!['', ...TYPED_PROVIDERS].includes(v.provider as '') || !['', 'pending', 'claimed', 'running', 'done', 'failed', 'canceled'].includes(String(v.job_state)) || !['', 'publish', 'canary', 'dry-run'].includes(String(v.checked_purpose))) throw invalid()
  const reasons = ['', 'invalid_contract', 'administrator_required', 'disabled', 'authority_changed', 'unsupported_profile', 'prehydration_unverified', 'capacity_refused', 'canceled', 'wall_limit', 'execution_failed', 'containment_failed']
  // Accept only the managed contract's closed refusal vocabulary.
  if (!word(v.reason, 64) || (v.reason !== '' && !reasons.includes(String(v.reason)))) throw invalid()
  if (v.available && (!digest(v.revision) || !/^[a-f0-9]{40}$/.test(String(v.commit)) || v.provider === '' || !v.profile || v.target_profile !== v.profile || v.resource_profile !== 'native-arm64-bounded-v1')) throw invalid()
  return value as TypedIndexView
}
export function validateTypedPreview(value: unknown, selection: TypedIndexSelection, exactCommit: string): TypedIndexPreview {
  const v = record(value, ['schema', 'selection', 'commit', 'request_digest', 'idempotency_key', 'resource_profile'])
  const s = record(v.selection, ['repository', 'expected_revision', 'provider', 'profile', 'purpose'])
  if (v.schema !== 'phebs-typed-index-preview-v1' || v.commit !== exactCommit || !digest(v.request_digest) || !/^[a-f0-9]{64}$/.test(String(v.idempotency_key)) || v.resource_profile !== 'native-arm64-bounded-v1' || Object.entries(selection).some(([key, val]) => s[key] !== val)) throw invalid()
  return value as TypedIndexPreview
}
async function typedJSON(path: string, body?: unknown, signal?: AbortSignal): Promise<unknown> {
  const response = await fetch(PATH + path, { signal, credentials: 'same-origin', ...(body === undefined ? {} : { method: 'POST', headers: { 'Content-Type': 'application/json', ...csrfHeaders() }, body: JSON.stringify(body) }) })
  if (response.status === 401) notifyAuthRequired()
  if (!response.ok) {
    if (path === '/plan' && response.status === 422) {
      const value = await readTypedJSON(response)
      if (value && typeof value === 'object' && 'detail' in value && value.detail === 'request_already_recorded') throw new Error(TYPED_REQUEST_RECORDED)
    }
    throw new Error(response.status === 409 ? 'Indexing selection changed. Refresh before trying again.' : 'Indexing unavailable. Refresh to retry.')
  }
  return readTypedJSON(response)
}
async function readTypedJSON(response: Response): Promise<unknown> {
  const reader = response.body?.getReader()
  if (!reader) throw invalid()
  const chunks: Uint8Array[] = []
  let size = 0
  try {
    for (;;) {
      const { value, done } = await reader.read()
      if (done) break
      size += value.byteLength
      if (size > 32768) throw invalid()
      chunks.push(value)
    }
  } finally { await reader.cancel(); reader.releaseLock() }
  const bytes = new Uint8Array(size)
  let offset = 0
  for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.byteLength }
  try { return JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes)) as unknown } catch { throw invalid() }
}
export async function fetchTypedProviders(signal?: AbortSignal) { return validateTypedProviders(await typedJSON('/providers', undefined, signal)) }
export async function fetchTypedView(repository: string, signal?: AbortSignal) { return validateTypedView(await typedJSON('/status?' + new URLSearchParams({ repository }), undefined, signal), repository) }
export async function planTypedIndex(selection: TypedIndexSelection, exactCommit: string, signal?: AbortSignal) { return validateTypedPreview(await typedJSON('/plan', selection, signal), selection, exactCommit) }
export async function enqueueTypedIndex(preview: TypedIndexPreview, signal?: AbortSignal) {
  return validateTypedView(await typedJSON('/enqueue', { ...preview.selection, request_digest: preview.request_digest, idempotency_key: preview.idempotency_key } satisfies TypedIndexEnqueue, signal), preview.selection.repository)
}
