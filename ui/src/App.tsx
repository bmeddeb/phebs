import { lazy, Suspense, useEffect, useState, type ReactNode } from 'react'
import { useStyletron } from 'baseui'
import { Notification, KIND as NOTIFICATION_KIND } from 'baseui/notification'
import { FOCUS_SEARCH, useHashRoute } from './router'
import { FONTS, useMode, usePhebsTokens } from './theme'
import { LogoutIcon, MoonIcon, SunIcon } from './icons'
import { BrandLoader, BrandLockup } from './Brand'
import { ErrorNotice, LoadingBlock } from './components/kit'
import { ScopeContextBar } from './components/ScopeContextBar'
import { CommandNavigator, type NavigatorSurface } from './components/CommandNavigator'
import { rememberScope, scopeFromParams } from './scope'
import { useAuth } from './auth'
import { fetchVersion } from './api'
import { isAbortError } from './util'
import LoginPage from './pages/LoginPage'

const SearchPage = lazy(() => import('./pages/SearchPage'))
const FilePage = lazy(() => import('./pages/FilePage'))
const ReposPage = lazy(() => import('./pages/ReposPage'))
const ServiceDirectoryPage = lazy(() => import('./pages/ServiceDirectoryPage'))
const RelationshipExplorerPage = lazy(() => import('./pages/RelationshipExplorerPage'))
const SettingsPage = lazy(() => import('./pages/SettingsPage'))
const HistoryPage = lazy(() => import('./pages/HistoryPage'))
const BlamePage = lazy(() => import('./pages/BlamePage'))
const CommitPage = lazy(() => import('./pages/CommitPage'))
const AuditPage = lazy(() => import('./pages/AuditPage'))
const AnalyticsPage = lazy(() => import('./pages/AnalyticsPage'))
const ImpactPage = lazy(() => import('./pages/ImpactPage'))
const InvestigationPage = lazy(() => import('./pages/InvestigationPage'))
const WorkbenchPage = lazy(() => import('./pages/WorkbenchPage'))
const ContractAtlasPage = lazy(() => import('./pages/ContractAtlasPage'))
const CallerMapPage = lazy(() => import('./pages/CallerMapPage'))
const CallerComparisonPage = lazy(() => import('./pages/CallerComparisonPage'))
const KafkaTopicsPage = lazy(() => import('./pages/KafkaTopicsPage'))

