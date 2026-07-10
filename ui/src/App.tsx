import { useEffect } from 'react'
import { useStyletron } from 'baseui'
import { useHashRoute } from './router'
import { useMode, usePhebsTokens } from './theme'
import { SunIcon, MoonIcon } from './icons'
import SearchPage from './pages/SearchPage'
import FilePage from './pages/FilePage'
import ReposPage from './pages/ReposPage'

// FOCUS_SEARCH lets the global "/" shortcut reach the search input without
// threading a ref through the router.
export const FOCUS_SEARCH = 'phebs-focus-search'

export default function App() {
  const [path, params] = useHashRoute()
  const [css] = useStyletron()
  const tok = usePhebsTokens()

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

  let page
  if (path.startsWith('/file')) page = <FilePage params={params} />
  else if (path.startsWith('/repos')) page = <ReposPage />
  else page = <SearchPage params={params} />

  const wide = !path.startsWith('/repos') // all pages full-width; kept for future narrowing

  return (
    <div className={css({ minHeight: '100vh', backgroundColor: tok.pageBg })}>
      <Header path={path} />
      <main
        className={css({
          maxWidth: wide ? '100%' : '1080px',
          margin: '0 auto',
          paddingLeft: '24px',
          paddingRight: '24px',
          paddingTop: '24px',
          paddingBottom: '48px',
        })}
      >
        {page}
      </main>
    </div>
  )
}

function Header({ path }: { path: string }) {
  const [css] = useStyletron()
  const tok = usePhebsTokens()
  const { mode, toggle } = useMode()

  const isRepos = path.startsWith('/repos')
  const isSearch = !isRepos && !path.startsWith('/file')

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
      })}
    >
      <a
        href="#/"
        className={css({
          fontSize: '20px',
          fontWeight: 700,
          color: tok.textPrimary,
          textDecoration: 'none',
          letterSpacing: '-0.01em',
        })}
      >
        phebs
      </a>
      <nav className={css({ display: 'flex', gap: '20px', alignItems: 'center', height: '100%' })}>
        <NavLink href="#/" label="Search" active={isSearch} />
        <NavLink href="#/repos" label="Repos" active={isRepos} />
      </nav>
      <div className={css({ flex: 1 })} />
      <span
        className={css({
          fontSize: '12px',
          color: tok.textTertiary,
          display: 'flex',
          alignItems: 'center',
          gap: '6px',
        })}
      >
        <kbd
          className={css({
            fontFamily: 'ui-monospace, Menlo, monospace',
            fontSize: '11px',
            padding: '2px 6px',
            border: `1px solid ${tok.kbdBorder}`,
            borderRadius: '4px',
            color: tok.textSecondary,
          })}
        >
          /
        </kbd>
        to search
      </span>
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
