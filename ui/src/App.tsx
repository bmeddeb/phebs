import { lazy, Suspense, useEffect } from 'react'
import { useStyletron } from 'baseui'
import { Spinner } from 'baseui/spinner'
import { Notification, KIND as NOTIFICATION_KIND } from 'baseui/notification'
import { FOCUS_SEARCH, useHashRoute } from './router'
import { useMode, usePhebsTokens } from './theme'
import { LogoutIcon, MoonIcon, SunIcon } from './icons'
import { useAuth } from './auth'
import LoginPage from './pages/LoginPage'

const SearchPage = lazy(() => import('./pages/SearchPage'))
const FilePage = lazy(() => import('./pages/FilePage'))
const ReposPage = lazy(() => import('./pages/ReposPage'))
const SettingsPage = lazy(() => import('./pages/SettingsPage'))
const HistoryPage = lazy(() => import('./pages/HistoryPage'))
const BlamePage = lazy(() => import('./pages/BlamePage'))
const CommitPage = lazy(() => import('./pages/CommitPage'))

export default function App() {
  const [path, params] = useHashRoute()
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const { status, loading, error: authError, logout } = useAuth()

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

  if (loading) {
    return <div className={css({ minHeight: '100vh', display: 'grid', placeItems: 'center', backgroundColor: tok.pageBg })}><Spinner $size="small" /></div>
  }
  if (!status || (status.auth_required && !status.authenticated)) return <LoginPage />

  let page
  if (path.startsWith('/file')) page = <FilePage params={params} />
  else if (path.startsWith('/history')) page = <HistoryPage params={params} />
  else if (path.startsWith('/blame')) page = <BlamePage params={params} />
  else if (path.startsWith('/commit')) page = <CommitPage params={params} />
  else if (path.startsWith('/repos')) page = <ReposPage isAdmin={status.user?.is_admin === true} />
  else if (path.startsWith('/settings')) page = <SettingsPage />
  else page = <SearchPage params={params} />

  const wide = !path.startsWith('/repos') // all pages full-width; kept for future narrowing

  return (
    <div className={css({ minHeight: '100vh', backgroundColor: tok.pageBg })}>
      <Header path={path} email={status.user?.email ?? ''} onLogout={() => void logout().catch(() => {})} />
      <main
        className={css({
          maxWidth: wide ? '100%' : '1080px',
          margin: '0 auto',
          paddingLeft: '24px',
          paddingRight: '24px',
          paddingTop: '24px',
          paddingBottom: '48px',
          '@media screen and (max-width: 720px)': {
            paddingLeft: '16px',
            paddingRight: '16px',
            paddingTop: '16px',
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

function Header({ path, email, onLogout }: { path: string; email: string; onLogout: () => void }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const { mode, toggle } = useMode()

  const isSettings = path.startsWith('/settings')
  const isRepos = path.startsWith('/repos')
  const isSearch = path === '/' || path.startsWith('/search')

  return (
    <header
      className={css({
        height: '56px',
        display: 'flex',
        alignItems: 'center',
        gap: '24px',
        paddingLeft: '24px',
        paddingRight: '24px',
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
      <a
        href="#/"
        className={css({
          fontSize: '20px',
          fontWeight: 700,
          color: tok.textPrimary,
          textDecoration: 'none',
          letterSpacing: '0',
        })}
      >
        phebs
      </a>
      <nav className={css({ display: 'flex', gap: '20px', alignItems: 'center', height: '100%', '@media screen and (max-width: 720px)': { gap: '12px' } })}>
        <NavLink href="#/" label="Search" active={isSearch} />
        <NavLink href="#/repos" label="Repos" active={isRepos} />
        <NavLink href="#/settings" label="Settings" active={isSettings} />
      </nav>
      <div className={css({ flex: 1 })} />
      {email && <span className={css({ fontSize: '12px', color: tok.textTertiary, maxWidth: '180px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', '@media screen and (max-width: 720px)': { display: 'none' } })}>{email}</span>}
      <button
        onClick={toggle}
        aria-label={mode === 'dark' ? 'Switch to light theme' : 'Switch to dark theme'}
        className={css({
          width: '32px',
          height: '32px',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          border: `1px solid ${tok.cardBorder}`,
          borderRadius: '8px',
          background: 'none',
          cursor: 'pointer',
          color: tok.textSecondary,
          ':hover': { backgroundColor: tok.hoverFill },
        })}
      >
        {mode === 'dark' ? <SunIcon /> : <MoonIcon />}
      </button>
      <button
        onClick={onLogout}
        aria-label="Sign out"
        title="Sign out"
        className={css({
          width: '32px',
          height: '32px',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          border: `1px solid ${tok.cardBorder}`,
          borderRadius: '8px',
          background: 'none',
          cursor: 'pointer',
          color: tok.textSecondary,
          ':hover': { backgroundColor: tok.hoverFill },
        })}
      >
        <LogoutIcon />
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
        fontSize: '14px',
        fontWeight: active ? 500 : 400,
        color: active ? tok.textPrimary : tok.textTertiary,
        textDecoration: 'none',
        boxShadow: active ? `inset 0 -2px 0 ${tok.textPrimary}` : 'none',
        ':hover': { color: tok.textPrimary },
      })}
    >
      {label}
    </a>
  )
}
