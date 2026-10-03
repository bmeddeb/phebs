import { useEffect, useRef, useState } from 'react'
import { useStyletron } from 'baseui'
import { Button, KIND, SIZE } from 'baseui/button'
import { StateNotice, StatusChip } from '../components/kit'
import { fetchRepoStatus } from '../api'
import { enqueueTypedIndex, fetchTypedProviders, fetchTypedView, planTypedIndex, type TypedIndexPreview, type TypedIndexProviders, type TypedIndexSelection, type TypedIndexView } from '../typedIndex'
import { navigate, useHashRoute } from '../router'
import { FONTS, usePhebsTokens } from '../theme'
import { isAbortError } from '../util'

const ACTIVE = new Set(['planning', 'indexing', 'validating', 'publishing'])

export function CodeNavigationIndexingSection() {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const [, params] = useHashRoute()
  const repository = params.get('repo') ?? ''
  const section = params.get('section')
  const purpose = (['canary', 'dry-run'].includes(params.get('purpose') ?? '') ? params.get('purpose') : 'publish') as TypedIndexSelection['purpose']
  const [providers, setProviders] = useState<TypedIndexProviders | null>(null)
  const [repositories, setRepositories] = useState<string[]>([])
  const [view, setView] = useState<TypedIndexView | null>(null)
  const [preview, setPreview] = useState<TypedIndexPreview | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [refresh, setRefresh] = useState(0)
  const generation = useRef(0)
  const mutation = useRef<AbortController | null>(null)

  useEffect(() => {
    const control = new AbortController()
    fetchTypedProviders(control.signal).then(setProviders).catch(e => { if (!isAbortError(e)) setError('Indexing availability unavailable. Refresh to retry.') })
    fetchRepoStatus(control.signal).then(rows => {
      if (!Array.isArray(rows) || rows.length > 4096 || rows.some(row => typeof row.name !== 'string' || row.name.length > 512)) throw new Error('repository bound')
      setRepositories(rows.filter(row => !row.deleting).map(row => row.name).sort())
    }).catch(e => { if (!isAbortError(e)) setError('Repository selection unavailable. Refresh to retry.') })
    return () => control.abort()
  }, [refresh])

  useEffect(() => {
    const epoch = ++generation.current
    mutation.current?.abort()
    setBusy(false)
    setPreview(null)
    setView(current => current?.repository === repository ? current : null)
    if (!repository || repository.length > 512) return
    const control = new AbortController()
    let timer: ReturnType<typeof setTimeout> | undefined
    const read = async () => {
      try {
        const next = await fetchTypedView(repository, control.signal)
        if (epoch !== generation.current) return
        setView(next)
        setError(current => current.includes('availability') || current.includes('Repository selection') ? current : '')
        setPreview(current => current?.selection.expected_revision === next.revision && current.commit === next.commit ? current : null)
        if (ACTIVE.has(next.state)) timer = setTimeout(read, 5000)
      } catch (e) {
        if (epoch === generation.current && !isAbortError(e)) setError('Indexing status unavailable. Last confirmed state is retained; refresh to retry.')
      }
    }
    void read()
    return () => { control.abort(); clearTimeout(timer) }
  }, [repository, purpose, refresh])
  useEffect(() => () => mutation.current?.abort(), [])
  useEffect(() => {
    if (section !== 'code-navigation-indexing') return
    const heading = document.getElementById('code-navigation-indexing-heading')
    heading?.focus({ preventScroll: true })
    heading?.scrollIntoView?.({ block: 'start' })
  }, [repository, section])

  const route = (repo: string, selectedPurpose = purpose) => navigate('/settings', { repo, purpose: selectedPurpose, section: 'code-navigation-indexing' })
  const plan = async () => {
    if (!view?.available || error || !providers?.providers.some(provider => provider.available && provider.id === view.provider)) return
    const epoch = generation.current
    const control = new AbortController()
    mutation.current = control
    setBusy(true)
    try {
      const next = await planTypedIndex({ repository, expected_revision: view.revision, provider: view.provider, profile: view.profile, purpose }, view.commit, control.signal)
      if (epoch === generation.current) setPreview(next)
    } catch (e) { if (epoch === generation.current && !isAbortError(e)) setError('Indexing selection changed or is unavailable. Refresh before planning again.') }
    finally { if (epoch === generation.current) setBusy(false) }
  }
  const enqueue = async () => {
    if (!preview || busy) return
    const epoch = generation.current
    const control = new AbortController()
    mutation.current = control
    setBusy(true)
    try {
      const next = await enqueueTypedIndex(preview, control.signal)
      if (epoch === generation.current) { setView(next); setError(''); setRefresh(value => value + 1) }
    } catch (e) { if (epoch === generation.current && !isAbortError(e)) setError('Request not confirmed. Retry sends the same exact request; refresh to re-plan.') }
    finally { if (epoch === generation.current) setBusy(false) }
  }
  const field = css({ display: 'grid', gap: '6px', minWidth: 0, fontSize: '12px', color: tok.textSecondary })
  const select = css({ width: '100%', minWidth: 0, boxSizing: 'border-box', padding: '9px', color: tok.textPrimary, backgroundColor: tok.pageBg, border: `1px solid ${tok.cardBorder}`, borderRadius: '6px', fontFamily: FONTS.SANS, fontSize: '12px' })
  const selected = view?.repository === repository ? view : null
  return (
    <section id="code-navigation-indexing" aria-labelledby="code-navigation-indexing-heading" className={css({ marginBottom: '32px', minWidth: 0 })}>
      <h1 id="code-navigation-indexing-heading" tabIndex={-1} className={css({ margin: '0 0 8px', scrollMarginTop: '64px', fontSize: '20px', color: tok.textPrimary })}>Code navigation indexing</h1>
      <p className={css({ fontSize: '12px', color: tok.textSecondary })}>Generate precise navigation for the repository’s indexed HEAD.</p>
      {error && <div role="alert"><StateNotice tone="amber" title="Indexing unavailable">{error}</StateNotice></div>}
      <div className={css({ display: 'flex', gap: '8px', marginBottom: '12px' })}>
        <Button size={SIZE.compact} kind={KIND.secondary} onClick={() => { setError(''); setRefresh(value => value + 1) }} disabled={busy}>Refresh indexing</Button>
        {preview && error && <Button size={SIZE.compact} onClick={() => void enqueue()} disabled={busy}>Retry exact request</Button>}
      </div>
      {!providers ? <p role="status">Loading provider availability…</p> : (
        <div className={css({ display: 'grid', gap: '8px', marginBottom: '14px' })}>
          {providers.providers.map((provider, i) => (
            <article key={provider.id} aria-label={provider.name} data-provider-id={provider.id} className={css({ display: 'flex', flexWrap: 'wrap', justifyContent: 'space-between', gap: '8px', padding: '12px', border: `1px solid ${tok.cardBorder}`, borderRadius: '8px' })}>
              <div><span className={css({ fontFamily: FONTS.MONO, fontSize: '11px', color: tok.textTertiary })}>{String(i + 1).padStart(2, '0')} · {i === 0 ? 'Bazel first' : provider.name}</span><h2 className={css({ margin: '3px 0', fontSize: '13px', color: tok.textPrimary })}>{provider.name}</h2></div>
              <StatusChip tone={provider.available && selected?.available && selected.provider === provider.id ? 'green' : 'blue'} role="status">{provider.available && selected?.available && selected.provider === provider.id ? 'Available' : 'Unavailable'}</StatusChip>
            </article>
          ))}
        </div>
      )}
      <div className={css({ display: 'grid', gap: '12px' })}>
        <label className={field}>Repository<select aria-label="Indexing repository" className={select} value={repository} onChange={event => route(event.target.value)} disabled={busy}>
          <option value="">Choose a repository</option>
          {repository && !repositories.includes(repository) && <option value={repository}>{repository}</option>}
          {repositories.map(repo => <option key={repo} value={repo}>{repo}</option>)}
        </select></label>
        {repository && !selected && !error && <p role="status">Loading indexing status…</p>}
        {selected && <div aria-live="polite" className={css({ minWidth: 0 })}>
          <StatusChip tone={selected.state === 'current' ? 'green' : selected.state === 'stale' || selected.state === 'failed' ? 'amber' : selected.state === 'canceled' ? 'neutral' : 'blue'}>{selected.state}</StatusChip>
          <p className={css({ fontSize: '12px', color: tok.textSecondary, overflowWrap: 'anywhere' })}>Indexed HEAD: <code>{selected.commit || 'Unavailable'}</code></p>
          {selected.current_commit && <p className={css({ fontSize: '12px', color: tok.textSecondary, overflowWrap: 'anywhere' })}>Current navigation: <code>{selected.current_commit}</code></p>}
          {selected.request_digest && <p className={css({ fontSize: '12px', color: tok.textSecondary })}>Coordinator: {selected.job_state || 'Unavailable'}{selected.checked_purpose ? ` · ${selected.checked_purpose} checked without publication` : ''}</p>}
          {selected.reason && <p className={css({ fontSize: '12px', color: tok.textSecondary })}>Refused: {selected.reason.replaceAll('_', ' ')}</p>}
          {!selected.available && <StateNotice tone="blue" title="Managed indexing unavailable">No installed profile is available for this repository.</StateNotice>}
        </div>}
        {selected?.available && providers?.providers.some(provider => provider.available && provider.id === selected.provider) && <>
          <label className={field}>Target profile<select className={select} disabled><option>{selected.target_profile}</option></select></label>
          <label className={field}>Configuration profile<select className={select} disabled><option>{selected.config_profile}</option></select></label>
          <label className={field}>Resource profile<select className={select} disabled><option>{selected.resource_profile}</option></select></label>
          <label className={field}>Action<select aria-label="Indexing action" className={select} value={purpose} onChange={event => route(repository, event.target.value as TypedIndexSelection['purpose'])} disabled={busy}>
            <option value="publish">Generate navigation</option><option value="canary">Canary</option><option value="dry-run">Dry run</option>
          </select></label>
          <p className={css({ margin: 0, fontSize: '12px', color: tok.textSecondary })}>Canary and dry run validate without publishing. Only a complete generation replaces current navigation.</p>
          <Button size={SIZE.compact} kind={KIND.secondary} disabled={busy || !!error || ACTIVE.has(selected.state)} onClick={() => void plan()}>Review indexing plan</Button>
        </>}
        {preview && <div className={css({ minWidth: 0, padding: '12px', border: `1px solid ${tok.cardBorder}`, borderRadius: '8px' })}>
          <p className={css({ fontSize: '12px', color: tok.textSecondary, overflowWrap: 'anywhere' })}>Exact commit: <code>{preview.commit}</code></p>
          <p className={css({ fontSize: '12px', color: tok.textSecondary })}>This preview selects the sealed profile. Native package planning runs after enqueue.</p>
          <details><summary>Request authority</summary><code className={css({ display: 'block', fontSize: '11px', overflowWrap: 'anywhere' })}>{preview.request_digest}</code></details>
          <Button size={SIZE.compact} disabled={busy || !!error} onClick={() => void enqueue()}>{purpose === 'publish' ? 'Generate navigation' : purpose === 'canary' ? 'Run canary' : 'Run dry run'}</Button>
        </div>}
        {providers && !providers.providers.some(provider => provider.available) && <StateNotice tone="blue" title="Current build boundary">Managed generation is not registered in this build. Committed SCIP artifacts remain the code-navigation source.</StateNotice>}
      </div>
    </section>
  )
}
