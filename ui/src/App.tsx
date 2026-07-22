import { lazy, Suspense, useEffect, useState } from 'react'
import { useStyletron } from 'baseui'
import { Spinner } from 'baseui/spinner'
import { Notification, KIND as NOTIFICATION_KIND } from 'baseui/notification'
import { FOCUS_SEARCH, useHashRoute } from './router'
import { useMode, usePhebsTokens } from './theme'
import { LogoutIcon, MoonIcon, SunIcon } from './icons'
import { BrandLoader, BrandLockup } from './Brand'
import { useAuth } from './auth'
import { fetchVersion } from './api'
import { isAbortError } from './util'
import LoginPage from './pages/LoginPage'

const SearchPage = lazy(() => import('./pages/SearchPage'))
const FilePage = lazy(() => import('./pages/FilePage'))
const ReposPage = lazy(() => import('./pages/ReposPage'))
const SettingsPage = lazy(() => import('./pages/SettingsPage'))
const HistoryPage = lazy(() => import('./pages/HistoryPage'))
const BlamePage = lazy(() => import('./pages/BlamePage'))
const CommitPage = lazy(() => import('./pages/CommitPage'))
const AuditPage = lazy(() => import('./pages/AuditPage'))
const AnalyticsPage = lazy(() => import('./pages/AnalyticsPage'))
const ImpactPage = lazy(() => import('./pages/ImpactPage'))

export default function App() {
  const [path, params] = useHashRoute()
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const { status, loading, error: authError, logout } = useAuth()
  const [capabilities, setCapabilities] = useState<string[]>([])
  const [capabilitiesLoaded, setCapabilitiesLoaded] = useState(false)

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
    setCapabilitiesLoaded(false)
    fetchVersion(controller.signal)
      .then((info) => {
        if (active) setCapabilities(info.capabilities ?? [])
      })
      .catch((cause) => {
        if (active && !isAbortError(cause)) setCapabilities([])
      })
      .finally(() => {
        if (active) setCapabilitiesLoaded(true)
      })
    return () => {
      active = false
      controller.abort()
    }
  }, [status?.authenticated])

  if (loading) {
    return <div className={css({ minHeight: '100vh', display: 'grid', placeItems: 'center', backgroundColor: tok.pageBg })}><BrandLoader /></div>
  }
  if (!status || (status.auth_required && !status.authenticated)) return <LoginPage />

  const impactAvailable = capabilities.includes('contract-impact-report')
  const compatibilityAvailable = capabilities.includes('contract-compatibility')
  let page
  if (path.startsWith('/file')) page = <FilePage params={params} />
  else if (path.startsWith('/history')) page = <HistoryPage params={params} />
  else if (path.startsWith('/blame')) page = <BlamePage params={params} />
  else if (path.startsWith('/commit')) page = <CommitPage params={params} />
  else if (path.startsWith('/repos')) page = <ReposPage isAdmin={status.user?.is_admin === true} />
  else if (path.startsWith('/audit')) page = <AuditPage isAdmin={status.user?.is_admin === true} />
  else if (path.startsWith('/analytics')) page = <AnalyticsPage isAdmin={status.user?.is_admin === true} />
  else if (path.startsWith('/impact') && !capabilitiesLoaded) page = <Spinner $size="small" />
  else if (path.startsWith('/impact') && impactAvailable) page = <ImpactPage params={params} compatibilityAvailable={compatibilityAvailable} />
  else if (path.startsWith('/settings')) page = <SettingsPage />
  else page = <SearchPage params={params} />

  const compactMain = path.startsWith('/file') || path.startsWith('/repos')

  return (
    <div className={css({ minHeight: '100vh', backgroundColor: tok.pageBg })}>
      <Header path={path} email={status.user?.email ?? ''} isAdmin={status.user?.is_admin === true} impactAvailable={impactAvailable} onLogout={() => void logout().catch(() => {})} />
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
        <Suspense fallback={<Spinner $size="small" />}>{page}</Suspense>
      </main>
    </div>
  )
}

function Header({ path, email, isAdmin, impactAvailable, onLogout }: { path: string; email: string; isAdmin: boolean; impactAvailable: boolean; onLogout: () => void }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const { mode, toggle } = useMode()

  const isSettings = path.startsWith('/settings')
  const isRepos = path.startsWith('/repos')
  const isAudit = path.startsWith('/audit')
  const isAnalytics = path.startsWith('/analytics')
  const isImpact = path.startsWith('/impact')
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
        {impactAvailable && <NavLink href="#/impact" label="Impact" active={isImpact} />}
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
