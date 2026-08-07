import { useEffect, useMemo, useRef, useState } from 'react'
import { useStyletron } from 'baseui'
import { Input } from 'baseui/input'
import { Notification, KIND } from 'baseui/notification'
import type { LanguageSupport } from '@codemirror/language'
import { fetchRepoStatus, streamSearch } from '../api'
import type { FileResult, Range, RepoStatus, SearchScopeReceipt, Stats } from '../api'
import { FOCUS_SEARCH, href, navigate } from '../router'
import { usePhebsTokens, useMode, FONTS, MOTION, REDUCED_MOTION, type PhebsTokens } from '../theme'
import { languageFor, langColor } from '../lang'
import { tokenize } from '../highlight'
import { SearchIcon, CopyIcon, CheckIcon, OpenIcon, ChevronRight, ChevronDown } from '../icons'
import { isAbortError, relTime, repoFilter, runeColumnToUTF16Offset, splitQueryTerms } from '../util'
import RepositoryBrowser from '../RepositoryBrowser'
import { AnalysisScopePanel } from '../components/AnalysisScopePanel'
import { analysisScopeFromRepoStatus } from '../components/analysisScope'

type Phase = 'idle' | 'streaming' | 'stopped' | 'done' | 'error'

const fileKey = (f: FileResult) => f.repo + '\0' + f.ref + '\0' + f.path
const firstMatchLine = (f: FileResult) =>
  f.chunks.find((chunk) => chunk.ranges.length > 0)?.ranges[0]?.start_line