export default function App() {
  const [path, params] = useHashRoute()
  // T43.8: one exact scope, read from the URL on every route (including the
  // Workbench's own param spelling). The bar persists it across surfaces;
  // clearing is an explicit bar action.
  const scope = scopeFromParams(params)
  const scopeServiceKey = scope?.serviceKey ?? ''

  // Every route names itself (audit F38); the scoped service joins the title.
  useEffect(() => {
    const title = routeTitle(path)
    document.title = scopeServiceKey
      ? `phebs · ${title} · ${scopeServiceKey}`
      : `phebs · ${title}`
  }, [path, scopeServiceKey])
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const { status, loading, error: authError, logout } = useAuth()
  const [capabilities, setCapabilities] = useState<string[]>([])
  const [capabilitiesLoaded, setCapabilitiesLoaded] = useState(false)
  const [capabilitiesError, setCapabilitiesError] = useState(false)
  const [capabilitiesAttempt, setCapabilitiesAttempt] = useState(0)
  const [navigatorOpen, setNavigatorOpen] = useState(false)

  // T43.9: Cmd/Ctrl+K opens the keyboard navigator; the existing '/' search
  // focus stays untouched.
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key.toLowerCase() !== 'k' || !(event.metaKey || event.ctrlKey)) return
      event.preventDefault()
      setNavigatorOpen((open) => !open)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  // Recently seen scope identities feed the navigator (identity only, never
  // authority; bounded in rememberScope).
  useEffect(() => {
    rememberScope(scope)
    // Identity fields are the real dependencies; the object is re-derived
    // every render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [scope?.repository, scope?.serviceKey, scope?.generation])

  // "/" focuses search from anywhere (unless already typing)
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== '/' || e.metaKey || e.ctrlKey) return
      const el = document.activeElement
      const typing = el instanceof HTMLElement && (el.tagName === 'INPUT' || el.tagName === 'TEXTAREA' || el.isContentEditable)
      if (typing) return
      e.preventDefault()
      window.dispatchEvent(new CustomEvent(FOCUS_SEARCH))
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  useEffect(() => {
    if (!status?.authenticated) return
    const controller = new AbortController()
    let active = true
    setCapabilities([])
    setCapabilitiesLoaded(false)
    setCapabilitiesError(false)
    fetchVersion(controller.signal)
      .then((info) => {
        if (active) setCapabilities(info.capabilities ?? [])
      })
      .catch((cause) => {
        if (active && !isAbortError(cause)) setCapabilitiesError(true)
      })
      .finally(() => {
        if (active) setCapabilitiesLoaded(true)
      })
    return () => {
      active = false
      controller.abort()
    }
  }, [status?.authenticated, capabilitiesAttempt])

  if (loading) {
    return <div className={css({ minHeight: '100vh', display: 'grid', placeItems: 'center', backgroundColor: tok.pageBg })}><BrandLoader /></div>
  }
  if (!status || (status.auth_required && !status.authenticated)) return <LoginPage />

  const impactAvailable = capabilities.includes('contract-impact-report')
  const contractsAvailable = capabilities.includes('contract-atlas')
  const callerMapAvailable = capabilities.includes('contract-caller-map')
  const callerComparisonAvailable = capabilities.includes('contract-caller-comparison')
  const compatibilityAvailable = capabilities.includes('contract-compatibility')
  const investigationsAvailable = capabilities.includes('investigation-core-views')
  const workbenchAvailable = capabilities.includes('change-workbench')
  const workbenchEvidenceAvailable =
    capabilities.includes('change-workbench-evidence')
  const topicsAvailable = capabilities.includes('kafka-topic-usage')
  const servicesAvailable = capabilities.includes('service-catalog-v2')
  const serviceRelationshipsAvailable = capabilities.includes('service-relationships-v1')
  // Capability-gated prefixes never fall through to Search: absent capability
  // renders a terminal boundary page under the original URL. A failed read is
  // distinct: transport failure cannot establish capability absence.
  const gate = (available: boolean, label: string, render: () => ReactNode) => (
    <CapabilityGate
      loaded={capabilitiesLoaded}
      error={capabilitiesError}
      available={available}
      label={label}
      path={path}
      onRetry={() => setCapabilitiesAttempt((attempt) => attempt + 1)}
      render={render}
    />
  )
  const navigatorSurfaces: NavigatorSurface[] = [
    { label: 'Search', path: '/', available: true },
    { label: 'Repositories', path: '/repos', available: true },
    { label: 'Service directory', path: '/services', available: servicesAvailable },
    { label: 'Relationship explorer', path: '/relationships', available: serviceRelationshipsAvailable },
    { label: 'Contract atlas', path: '/contracts', available: contractsAvailable },
    { label: 'Contract impact', path: '/impact', available: impactAvailable },
    { label: 'Kafka topics', path: '/topics', available: topicsAvailable },
    { label: 'Investigations', path: '/investigations', available: investigationsAvailable },
    { label: 'Change workbench', path: '/workbench', available: workbenchAvailable },
    { label: 'Audit log', path: '/audit', available: status.user?.is_admin === true },
    { label: 'Analytics', path: '/analytics', available: status.user?.is_admin === true },
    { label: 'Settings', path: '/settings', available: true },
  ]
  let page
  if (path.startsWith('/file')) page = <FilePage params={params} />
  else if (path.startsWith('/history')) page = <HistoryPage params={params} />
  else if (path.startsWith('/blame')) page = <BlamePage params={params} />
  else if (path.startsWith('/commit')) page = <CommitPage params={params} />
  else if (path.startsWith('/repos')) page = <ReposPage isAdmin={status.user?.is_admin === true} serviceDirectoryAvailable={servicesAvailable} />
  else if (path.startsWith('/services')) page = gate(servicesAvailable, 'The service directory', () => <ServiceDirectoryPage params={params} relationshipsAvailable={serviceRelationshipsAvailable} />)
  else if (path.startsWith('/relationships')) page = gate(serviceRelationshipsAvailable, 'The relationship explorer', () => <RelationshipExplorerPage params={params} />)
  else if (path.startsWith('/audit')) page = <AuditPage isAdmin={status.user?.is_admin === true} />
  else if (path.startsWith('/analytics')) page = <AnalyticsPage isAdmin={status.user?.is_admin === true} />
  else if (path.startsWith('/contracts')) page = gate(contractsAvailable, 'The contract atlas', () => <ContractAtlasPage params={params} callerMapAvailable={callerMapAvailable} workbenchAvailable={workbenchAvailable} />)
  else if (path.startsWith('/callers')) page = gate(callerMapAvailable, 'The caller map', () => <CallerMapPage params={params} comparisonAvailable={callerComparisonAvailable} />)
  else if (path.startsWith('/compare-callers')) page = gate(callerComparisonAvailable, 'Caller comparison', () => <CallerComparisonPage params={params} />)
  else if (path.startsWith('/impact')) page = gate(impactAvailable, 'The impact report', () => <ImpactPage params={params} compatibilityAvailable={compatibilityAvailable} capabilities={capabilities} />)
  else if (path.startsWith('/topics')) page = gate(topicsAvailable, 'Kafka topic usage', () => <KafkaTopicsPage params={params} />)
  else if (path.startsWith('/investigations')) page = gate(investigationsAvailable, 'Investigations', () => <InvestigationPage params={params} />)
  else if (path.startsWith('/workbench')) page = gate(workbenchAvailable, 'The change workbench', () => <WorkbenchPage params={params} evidenceAvailable={workbenchEvidenceAvailable} />)
  else if (path.startsWith('/settings')) page = <SettingsPage isAdmin={status.user?.is_admin === true} />
  else page = <SearchPage params={params} />

  const compactMain = path.startsWith('/file') || path.startsWith('/repos') || path.startsWith('/services') || path.startsWith('/relationships')

  return (
    <div className={css({ minHeight: '100vh', backgroundColor: tok.pageBg })}>
      <Header path={path} email={status.user?.email ?? ''} isAdmin={status.user?.is_admin === true} contractsAvailable={contractsAvailable} impactAvailable={impactAvailable} topicsAvailable={topicsAvailable} investigationsAvailable={investigationsAvailable} workbenchAvailable={workbenchAvailable} onLogout={() => void logout().catch(() => {})} />
      {scope && <ScopeContextBar scope={scope} path={path} params={params} />}
      {navigatorOpen && (
        <CommandNavigator
          surfaces={navigatorSurfaces}
          scope={scope}
          onClose={() => setNavigatorOpen(false)}
        />
      )}
      <main
        className={css({
          width: '100%',
          maxWidth: '100%',
          boxSizing: 'border-box',
          margin: '0 auto',
          paddingLeft: '20px',
          paddingRight: '20px',
          paddingTop: compactMain ? '16px' : '20px',
          paddingBottom: compactMain ? '36px' : '40px',
          '@media screen and (max-width: 720px)': {
            paddingLeft: '16px',
            paddingRight: '16px',
            paddingTop: '16px',
            paddingBottom: '32px',
          },
        })}
      >
        {authError && (
          <Notification kind={NOTIFICATION_KIND.negative} overrides={{ Body: { style: { width: 'auto', marginTop: 0 } } }}>
            {authError}
          </Notification>
        )}
        <Suspense fallback={<LoadingBlock label="Loading page…" />}>{page}</Suspense>
      </main>
    </div>
  )
}

