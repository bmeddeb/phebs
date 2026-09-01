import { readFileSync } from 'node:fs'
import type { Page, Route } from '@playwright/test'
import { MARKDOWN_PREVIEW_FIXTURE, type ReceiptRoute } from './routes'

const MARKDOWN_PREVIEW_SOURCE = readFileSync(
  new URL('./fixtures/markdown-preview.md', import.meta.url),
  'utf8',
)

async function fulfillJSON(route: Route, value: unknown, status = 200) {
  await route.fulfill({
    status,
    contentType: 'application/json; charset=utf-8',
    body: JSON.stringify(value),
  })
}

function isFixtureIdentity(url: URL): boolean {
  return url.searchParams.get('repo') === MARKDOWN_PREVIEW_FIXTURE.repository
}

async function installMarkdownPreviewFixture(page: Page) {
  await page.route('**/api/**', async (route) => {
    const url = new URL(route.request().url())

    // The setup project validates a real authenticated session before any
    // receipt runs. Pin the presentation identity inside this one page-scoped
    // fixture so reviewed pixels never inherit a developer's personal email.
    if (url.pathname === '/api/auth/status') {
      await fulfillJSON(route, {
        authenticated: true,
        auth_required: true,
        setup_required: false,
        oidc_enabled: false,
        password_enabled: true,
        user: {
          id: 'receipt-operator',
          email: 'ux-audit@localhost.test',
          display_name: 'UX audit',
          is_admin: true,
        },
      })
      return
    }

    // Keep repository metadata isolated from the instance too. FilePage has
    // an explicit fixture revision, so an empty status list is the exact
    // terminal response and cannot borrow live index metadata.
    if (url.pathname === '/api/repo-status') {
      await fulfillJSON(route, [])
      return
    }

    if (url.pathname === '/api/source' && isFixtureIdentity(url)) {
      const exact =
        url.searchParams.get('path') === MARKDOWN_PREVIEW_FIXTURE.path &&
        url.searchParams.get('ref') === MARKDOWN_PREVIEW_FIXTURE.revision
      if (!exact) {
        await fulfillJSON(route, { error: 'unexpected markdown receipt source identity' }, 404)
        return
      }
      await fulfillJSON(route, {
        content: MARKDOWN_PREVIEW_SOURCE,
        encoding: 'utf8',
        size: Buffer.byteLength(MARKDOWN_PREVIEW_SOURCE),
      })
      return
    }

    if (url.pathname === '/api/folder_contents' && isFixtureIdentity(url)) {
      const exact =
        (url.searchParams.get('path') ?? '') === '' &&
        url.searchParams.get('ref') === MARKDOWN_PREVIEW_FIXTURE.revision
      if (!exact) {
        await fulfillJSON(route, { error: 'unexpected markdown receipt folder identity' }, 404)
        return
      }
      await fulfillJSON(route, {
        entries: [{
          name: MARKDOWN_PREVIEW_FIXTURE.path,
          type: 'file',
          size: Buffer.byteLength(MARKDOWN_PREVIEW_SOURCE),
        }],
      })
      return
    }

    // Any other request carrying the synthetic repository is a harness drift,
    // not permission to fall through to a live backend with invented state.
    if (isFixtureIdentity(url)) {
      await fulfillJSON(route, { error: 'unexpected markdown receipt fixture request' }, 404)
      return
    }

    await route.continue()
  })
}

export async function installReceiptFixture(page: Page, fixture: ReceiptRoute['fixture']) {
  if (fixture === 'markdown-preview') await installMarkdownPreviewFixture(page)
}
