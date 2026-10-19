import type { Page, Route } from '@playwright/test'
import { impactReport, kafkaUsage, kafkaInventory } from '../src/test-fixtures/impactKafka'
import { navigationFixture } from './navigationFixture'
import { installOperationImpactFixture } from './operationImpactFixture'

export { impactReport, kafkaUsage, kafkaInventory }
export const SAVED_IMPACT_PATH = '/impact?bundle=' + impactReport.bundle_id

export async function impactKafkaFixture(page: Page) {
  const base = await navigationFixture(page)
  const stable = await installOperationImpactFixture(page, 'current')
  const reads: string[] = []
  let hold = ''
  let held: { route: Route; value: unknown } | undefined
  let badSaved = false
  let badTopic = false
  let badDetail = false
  let failed = ''
  await page.route('**/api/**', async route => {
    const url = new URL(route.request().url())
    let value: unknown
    if (url.pathname === '/api/version') value = { version: 'impact-topics-audit', capabilities: [
      'contract-impact-report', 'contract-impact-operation', 'contract-caller-map', 'kafka-topic-usage', 'kafka-topic-inventory',
    ] }
    else if (url.pathname.startsWith('/api/contract_impact_reports/')) value = { ...impactReport,
      bundle_id: badSaved ? 'pb_crossed' : decodeURIComponent(url.pathname.split('/').pop()!),
    }
    else if (url.pathname === '/api/contract_impact_report') value = impactReport
    else if (url.pathname === '/api/list_kafka_topics') value = { id: kafkaInventory.bundle_id, bundle: kafkaUsage.bundle }
    else if (url.pathname.startsWith('/api/topic_inventory/')) value = url.searchParams.get('cursor')
      ? { ...kafkaInventory, topics: [], pagination: { ...kafkaInventory.pagination, returned: 0, complete: true, next_cursor: undefined } }
      : kafkaInventory
    else if (url.pathname === '/api/find_kafka_topic_usage') value = { ...kafkaUsage, bundle: { ...kafkaUsage.bundle,
      query: { ...kafkaUsage.bundle.query, topic: badTopic ? 'crossed-topic' : url.searchParams.get('topic')! },
      assertions: kafkaUsage.bundle.assertions.map(row => badDetail ? { ...row, detail: 'null' } : row),
    } }
    else { await route.fallback(); return }
    if (url.pathname !== '/api/version') reads.push(url.pathname + url.search)
    if (hold && url.pathname === hold) { hold = ''; held = { route, value }; return }
    if (failed && url.pathname === failed) {
      await route.fulfill({ status: 503, json: { error: 'temporary audit failure' } }); return
    }
    await route.fulfill({ json: value })
  })
  return {
    ...base, reads, unexpected: base.unexpected,
    stableUnexpected: stable.unexpected,
    badSaved: (value: boolean) => { badSaved = value },
    badTopic: (value: boolean) => { badTopic = value },
    badDetail: (value: boolean) => { badDetail = value },
    fail: (path: string) => { failed = path },
    hold: (path: string) => { hold = path },
    held: () => held !== undefined,
    release: async () => { const pending = held!; held = undefined; await pending.route.fulfill({ json: pending.value }) },
  }
}