function routeTitle(path: string): string {
  if (path.startsWith('/repos')) return 'Repositories'
  if (path.startsWith('/services')) return 'Service directory'
  if (path.startsWith('/relationships')) return 'Relationship explorer'
  if (path.startsWith('/file')) return 'File'
  if (path.startsWith('/history')) return 'History'
  if (path.startsWith('/blame')) return 'Blame'
  if (path.startsWith('/commit')) return 'Commit'
  if (path.startsWith('/contracts')) return 'Contract atlas'
  if (path.startsWith('/callers')) return 'Caller map'
  if (path.startsWith('/compare-callers')) return 'Caller comparison'
  if (path.startsWith('/impact')) return 'Contract impact'
  if (path.startsWith('/topics')) return 'Kafka topics'
  if (path.startsWith('/investigations')) return 'Investigations'
  if (path.startsWith('/workbench')) return 'Change workbench'
  if (path.startsWith('/audit')) return 'Audit log'
  if (path.startsWith('/analytics')) return 'Analytics'
  if (path.startsWith('/settings')) return 'Settings'
  return 'Search'
}

export function CapabilityGate({ loaded, error, available, label, path, onRetry, render }: {
  loaded: boolean
  error: boolean
  available: boolean
  label: string
  path: string
  onRetry: () => void
  render: () => ReactNode
}) {
  const [css] = useStyletron()
  if (!loaded) return <LoadingBlock label="Checking instance capabilities…" />
  if (error) {
    return (
      <div className={css({ maxWidth: '520px', margin: '56px auto 0' })}>
        <ErrorNotice onRetry={onRetry} retryLabel="Retry capability check">
          Instance capability check failed. No capability absence is inferred.
        </ErrorNotice>
      </div>
    )
  }
  return available ? render() : <CapabilityUnavailablePage label={label} path={path} />
}