export default function SearchPage({ params }: { params: URLSearchParams }) {
  const urlQuery = params.get('q') ?? ''
  const serviceRepository = params.get('repository') ?? ''
  const serviceKey = params.get('service_key') ?? ''
  const scopeKind = params.get('scope') === 'service' && serviceRepository && serviceKey
    ? 'service'
    : 'all_code'
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const { mode } = useMode()
  const [input, setInput] = useState(urlQuery)
  const [files, setFiles] = useState<FileResult[]>([])
  const [stats, setStats] = useState<Stats | null>(null)
  const [scopeReceipt, setScopeReceipt] = useState<SearchScopeReceipt | null>(null)
  const [repositories, setRepositories] = useState<RepoStatus[]>([])
  const [repositoriesLoading, setRepositoriesLoading] = useState(true)
  const [repositoriesError, setRepositoriesError] = useState('')
  const [repositoryGeneration, setRepositoryGeneration] = useState(0)
  const [phase, setPhase] = useState<Phase>('idle')
  const [error, setError] = useState('')
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set())
  const [selected, setSelected] = useState(-1)
  const [searchGeneration, setSearchGeneration] = useState(0)
  const stopRef = useRef<() => void>(() => {})
  const inputRef = useRef<HTMLInputElement>(null)
  const rowRefs = useRef(new Map<string, HTMLDivElement>())
  const seenReposRef = useRef(new Set<string>())
  const seenFilesRef = useRef(new Set<string>())

  useEffect(() => {
    const onFocus = () => inputRef.current?.focus()
    window.addEventListener(FOCUS_SEARCH, onFocus)
    return () => window.removeEventListener(FOCUS_SEARCH, onFocus)
  }, [])

  useEffect(() => {
    const controller = new AbortController()
    let active = true
    setRepositoriesLoading(true)
    setRepositoriesError('')
    fetchRepoStatus(controller.signal)
      .then((repos) => {
        if (active) setRepositories(repos)
      })
      .catch((cause) => {
        if (active && !isAbortError(cause)) setRepositoriesError(String(cause))
      })
      .finally(() => {
        if (active) setRepositoriesLoading(false)
      })
    return () => {
      active = false
      controller.abort()
    }
  }, [repositoryGeneration])

  const indexedAtByRepo = useMemo(() => {
    const indexed = new Map<string, string>()
    for (const repo of repositories) {
      if (repo.indexed_at) indexed.set(repo.name, repo.indexed_at)
    }
    return indexed
  }, [repositories])

  // the hash is the source of truth: searching = navigating
  useEffect(() => {
    setInput(urlQuery)
    setSelected(-1)
    setCollapsed(new Set())
    if (!urlQuery) {
      setPhase('idle')
      setFiles([])
      setStats(null)
      setScopeReceipt(null)
      return
    }
    setFiles([])
    setStats(null)
    setScopeReceipt(null)
    setError('')
    setPhase('streaming')
    stopRef.current = streamSearch(
      urlQuery,
      (batch) => setFiles((prev) => [...prev, ...batch.files]),
      (s) => {
        setStats(s)
        setPhase('done')
      },
      (msg) => {
        setError(msg)
        setPhase('error')
      },
      scopeKind === 'service'
        ? { kind: 'service', repository: serviceRepository, serviceKey }
        : { kind: 'all_code' },
      setScopeReceipt,
    )
    return () => stopRef.current()
  }, [urlQuery, searchGeneration, scopeKind, serviceRepository, serviceKey])

  // group by repo, preserving arrival order
  const groups = useMemo(() => {
    const m = new Map<string, FileResult[]>()
    for (const f of files) {
      const list = m.get(f.repo) ?? []
      list.push(f)
      m.set(f.repo, list)
    }
    return [...m.entries()]
  }, [files])

  const scopePresentation = useMemo(() => {
    const byName = new Map(repositories.map((repository) => [
      repository.name,
      repository,
    ]))
    if (groups.length > 0) {
      const scopes = []
      const gaps = []
      let missingScopeCount = 0
      for (const [name, resultFiles] of groups) {
        const repository = byName.get(name)
        if (!repository) {
          missingScopeCount++
          continue
        }
        const resultCommits = new Set(resultFiles.map((file) => file.ref).filter(Boolean))
        if (resultCommits.size !== 1) {
          gaps.push({
            state: 'unavailable',
            code: 'search_revision_scope_not_projectable',
            reason: resultCommits.size === 0
              ? `${name} has no exact result revision from which to project repository scope.`
              : `${name} spans multiple result revisions, so no single exact repository scope can be projected.`,
          })
          continue
        }
        const resultCommit = resultCommits.values().next().value
        if (!repository.indexed_commit_hash) {
          gaps.push({
            state: 'unavailable',
            code: 'search_index_scope_unavailable',
            reason: `${name} has no current indexed commit from which to project exact search scope.`,
          })
          continue
        }
        if (resultCommit !== repository.indexed_commit_hash) {
          gaps.push({
            state: 'unavailable',
            code: 'search_revision_scope_not_projectable',
            reason: `${name} search results are bound to ${resultCommit}, while the loaded repository scope is bound to ${repository.indexed_commit_hash}; the current analysis unit is not projected across revisions.`,
          })
          continue
        }
        scopes.push(analysisScopeFromRepoStatus(repository))
      }
      if (missingScopeCount > 0) {
        gaps.push({
          state: repositoriesLoading ? 'processing' : 'unavailable',
          code: repositoriesLoading
            ? 'repository_status_loading'
            : 'repository_status_unavailable',
          reason: `${missingScopeCount} result ${missingScopeCount === 1 ? 'repository has' : 'repositories have'} no loaded scope projection${repositoriesLoading ? ' yet' : ''}.`,
        })
      }
      return { scopes, gaps }
    }
    if (phase !== 'done' || !urlQuery) return { scopes: [], gaps: [] }
    if (repositoriesLoading) {
      return {
        scopes: [],
        gaps: [{
          state: 'processing',
          code: 'repository_status_loading',
          reason: 'The query is complete; its repository-scope projection is still loading.',
        }],
      }
    }
    if (repositoriesError) {
      return {
        scopes: [],
        gaps: [{
          state: 'unavailable',
          code: 'repository_status_unavailable',
          reason: 'The completed query has no loaded repository-scope authority.',
        }],
      }
    }

    const terms = splitQueryTerms(urlQuery)
    const repositoryTerms = terms.filter((term) => /^-?repo:/.test(term))
    const exactRepositoryByTerm = new Map(repositories.map((candidate) => [
      repoFilter(candidate.name),
      candidate,
    ]))
    const exactRepositories = repositoryTerms.map((term) =>
      exactRepositoryByTerm.get(term))
    if (exactRepositories.some((candidate) => !candidate)) {
      return {
        scopes: [],
        gaps: [{
          state: 'unavailable',
          code: 'search_scope_filter_not_projectable',
          reason: 'The completed zero-result query uses a repository filter that cannot be mapped exactly to the loaded repository-status projection.',
        }],
      }
    }
    const targets = repositoryTerms.length > 0
      ? exactRepositories.filter((candidate) => candidate !== undefined)
      : repositories
    if (targets.length === 0) {
      return {
        scopes: [],
        gaps: [{
          state: 'empty',
          code: 'no_visible_search_repositories',
          reason: 'The completed query had no visible indexed repository scope.',
        }],
      }
    }
    const revisionFiltered = terms.some((term) => /^-?rev:/.test(term))
    if (revisionFiltered) {
      return {
        scopes: [],
        gaps: [{
          state: 'unavailable',
          code: 'search_revision_scope_not_projectable',
          reason: 'The completed zero-result revision query has no result commit from which to project exact revision authority.',
        }],
      }
    }
    const indexedTargets = targets.filter((target) => target.indexed_commit_hash)
    const unavailableCount = targets.length - indexedTargets.length
    return {
      scopes: indexedTargets.map(analysisScopeFromRepoStatus),
      gaps: unavailableCount > 0 ? [{
        state: 'unavailable',
        code: 'search_index_scope_unavailable',
        reason: `${unavailableCount} targeted ${unavailableCount === 1 ? 'repository has' : 'repositories have'} no current indexed commit from which to project exact search scope.`,
      }] : [],
    }
  }, [groups, phase, repositories, repositoriesError, repositoriesLoading, urlQuery])

  // files reachable by keyboard: those in expanded groups, in render order
  const visible = useMemo(
    () => groups.filter(([repo]) => !collapsed.has(repo)).flatMap(([, fs]) => fs),
    [groups, collapsed],
  )

  const entering = useMemo(() => {
    const repos = new Set<string>()
    const files = new Set<string>()
    for (const [repo, repoFiles] of groups) {
      if (!seenReposRef.current.has(repo)) repos.add(repo)
      for (const file of repoFiles) {
        const key = fileKey(file)
        if (!seenFilesRef.current.has(key)) files.add(key)
      }
    }
    return { repos, files }
  }, [groups])

  useEffect(() => {
    for (const [repo, repoFiles] of groups) {
      seenReposRef.current.add(repo)
      for (const file of repoFiles) seenFilesRef.current.add(fileKey(file))
    }
  }, [groups])

  useEffect(() => {
    seenReposRef.current.clear()
    seenFilesRef.current.clear()
  }, [urlQuery, searchGeneration])

  // keyboard navigation: j/k move a file cursor, Enter opens, y copies, o folds
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const el = document.activeElement
      const interactive = el instanceof Element && el.closest('input, textarea, select, button, a, [contenteditable="true"], [role="button"], [role="link"]')
      if (interactive || e.metaKey || e.ctrlKey || e.altKey) return
      if (e.key === 'j' || e.key === 'k') {
        e.preventDefault()
        setSelected((s) => {
          const next = e.key === 'j' ? s + 1 : s - 1
          return Math.max(0, Math.min(visible.length - 1, next))
        })
      } else if (e.key === 'Enter' && selected >= 0 && visible[selected]) {
        const f = visible[selected]
        navigate('/file', {
          repo: f.repo,
          path: f.path,
          ref: f.ref,
          L: String(firstMatchLine(f) ?? 1),
        })
      } else if (e.key === 'y' && selected >= 0 && visible[selected]) {
        navigator.clipboard?.writeText(visible[selected].path)
      } else if (e.key === 'o' && selected >= 0 && visible[selected]) {
        const repo = visible[selected].repo
        setCollapsed((c) => {
          const n = new Set(c)
          if (n.has(repo)) n.delete(repo)
          else n.add(repo)
          return n
        })
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [visible, selected])

  // keep the selected card in view
  useEffect(() => {
    if (selected < 0 || !visible[selected]) return
    rowRefs.current.get(fileKey(visible[selected]))?.scrollIntoView({ block: 'nearest' })
  }, [selected, visible])

  const toggleGroup = (repo: string) =>
    setCollapsed((c) => {
      const n = new Set(c)
      if (n.has(repo)) n.delete(repo)
      else n.add(repo)
      return n
    })

  const repoCount = groups.length

  const stopSearch = () => {
    stopRef.current()
    setPhase('stopped')
  }

  return (
    <div>
      <form
        onSubmit={(e) => {
          e.preventDefault()
          const next = input.trim()
          if (!next) return
          if (next === urlQuery) setSearchGeneration((generation) => generation + 1)
          else navigate('/search', searchRouteParams(
            next, scopeKind, serviceRepository, serviceKey,
          ))
        }}
        className={css({ width: '100%', position: 'relative' })}
      >
        <Input
          inputRef={inputRef as React.RefObject<HTMLInputElement>}
          value={input}
          onChange={(e) => setInput(e.currentTarget.value)}
          name="q"
          aria-label="Search code"
          placeholder='Search code — try func.*Parse repo:zoekt lang:go "exact phrase"'
          clearable
          autoFocus
          startEnhancer={<SearchIcon size={14} />}
          endEnhancer={(
            <div className={css({ display: 'flex', alignItems: 'center', gap: '8px' })}>
              <Kbd>/</Kbd>
              <button
                type={phase === 'streaming' ? 'button' : 'submit'}
                onClick={phase === 'streaming' ? stopSearch : undefined}
                className={css({
                  height: '32px',
                  minWidth: '70px',
                  border: 'none',
                  borderRadius: '6px',
                  paddingLeft: '14px',
                  paddingRight: '14px',
                  fontFamily: FONTS.SANS,
                  fontSize: '13px',
                  fontWeight: 500,
                  color: phase === 'streaming' ? tok.textSecondary : tok.pageBg,
                  backgroundColor: phase === 'streaming' ? tok.fill : mode === 'dark' ? tok.textSecondary : tok.textPrimary,
                  boxShadow: phase === 'streaming' ? `inset 0 0 0 1px ${tok.kbdBorder}` : 'none',
                  cursor: 'pointer',
                  ':hover': { backgroundColor: phase === 'streaming' ? tok.kbdBorder : mode === 'dark' ? tok.textTertiary : tok.textSecondary },
                  ':focus-visible': { outline: `2px solid ${tok.accent}`, outlineOffset: '1px' },
                })}
              >
                {phase === 'streaming' ? 'Stop' : 'Search'}
              </button>
            </div>
          )}
          overrides={{
            Root: { style: { height: '44px', borderTopLeftRadius: '8px', borderTopRightRadius: '8px', borderBottomLeftRadius: '8px', borderBottomRightRadius: '8px' } },
            InputContainer: { style: { height: '44px' } },
            Input: { style: { fontFamily: FONTS.MONO, fontSize: '14px', lineHeight: '20px' } },
            StartEnhancer: { style: { paddingLeft: '14px', paddingRight: '2px' } },
            EndEnhancer: { style: { paddingLeft: '8px', paddingRight: '6px', backgroundColor: 'transparent' } },
          }}
        />
        {phase === 'streaming' && (
          <div
            aria-hidden="true"
            className={css({
              position: 'absolute',
              left: 0,
              right: 0,
              bottom: '-6px',
              height: '2px',
              borderRadius: '2px',
              backgroundImage: `linear-gradient(90deg, transparent 0%, ${tok.accent} 50%, transparent 100%)`,
              backgroundSize: '50% 100%',
              backgroundRepeat: 'no-repeat',
              animationName: { '0%': { backgroundPosition: '-60% 0' }, '100%': { backgroundPosition: '160% 0' } },
              animationDuration: MOTION.pulse,
              animationTimingFunction: 'linear',
              animationIterationCount: 'infinite',
              [REDUCED_MOTION]: { animationName: 'none', backgroundColor: tok.accent, backgroundImage: 'none' },
            })}
          />
        )}
      </form>

      <SearchScopeSelector
        kind={scopeKind}
        repository={serviceRepository}
        serviceKey={serviceKey}
        query={urlQuery || input.trim()}
        receipt={scopeReceipt}
      />

      <SearchMeta
        input={input}
        setInput={setInput}
        inputRef={inputRef}
        phase={phase}
        files={files}
        stats={stats}
        repoCount={repoCount}
        showNavigation={visible.length > 0}
      />

      {(scopePresentation.scopes.length > 0 || scopePresentation.gaps.length > 0) && (
        <div className={css({ marginTop: '14px' })}>
          <AnalysisScopePanel
            id="search-analysis-scope"
            scopes={scopePresentation.scopes}
            gaps={scopePresentation.gaps}
            eyebrow={groups.length > 0
              ? 'Scope of the mounted results'
              : 'Scope of the completed query'}
            compact
          />
        </div>
      )}

      <div className={css({
        display: 'flex',
        gap: '20px',
        marginTop: '14px',
        alignItems: 'flex-start',
        '@media screen and (max-width: 720px)': { flexDirection: 'column', gap: '12px' },
      })}>
        <RepositoryBrowser
          repositories={repositories}
          loading={repositoriesLoading}
          error={repositoriesError}
          onRetry={() => setRepositoryGeneration((generation) => generation + 1)}
          onInsertFilter={(repo) => {
            const term = repoFilter(repo)
            if (!splitQueryTerms(input).includes(term)) {
              setInput(`${input}${input && !input.endsWith(' ') ? ' ' : ''}${term}`)
            }
            inputRef.current?.focus()
          }}
        />
        <div className={css({ flex: '1 1 0', minWidth: 0, width: '100%' })}>
          <div className={css({
            display: 'flex',
            gap: '24px',
            alignItems: 'flex-start',
            '@media screen and (max-width: 900px)': { flexDirection: 'column', gap: '12px' },
          })}>
            {files.length > 0 && (
              <FacetRail
                files={files}
                query={urlQuery}
                scope={{ kind: scopeKind, repository: serviceRepository, serviceKey }}
              />
            )}
            <div className={css({ flex: '1 1 0', minWidth: 0, width: '100%' })}>
              {phase === 'error' && (
                <Notification kind={KIND.negative} overrides={{ Body: { style: { width: 'auto', marginTop: 0 } } }}>
                  {error}
                </Notification>
              )}
              {phase === 'done' && files.length === 0 && (
                <div className={css({ padding: '48px 0', textAlign: 'center', color: tok.textTertiary })}>
                  No results for <span className={css({ fontFamily: FONTS.MONO, color: tok.textPrimary })}>{urlQuery}</span>.
                </div>
              )}
              {groups.map(([repo, repoFiles]) => (
                <RepoGroup
                  key={repo}
                  repo={repo}
                  files={repoFiles}
                  indexedAt={indexedAtByRepo.get(repo)}
                  animateCard={entering.repos.has(repo)}
                  enteringFiles={entering.files}
                  open={!collapsed.has(repo)}
                  onToggle={() => toggleGroup(repo)}
                  selectedKey={selected >= 0 && visible[selected] ? fileKey(visible[selected]) : ''}
                  registerRef={(k, el) => {
                    if (el) rowRefs.current.set(k, el)
                    else rowRefs.current.delete(k)
                  }}
                />
              ))}
              {phase === 'streaming' && <SkeletonCards />}
            </div>
          </div>
          {phase === 'idle' && repositories.length > 0 && !repositoriesLoading && (
            <div className={css({ padding: '32px 0 0', color: tok.textTertiary, fontSize: '13px', lineHeight: '20px' })}>
              Browse a repository or enter a query to search its indexed source.
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

function searchRouteParams(
  q: string,
  kind: string,
  repository: string,
  serviceKey: string,
): Record<string, string> {
  const result: Record<string, string> = { q }
  if (kind === 'service' && repository && serviceKey) {
    result.scope = 'service'
    result.repository = repository
    result.service_key = serviceKey
  }
  return result
}

function SearchScopeSelector({ kind, repository, serviceKey, query, receipt }: {
  kind: string
  repository: string
  serviceKey: string
  query: string
  receipt: SearchScopeReceipt | null
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const allCodeHref = href('/search', { q: query })
  const serviceHref = repository && serviceKey
    ? href('/search', searchRouteParams(query, 'service', repository, serviceKey))
    : ''
  return (
    <fieldset className={css({ margin: '12px 0 0', padding: '10px 12px', border: `1px solid ${tok.cardBorder}`, borderRadius: '8px', minWidth: 0 })}>
      <legend className={css({ padding: '0 5px', color: tok.textTertiary, fontSize: '10.5px', lineHeight: '14px', textTransform: 'uppercase', letterSpacing: '0.07em' })}>
        Search scope
      </legend>
      <div className={css({ display: 'flex', alignItems: 'center', gap: '8px', flexWrap: 'wrap' })}>
        <a
          href={allCodeHref}
          aria-current={kind === 'all_code' ? 'page' : undefined}
          className={css(scopeChoice(tok, kind === 'all_code'))}
        >
          All code
        </a>
        {serviceHref ? (
          <a
            href={serviceHref}
            aria-current={kind === 'service' ? 'page' : undefined}
            className={css(scopeChoice(tok, kind === 'service'))}
          >
            Service · {serviceKey}
          </a>
        ) : (
          <a href={href('/services')} className={css(scopeChoice(tok, false))}>Choose a service</a>
        )}
        <span role="status" className={css({ marginLeft: 'auto', minWidth: 0, fontSize: '10.5px', lineHeight: '16px', color: tok.textTertiary, overflowWrap: 'anywhere', '@media screen and (max-width: 620px)': { width: '100%', marginLeft: 0 } })}>
          {kind === 'service'
            ? receipt
              ? `${receipt.service_status} · shared paths included · unowned paths excluded · ${receipt.result_files} cited files`
              : 'Shared paths are included; unowned paths are excluded. Exact current or stale authority is required.'
            : receipt
              ? `Visible indexed repositories · ${receipt.revisions.length} exact revision${receipt.revisions.length === 1 ? '' : 's'} · ${receipt.result_files} cited files`
              : 'Search every visible indexed repository.'}
        </span>
      </div>
      {kind === 'service' && repository && (
        <div className={css({ marginTop: '6px', fontFamily: FONTS.MONO, fontSize: '10px', lineHeight: '15px', color: tok.textTertiary, overflowWrap: 'anywhere' })}>
          {repository} · receipt {receipt ? receipt.digest.slice(0, 23) : 'pending'}
        </div>
      )}
    </fieldset>
  )
}

function scopeChoice(tok: PhebsTokens, active: boolean) {
  return {
    display: 'inline-flex',
    alignItems: 'center',
    minHeight: '30px',
    padding: '0 10px',
    border: `1px solid ${active ? tok.accent : tok.cardBorder}`,
    borderRadius: '6px',
    backgroundColor: active ? tok.selectedLineBg : tok.pageBg,
    color: active ? tok.textPrimary : tok.textSecondary,
    textDecoration: 'none',
    fontSize: '11.5px',
    fontWeight: active ? 600 : 500,
    ':hover': { backgroundColor: tok.hoverFill },
    ':focus-visible': { outline: `2px solid ${tok.accent}`, outlineOffset: '1px' },
  } as const
}

function Kbd({ children }: { children: React.ReactNode }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <kbd
      className={css({
        fontFamily: FONTS.MONO,
        fontSize: '11px',
        padding: '1px 5px',
        border: `1px solid ${tok.kbdBorder}`,
        borderRadius: '4px',
        color: tok.textSecondary,
      })}
    >
      {children}
    </kbd>
  )
}

// FacetRail derives repo and language counts from the streamed files; each
// facet toggles a repo:/lang: term in the query and re-navigates.
function FacetRail({ files, query, scope }: {
  files: FileResult[]
  query: string
  scope: { kind: string; repository: string; serviceKey: string }
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()

  const repos = useMemo(() => {
    const m = new Map<string, number>()
    for (const f of files) m.set(f.repo, (m.get(f.repo) ?? 0) + 1)
    return [...m.entries()].sort((a, b) => b[1] - a[1])
  }, [files])

  const langs = useMemo(() => {
    const m = new Map<string, number>()
    for (const f of files) {
      const l = (f.language || extLang(f.path)).toLowerCase()
      m.set(l, (m.get(l) ?? 0) + 1)
    }
    return [...m.entries()].sort((a, b) => b[1] - a[1])
  }, [files])

  const terms = new Set(splitQueryTerms(query))
  const toggle = (term: string) => {
    const next = new Set(terms)
    if (next.has(term)) next.delete(term)
    else next.add(term)
    navigate('/search', searchRouteParams(
      [...next].join(' '), scope.kind, scope.repository, scope.serviceKey,
    ))
  }
  const shortRepo = (r: string) => r.slice(r.lastIndexOf('/') + 1)

  return (
    <aside className={css({
      width: '200px',
      flexShrink: 0,
      '@media screen and (max-width: 720px)': {
        display: 'flex',
        gap: '16px',
        width: '100%',
        overflowX: 'auto',
        paddingBottom: '4px',
      },
    })}>
      <FacetSection title="Repositories">
        {repos.map(([repo, n]) => {
          const term = repoFilter(repo)
          const active = terms.has(term)
          return (
            <FacetRow
              key={repo}
              active={active}
              label={`${active ? 'Remove' : 'Add'} repository filter ${repo}`}
              onClick={() => toggle(term)}
            >
              <span
                aria-hidden="true"
                className={css({
                  width: '13px',
                  height: '13px',
                  border: `1px solid ${active ? tok.accent : tok.kbdBorder}`,
                  borderRadius: '3px',
                  backgroundColor: active ? tok.accent : 'transparent',
                  color: tok.onAccent,
                  fontSize: '11px',
                  lineHeight: '11px',
                  textAlign: 'center',
                  flexShrink: 0,
                })}
              >
                {active ? '✓' : ''}
              </span>
              <span className={css({ flex: 1, fontSize: '12.5px', color: tok.textSecondary, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' })}>
                {shortRepo(repo)}
              </span>
              <span className={css({ fontSize: '11px', color: tok.textTertiary, fontVariantNumeric: 'tabular-nums' })}>{n}</span>
            </FacetRow>
          )
        })}
      </FacetSection>
      {langs.length > 0 && (
        <FacetSection title="Languages">
          {langs.map(([lang, n]) => {
            const term = `lang:${lang}`
            const active = terms.has(term)
            return (
              <FacetRow
                key={lang}
                active={active}
                label={`${active ? 'Remove' : 'Add'} language filter ${lang}`}
                onClick={() => toggle(term)}
              >
                <span className={css({ width: '8px', height: '8px', borderRadius: '50%', backgroundColor: langColor('x.' + lang, tok) })} />
                <span className={css({ flex: 1, fontSize: '12.5px', fontWeight: active ? 600 : 400, color: active ? tok.textPrimary : tok.textSecondary })}>
                  {lang}
                </span>
                <span className={css({ fontSize: '11px', color: tok.textTertiary, fontVariantNumeric: 'tabular-nums' })}>{n}</span>
              </FacetRow>
            )
          })}
        </FacetSection>
      )}
    </aside>
  )
}

function FacetSection({ title, children }: { title: string; children: React.ReactNode }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <div className={css({
      marginBottom: '20px',
      '@media screen and (max-width: 720px)': {
        flex: '1 0 180px',
        marginBottom: 0,
        maxHeight: '180px',
        overflowY: 'auto',
      },
    })}>
      <div className={css({ fontSize: '11px', fontWeight: 600, letterSpacing: '0.05em', textTransform: 'uppercase', color: tok.textTertiary, marginBottom: '6px' })}>
        {title}
      </div>
      {children}
    </div>
  )
}

function FacetRow({
  active,
  label,
  onClick,
  children,
}: {
  active: boolean
  label: string
  onClick: () => void
  children: React.ReactNode
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <button
      type="button"
      aria-label={label}
      aria-pressed={active}
      onClick={onClick}
      className={css({
        display: 'flex',
        alignItems: 'center',
        gap: '8px',
        height: '30px',
        paddingLeft: '4px',
        paddingRight: '4px',
        borderRadius: '6px',
        width: '100%',
        border: 'none',
        backgroundColor: 'transparent',
        color: 'inherit',
        textAlign: 'left',
        cursor: 'pointer',
        ':hover': { backgroundColor: tok.hoverFill },
        ':focus-visible': { outline: `2px solid ${tok.accent}`, outlineOffset: '1px' },
      })}
    >
      {children}
    </button>
  )
}

const OPERATORS = ['repo:', 'lang:', 'file:', 'sym:', 'case:yes', '-', '"exact phrase"']

function SearchMeta({
  input,
  setInput,
  inputRef,
  phase,
  files,
  stats,
  repoCount,
  showNavigation,
}: {
  input: string
  setInput: (s: string) => void
  inputRef: React.RefObject<HTMLInputElement | null>
  phase: Phase
  files: FileResult[]
  stats: Stats | null
  repoCount: number
  showNavigation: boolean
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const matchCount = countMatches(files)
  const insert = (op: string) => {
    const sep = input && !input.endsWith(' ') ? ' ' : ''
    setInput(input + sep + op)
    inputRef.current?.focus()
  }
  return (
    <div className={css({ display: 'flex', alignItems: 'center', gap: '10px', marginTop: '8px', minHeight: '22px', '@media screen and (max-width: 900px)': { alignItems: 'flex-start', flexDirection: 'column' } })}>
      <div className={css({ display: 'flex', alignItems: 'center', gap: '6px', flexWrap: 'wrap' })}>
        {OPERATORS.map((op) => (
          <button
            key={op}
            type="button"
            onClick={() => insert(op)}
            className={css({
              fontFamily: FONTS.MONO,
              fontSize: '11px',
              lineHeight: '14px',
              padding: '3px 7px',
              borderRadius: '5px',
              border: 'none',
              backgroundColor: tok.fill,
              color: tok.textSecondary,
              cursor: 'pointer',
              ':hover': { backgroundColor: tok.hoverFill, color: tok.textPrimary },
              ':focus-visible': { outline: `2px solid ${tok.accent}`, outlineOffset: '1px' },
            })}
          >
            {op}
          </button>
        ))}
      </div>
      <div className={css({ flex: 1 })} />
      <div
        className={css({ display: 'flex', alignItems: 'center', gap: '7px', minHeight: '22px', fontSize: '12px', color: tok.textTertiary, whiteSpace: 'nowrap', '@media screen and (max-width: 720px)': { whiteSpace: 'normal', flexWrap: 'wrap' } })}
      >
        {phase === 'streaming' && (
          <span
            aria-hidden="true"
            className={css({
              width: '8px',
              height: '8px',
              flexShrink: 0,
              borderRadius: '50%',
              backgroundColor: tok.status.unavailable.solid,
              animationName: { '0%,100%': { opacity: 1 }, '50%': { opacity: 0.35 } },
              animationDuration: MOTION.pulse,
              animationIterationCount: 'infinite',
              [REDUCED_MOTION]: { animationName: 'none' },
            })}
          />
        )}
        {phase !== 'idle' && (
          <span role="status" aria-live="polite" aria-atomic="true" className={css({ fontVariantNumeric: 'tabular-nums' })}>
            <b className={css({ color: tok.textPrimary, fontWeight: 600 })}>{matchCount}</b> {matchCount === 1 ? 'match' : 'matches'} ·{' '}
            <b className={css({ color: tok.textPrimary, fontWeight: 600 })}>{files.length}</b> {files.length === 1 ? 'file' : 'files'}
            {stats ? ` · ${stats.duration_ms}ms · ${repoCount} ${repoCount === 1 ? 'repository' : 'repositories'}` : ''}
            {phase === 'streaming' ? ' · searching…' : ''}
            {phase === 'stopped' ? ' · stopped' : ''}
          </span>
        )}
        {showNavigation && (
          <>
            <span aria-hidden="true" className={css({ width: '1px', height: '12px', backgroundColor: tok.innerSep, '@media screen and (max-width: 720px)': { display: 'none' } })} />
            <span className={css({ display: 'flex', alignItems: 'center', gap: '5px', '@media screen and (max-width: 720px)': { display: 'none' } })}>
              <Kbd>j</Kbd><Kbd>k</Kbd> navigate
            </span>
          </>
        )}
        <a
          href="https://github.com/sourcegraph/zoekt/blob/main/doc/query_syntax.md"
          target="_blank"
          rel="noreferrer"
          className={css({ fontSize: '12px', color: tok.textTertiary, textDecoration: 'none', ':hover': { color: tok.accent } })}
        >
          Syntax ↗
        </a>
      </div>
    </div>
  )
}

function countMatches(files: FileResult[]): number {
  return files.reduce((n, f) => n + f.chunks.reduce((m, c) => m + c.ranges.length, 0), 0)
}

function RepoGroup({
  repo,
  files,
  indexedAt,
  animateCard,
  enteringFiles,
  open,
  onToggle,
  selectedKey,
  registerRef,
}: {
  repo: string
  files: FileResult[]
  indexedAt?: string
  animateCard: boolean
  enteringFiles: Set<string>
  open: boolean
  onToggle: () => void
  selectedKey: string
  registerRef: (key: string, el: HTMLDivElement | null) => void
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const matches = countMatches(files)
  return (
    <section
      className={css({
        border: `1px solid ${tok.cardBorder}`,
        borderRadius: '8px',
        marginBottom: '14px',
        overflow: 'hidden',
        backgroundColor: tok.pageBg,
        ...(animateCard ? {
          animationName: { from: { opacity: 0, transform: 'translateY(7px)' }, to: { opacity: 1, transform: 'translateY(0)' } },
          animationDuration: MOTION.element,
          animationTimingFunction: MOTION.easeOut,
          [REDUCED_MOTION]: { animationName: 'none' },
        } : {}),
      })}
    >
      <button
        type="button"
        aria-expanded={open}
        onClick={onToggle}
        className={css({
          display: 'flex',
          alignItems: 'center',
          gap: '7px',
          width: '100%',
          height: '38px',
          border: 'none',
          borderBottom: open ? `1px solid ${tok.innerSep}` : 'none',
          backgroundColor: tok.bandBg,
          paddingLeft: '12px',
          paddingRight: '12px',
          cursor: 'pointer',
          color: tok.textPrimary,
          textAlign: 'left',
          ':hover': { backgroundColor: tok.hoverFill },
          ':focus-visible': { outline: `2px solid ${tok.accent}`, outlineOffset: '-2px' },
        })}
      >
        <span className={css({ color: tok.textTertiary, display: 'flex' })}>
          {open ? <ChevronDown size={13} /> : <ChevronRight size={13} />}
        </span>
        <span className={css({ fontSize: '14px', fontWeight: 600, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' })}>{repo}</span>
        <span className={css({ fontSize: '12px', color: tok.textTertiary, whiteSpace: 'nowrap', fontVariantNumeric: 'tabular-nums' })}>
          {matches} {matches === 1 ? 'match' : 'matches'} · {files.length} {files.length === 1 ? 'file' : 'files'}
        </span>
        <span className={css({ flex: 1 })} />
        {indexedAt && (
          <span className={css({ fontSize: '11px', color: tok.gutter, whiteSpace: 'nowrap' })}>
            indexed {relTime(indexedAt)}
          </span>
        )}
      </button>
      {open &&
        files.map((f, index) => (
          <FileBlock
            key={fileKey(f)}
            file={f}
            first={index === 0}
            selected={fileKey(f) === selectedKey}
            animate={enteringFiles.has(fileKey(f)) && !animateCard}
            registerRef={registerRef}
          />
        ))}
    </section>
  )
}

function FileBlock({
  file,
  first,
  selected,
  animate,
  registerRef,
}: {
  file: FileResult
  first: boolean
  selected: boolean
  animate: boolean
  registerRef: (key: string, el: HTMLDivElement | null) => void
}) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const [lang, setLang] = useState<LanguageSupport | null>(null)
  useEffect(() => {
    let live = true
    languageFor(file.path).then((l) => live && setLang(l))
    return () => {
      live = false
    }
  }, [file.path])

  const firstLine = firstMatchLine(file)
  const matches = file.chunks.reduce((m, c) => m + c.ranges.length, 0)
  const slash = file.path.lastIndexOf('/')
  const dir = slash === -1 ? '' : file.path.slice(0, slash + 1)
  const name = slash === -1 ? file.path : file.path.slice(slash + 1)
  const fileHref = href('/file', {
    repo: file.repo,
    path: file.path,
    ref: file.ref,
    ...(firstLine ? { L: String(firstLine) } : {}),
  })

  return (
    <div
      ref={(el) => registerRef(fileKey(file), el)}
      className={css({
        borderTop: first ? 'none' : `1px solid ${tok.innerSep}`,
        backgroundColor: tok.pageBg,
        ...(animate ? {
          animationName: { from: { opacity: 0, transform: 'translateY(7px)' }, to: { opacity: 1, transform: 'translateY(0)' } },
          animationDuration: MOTION.element,
          animationTimingFunction: MOTION.easeOut,
          [REDUCED_MOTION]: { animationName: 'none' },
        } : {}),
      })}
    >
      <div className={css({ height: '32px', display: 'flex', alignItems: 'center', gap: '8px', paddingLeft: '12px', paddingRight: '10px', borderBottom: `1px solid ${tok.innerSep}` })}>
        <span className={css({ width: '8px', height: '8px', borderRadius: '2px', backgroundColor: langColor(file.path, tok), flexShrink: 0 })} />
        <a href={fileHref} className={css({ textDecoration: 'none', fontSize: '12.5px', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' })}>
          <span className={css({ color: tok.textTertiary })}>{dir}</span>
          <span className={css({ color: tok.textPrimary, fontWeight: 500 })}>{name}</span>
        </a>
        <div className={css({ flex: 1 })} />
        <span className={css({ fontSize: '11px', color: tok.textTertiary, whiteSpace: 'nowrap', fontVariantNumeric: 'tabular-nums' })}>{matches}</span>
        <CopyButton text={file.path} title="Copy path" />
        <a href={fileHref} title="Open file" className={css({ display: 'flex', color: tok.textTertiary, padding: '3px', borderRadius: '6px', ':hover': { color: tok.textPrimary, backgroundColor: tok.hoverFill } })}>
          <OpenIcon size={13} />
        </a>
      </div>
      {file.chunks.map((chunk, i) => (
        <ChunkView key={i} chunk={chunk} file={file} lang={lang} first={i === 0} selectedLine={selected ? firstLine : undefined} />
      ))}
    </div>
  )
}

function ChunkView({
  chunk,
  file,
  lang,
  first,
  selectedLine,
}: {
  chunk: FileResult['chunks'][number]
  file: FileResult
  lang: LanguageSupport | null
  first: boolean
  selectedLine?: number
}) {
  const [css] = useStyletron()
  const { mode } = useMode()
  const tok = usePhebsTokens()
  const lines = chunk.content.replace(/\n$/, '').split('\n')
  return (
    <div className={css({ borderTop: first ? 'none' : `1px solid ${tok.innerSep}`, paddingTop: '4px', paddingBottom: '4px' })}>
      {lines.map((line, i) => {
        const lineNo = chunk.start_line + i
        return (
          <div key={i} data-selected-line={lineNo === selectedLine ? 'true' : undefined} className={css({
            display: 'flex',
            fontFamily: FONTS.MONO,
            fontSize: '12.5px',
            lineHeight: '18px',
            backgroundColor: lineNo === selectedLine ? tok.selectedLineBg : 'transparent',
            boxShadow: lineNo === selectedLine ? `inset 2px 0 0 ${tok.accent}` : 'none',
            ':hover': { backgroundColor: lineNo === selectedLine ? tok.selectedLineBg : tok.hoverFill },
          })}>
            <a
              href={href('/file', {
                repo: file.repo,
                path: file.path,
                ref: file.ref,
                L: String(lineNo),
              })}
              className={css({ flexShrink: 0, width: '40px', paddingRight: '10px', textAlign: 'right', color: tok.gutter, textDecoration: 'none', userSelect: 'none', ':hover': { color: tok.accent } })}
            >
              {lineNo}
            </a>
            <code className={css({ flex: '1 1 0', minWidth: 0, whiteSpace: 'pre', overflowX: 'auto', tabSize: 4, color: tok.plainCode, paddingRight: '12px' })}>
              {renderLine(line, lineNo, chunk.ranges, lang, mode, tok.matchBg)}
            </code>
          </div>
        )
      })}
    </div>
  )
}

// renderLine tokenizes one source line and overlays match ranges, emitting
// styled spans (syntax color + match background where they intersect).
function renderLine(line: string, lineNo: number, ranges: Range[], lang: LanguageSupport | null, mode: 'light' | 'dark', matchBg: string) {
  const tokens = tokenize(line, lang, mode)
  const matches = matchSpans(line, lineNo, ranges)
  const nodes: React.ReactNode[] = []
  let key = 0
  for (const t of tokens) {
    const base = { color: t.color, fontStyle: t.fontStyle }
    if (matches.length === 0) {
      nodes.push(<span key={key++} style={base}>{line.slice(t.from, t.to)}</span>)
      continue
    }
    const cuts = new Set([t.from, t.to])
    for (const m of matches) {
      if (m.to > t.from && m.from < t.to) {
        cuts.add(Math.max(t.from, m.from))
        cuts.add(Math.min(t.to, m.to))
      }
    }
    const sorted = [...cuts].sort((a, b) => a - b)
    for (let i = 0; i < sorted.length - 1; i++) {
      const a = sorted[i]
      const b = sorted[i + 1]
      if (a === b) continue
      const isMatch = matches.some((m) => m.from <= a && b <= m.to)
      nodes.push(
        <span key={key++} style={isMatch ? { ...base, backgroundColor: matchBg, borderRadius: '2px' } : base}>
          {line.slice(a, b)}
        </span>,
      )
    }
  }
  return nodes
}

function matchSpans(line: string, lineNo: number, ranges: Range[]): { from: number; to: number }[] {
  const spans: { from: number; to: number }[] = []
  for (const r of ranges) {
    if (lineNo < r.start_line || lineNo > r.end_line) continue
    const from = lineNo === r.start_line ? runeColumnToUTF16Offset(line, r.start_col) : 0
    const to = lineNo === r.end_line ? runeColumnToUTF16Offset(line, r.end_col) : line.length
    spans.push({ from: Math.max(0, from), to: Math.min(line.length, to) })
  }
  return spans
}

function CopyButton({ text, title }: { text: string; title: string }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const [done, setDone] = useState(false)
  return (
    <button
      type="button"
      title={title}
      onClick={() => {
        navigator.clipboard?.writeText(text)
        setDone(true)
        setTimeout(() => setDone(false), 1200)
      }}
      className={css({ display: 'flex', border: 'none', background: 'none', cursor: 'pointer', color: done ? tok.status.current.solid : tok.textTertiary, padding: '3px', borderRadius: '6px', ':hover': { color: tok.textPrimary, backgroundColor: tok.hoverFill } })}
    >
      {done ? <CheckIcon size={13} /> : <CopyIcon size={13} />}
    </button>
  )
}

function SkeletonCards() {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const shimmer = css({
    backgroundImage: `linear-gradient(90deg, ${tok.fill} 0%, ${tok.cardBorder} 50%, ${tok.fill} 100%)`,
    backgroundSize: '200% 100%',
    animationName: { '0%': { backgroundPosition: '100% 0' }, '100%': { backgroundPosition: '0 0' } },
    animationDuration: MOTION.pulse,
    animationIterationCount: 'infinite',
    borderRadius: '4px',
    [REDUCED_MOTION]: { animationName: 'none' },
  })
  return (
    <div data-testid="streaming-skeleton" className={css({ border: `1px solid ${tok.cardBorder}`, borderRadius: '8px', marginBottom: '14px', overflow: 'hidden' })} aria-hidden="true">
      <div className={css({ height: '38px', display: 'flex', alignItems: 'center', paddingLeft: '12px', backgroundColor: tok.bandBg, borderBottom: `1px solid ${tok.innerSep}` })}>
        <div className={`${shimmer} ${css({ width: '200px', height: '11px' })}`} />
      </div>
      <div className={css({ display: 'grid', gap: '8px', padding: '10px 12px' })}>
        {[60, 80, 45].map((w, i) => (
          <div key={i} className={`${shimmer} ${css({ width: `${w}%`, height: '9px' })}`} />
        ))}
      </div>
    </div>
  )
}

// extLang maps a file extension to a lang: filter name when the API doesn't
// supply a language.
function extLang(path: string): string {
  const ext = path.slice(path.lastIndexOf('.') + 1).toLowerCase()
  const map: Record<string, string> = {
    go: 'go', ts: 'typescript', tsx: 'typescript', js: 'javascript', jsx: 'javascript',
    py: 'python', md: 'markdown', json: 'json', proto: 'protobuf', sh: 'shell', yaml: 'yaml', yml: 'yaml',
  }
  return map[ext] ?? ext ?? 'text'
}
