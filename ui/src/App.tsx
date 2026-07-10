import { useStyletron } from 'baseui'
import { HeadingXSmall } from 'baseui/typography'
import { StyledLink } from 'baseui/link'
import { useHashRoute } from './router'
import SearchPage from './pages/SearchPage'
import FilePage from './pages/FilePage'
import ReposPage from './pages/ReposPage'

export default function App() {
  const [path, params] = useHashRoute()
  const [css, theme] = useStyletron()

  let page
  if (path.startsWith('/file')) page = <FilePage params={params} />
  else if (path.startsWith('/repos')) page = <ReposPage />
  else page = <SearchPage params={params} />

  return (
    <div>
      <header
        className={css({
          display: 'flex',
          alignItems: 'center',
          gap: theme.sizing.scale800,
          paddingLeft: theme.sizing.scale800,
          paddingRight: theme.sizing.scale800,
          paddingTop: theme.sizing.scale500,
          paddingBottom: theme.sizing.scale500,
          borderBottom: `1px solid ${theme.colors.borderOpaque}`,
          backgroundColor: theme.colors.backgroundPrimary,
        })}
      >
        <HeadingXSmall margin={0}>
          <a href="#/" className={css({ color: 'inherit', textDecoration: 'none' })}>
            phebs
          </a>
        </HeadingXSmall>
        <nav className={css({ display: 'flex', gap: theme.sizing.scale600 })}>
          <StyledLink href="#/">Search</StyledLink>
          <StyledLink href="#/repos">Repos</StyledLink>
        </nav>
      </header>
      <main
        className={css({
          maxWidth: '1080px',
          margin: '0 auto',
          paddingLeft: theme.sizing.scale800,
          paddingRight: theme.sizing.scale800,
          paddingTop: theme.sizing.scale800,
          paddingBottom: theme.sizing.scale800,
        })}
      >
        {page}
      </main>
    </div>
  )
}