function CapabilityUnavailablePage({ label, path }: { label: string; path: string }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <section aria-labelledby="capability-unavailable-heading" className={css({ maxWidth: '520px', margin: '56px auto 0' })}>
      <h1 id="capability-unavailable-heading" className={css({ margin: 0, color: tok.textPrimary, fontSize: '18px', lineHeight: '26px', fontWeight: 600 })}>
        {label} is not available on this instance
      </h1>
      <p className={css({ margin: '10px 0 0', color: tok.textSecondary, fontSize: '13px', lineHeight: '20px' })}>
        This deployment does not expose the capability that{' '}
        <code className={css({ fontFamily: FONTS.MONO, fontSize: '12px' })}>{path}</code>{' '}
        requires, so this page cannot render. This is a capability boundary of
        the instance, not a claim about the underlying evidence.
      </p>
      <a
        href="#/"
        className={css({ display: 'inline-block', marginTop: '14px', color: tok.textPrimary, fontSize: '13px', fontWeight: 600, textDecoration: 'underline', ':focus-visible': { outline: `2px solid ${tok.textPrimary}`, outlineOffset: '2px' } })}
      >
        Go to Search
      </a>
    </section>
  )
}

export function Header({ path, email, isAdmin, contractsAvailable, impactAvailable, topicsAvailable, investigationsAvailable, workbenchAvailable, onLogout }: { path: string; email: string; isAdmin: boolean; contractsAvailable: boolean; impactAvailable: boolean; topicsAvailable: boolean; investigationsAvailable: boolean; workbenchAvailable: boolean; onLogout: () => void }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const { mode, toggle } = useMode()

  const isSettings = path.startsWith('/settings')
  const isRepos = path.startsWith('/repos') || path.startsWith('/services') || path.startsWith('/relationships')
  const isAudit = path.startsWith('/audit')
  const isAnalytics = path.startsWith('/analytics')
  const isImpact = path.startsWith('/impact') || path.startsWith('/callers') ||
    path.startsWith('/compare-callers')
  const isTopics = path.startsWith('/topics')
  const isContracts = path.startsWith('/contracts')
  const isInvestigations = path.startsWith('/investigations')
  const isWorkbench = path.startsWith('/workbench')
  const isSearch = path === '/' || path.startsWith('/search')

  return (
    <header
      className={css({
        height: '52px',
        display: 'flex',
        alignItems: 'center',
        gap: '20px',
        paddingLeft: '20px',
        paddingRight: '20px',
        borderBottom: `1px solid ${tok.cardBorder}`,
        backgroundColor: tok.pageBg,
        position: 'sticky',
        top: 0,
        zIndex: 10,
        '@media screen and (max-width: 720px)': {
          gap: '10px',
          paddingLeft: '16px',
          paddingRight: '16px',
        },
      })}
    >
      <BrandLockup href="#/" markSize={18} wordmarkSize={17} gap={8} />
      <nav className={css({
        display: 'flex',
        gap: '18px',
        alignItems: 'center',
        height: '100%',
        '@media screen and (max-width: 720px)': {
          flex: '1 1 auto',
          minWidth: 0,
          gap: '12px',
          overflowX: 'auto',
          overflowY: 'hidden',
          scrollbarWidth: 'none',
        },
      })}>
        <NavLink href="#/" label="Search" active={isSearch} />
        <NavLink href="#/repos" label="Repos" active={isRepos} />
        {contractsAvailable && <NavLink href="#/contracts" label="Contracts" active={isContracts} />}
        {impactAvailable && <NavLink href="#/impact" label="Impact" active={isImpact} />}
        {topicsAvailable && <NavLink href="#/topics" label="Topics" active={isTopics} />}
        {investigationsAvailable && <NavLink href="#/investigations" label="Investigations" active={isInvestigations} />}
        {workbenchAvailable && <NavLink href="#/workbench" label="Workbench" active={isWorkbench} />}
        {isAdmin && <NavLink href="#/audit" label="Audit" active={isAudit} />}
        {isAdmin && <NavLink href="#/analytics" label="Analytics" active={isAnalytics} />}
        <NavLink href="#/settings" label="Settings" active={isSettings} />
      </nav>
      <div className={css({ flex: 1, '@media screen and (max-width: 720px)': { display: 'none' } })} />
      {email && <span className={css({ fontSize: '12px', color: tok.textTertiary, maxWidth: '180px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', '@media screen and (max-width: 720px)': { display: 'none' } })}>{email}</span>}
      <button
        onClick={toggle}
        aria-label={mode === 'dark' ? 'Switch to light theme' : 'Switch to dark theme'}
        className={css({
          width: '28px',
          height: '28px',
          boxSizing: 'border-box',
          flexShrink: 0,
          padding: 0,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          border: `1px solid ${tok.cardBorder}`,
          borderRadius: '7px',
          background: 'none',
          cursor: 'pointer',
          color: tok.textSecondary,
          ':hover': { backgroundColor: tok.hoverFill },
          ':focus-visible': { outline: `2px solid ${tok.accent}`, outlineOffset: '1px' },
        })}
      >
        {mode === 'dark' ? <SunIcon size={14} /> : <MoonIcon size={14} />}
      </button>
      <button
        onClick={onLogout}
        aria-label="Sign out"
        title="Sign out"
        className={css({
          width: '28px',
          height: '28px',
          boxSizing: 'border-box',
          flexShrink: 0,
          padding: 0,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          border: `1px solid ${tok.cardBorder}`,
          borderRadius: '7px',
          background: 'none',
          cursor: 'pointer',
          color: tok.textSecondary,
          ':hover': { backgroundColor: tok.hoverFill },
          ':focus-visible': { outline: `2px solid ${tok.accent}`, outlineOffset: '1px' },
        })}
      >
        <LogoutIcon size={14} />
      </button>
    </header>
  )
}

function NavLink({ href, label, active }: { href: string; label: string; active: boolean }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  return (
    <a
      href={href}
      aria-current={active ? 'page' : undefined}
      className={css({
        height: '100%',
        display: 'flex',
        alignItems: 'center',
        fontSize: '13px',
        fontWeight: active ? 500 : 400,
        color: active ? tok.textPrimary : tok.textTertiary,
        textDecoration: 'none',
        boxShadow: active ? `inset 0 -2px 0 ${tok.textPrimary}` : 'none',
        ':hover': { color: tok.textPrimary },
        ':focus-visible': { outline: `2px solid ${tok.accent}`, outlineOffset: '-2px' },
      })}
    >
      {label}
    </a>
  )
}
