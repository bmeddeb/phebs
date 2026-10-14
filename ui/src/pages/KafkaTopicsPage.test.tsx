import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { Client } from 'styletron-engine-monolithic'
import { Provider as StyletronProvider } from 'styletron-react'
import { BaseProvider, LightTheme } from 'baseui'
import type { ProofBundleEnvelope, TopicInventoryReport } from '../api'

const api = vi.hoisted(() => ({
  fetchKafkaTopicUsage: vi.fn(),
  composeKafkaTopicInventory: vi.fn(),
  fetchTopicInventoryPage: vi.fn(),
}))
vi.mock('../api', () => api)

import KafkaTopicsPage from './KafkaTopicsPage'

const engine = new Client()

const zeroCensus = {
  'ambiguous-library-import': 0,
  'call-expr': 0,
  'invalid-topic-literal': 0,
  'non-literal-expr': 0,
  'selector-expr': 0,
  'unresolved-ident': 0,
}

const envelope: ProofBundleEnvelope = {
  id: 'pb_test',
  bundle: {
    schema_version: 'proof-bundle-v1',
    query: { kind: 'find_kafka_topic_usage', topic: 'orders-v1', domains: ['kafka-consumer', 'kafka-producer'] },
    assertions: [
      {
        id: 'produces', predicate: 'PRODUCES_TO_TOPIC', subject: 'svc/producer.go',
        object: 'topic:orders-v1', tier: 'derived', repo: 'example.com/app',
        // Two supporting atoms: the extractor merges same-file sites into
        // one assertion, and the page must cite every site.
        supporting: ['atom-1', 'atom-2'], run_id: 'run-p',
        detail: '{"schema":"kafka-topic-evidence-detail-v1","libraries":["sarama"],"import_paths":["github.com/IBM/sarama"],"shapes":["ProducerMessage"],"bindings":["literal","same-file-const"]}',
      },
      {
        id: 'consumes', predicate: 'CONSUMES_FROM_TOPIC', subject: 'svc/consumer.go',
        object: 'topic:orders-v1', tier: 'heuristic', repo: 'example.com/app',
        supporting: ['atom-3'], run_id: 'run-c',
        detail: '{"schema":"kafka-topic-evidence-detail-v1","libraries":["segmentio"],"import_paths":["github.com/segmentio/kafka-go"],"shapes":["ReaderConfig.Topic"],"bindings":["literal"],"group_ids":["billing"]}',
      },
    ],
    evidence: [
      {
        repository: 'example.com/app', run_id: 'run-p',
        atom: {
          atom_id: 'atom-1', schema_version: 't23-v1', blob_digest: 'sha256:x',
          start_byte: 10, end_byte: 21, rule_id: 'kafkago-topic-v1',
          extractor_version: '1.0.0', adapter_config_digest: 'sha256:y',
          fact_fingerprint: 'f', first_seen: '2026-07-26T00:00:00Z',
        },
        occurrences: [{
          occurrence_id: 'occ-1', atom_id: 'atom-1', repo: 'example.com/app',
          commit: 'c0ffee', path: 'svc/producer.go', start_line: 12, end_line: 12,
          visibility_scope: 'default', run_id: 'run-p', observed_at: '2026-07-26T00:00:00Z',
        }],
      },
      {
        repository: 'example.com/app', run_id: 'run-p',
        atom: {
          atom_id: 'atom-2', schema_version: 't23-v1', blob_digest: 'sha256:x',
          start_byte: 40, end_byte: 51, rule_id: 'kafkago-topic-v1',
          extractor_version: '1.0.0', adapter_config_digest: 'sha256:y',
          fact_fingerprint: 'f', first_seen: '2026-07-26T00:00:00Z',
        },
        occurrences: [{
          occurrence_id: 'occ-2', atom_id: 'atom-2', repo: 'example.com/app',
          commit: 'c0ffee', path: 'svc/producer.go', start_line: 30, end_line: 30,
          visibility_scope: 'default', run_id: 'run-p', observed_at: '2026-07-26T00:00:00Z',
        }],
      },
    ],
    unresolved_census: {
      schema_version: 'kafka-topic-census-v1',
      producer: { ...zeroCensus, 'call-expr': 3 },
      consumer: { ...zeroCensus },
      published_runs: 2,
      producer_published_runs: 1,
      consumer_published_runs: 1,
      truncated: ['producer:call-expr'],
    },
    coverage: {
      schema_version: 'coverage-certificate-v1',
      domains: [],
      repository_count: 1,
      repositories: [{
        repository: 'example.com/app',
        indexed_commit: 'c0ffee',
        scip_index: 'absent',
        runs: [],
      }],
      digest: 'sha256:z',
    },
    extractor_versions: [],
    caveat: 'Source evidence only; never a completeness claim.',
  },
}

const quality = {
  card: 'phebs.kafka.producer', claim: 'site coverage', protocol: 'docs/KAFKA_PRODUCER_QUALITY_PROTOCOL.md',
  measured: ['762/762 sites'], unmeasured: ['topic-name precision'],
}

const inventory: TopicInventoryReport = {
  schema_version: 'kafka-topic-inventory-v1',
  bundle_id: 'pb_inv',
  planes: [
    { plane: 'producer', domain: 'kafka-producer', released: true, state: 'served', quality, published_runs: 1, repositories: 1, topic_count: 1, site_count: 2 },
    { plane: 'consumer', domain: 'kafka-consumer', released: true, state: 'served', quality: { ...quality, card: 'phebs.kafka.consumer' }, published_runs: 1, repositories: 1, topic_count: 1, site_count: 1 },
  ],
  topics: [
    {
      topic: 'orders-v1',
      producer: {
        state: 'served', sites: 2, files: 1, omitted_citations: 3,
        citations: [{ repository: 'example.com/app', commit: 'c0ffee1234567890', run_id: 'run-p', assertion_id: 'a1', path: 'svc/producer.go', sites: 2 }],
      },
      consumer: { state: 'none', sites: 0, files: 0 },
    },
    {
      topic: 'payments',
      producer: { state: 'none', sites: 0, files: 0 },
      consumer: { state: 'served', sites: 1, files: 1 },
    },
  ],
  pagination: { order: 'topic ascending', page_size: 50, returned: 2, total: 3, complete: false, next_cursor: 'cur2' },
  unresolved_census: { ...envelope.bundle.unresolved_census! },
  coverage: envelope.bundle.coverage,
  truncated: ['producer:topic-scan'],
  caveat: 'Composed source-spelled topic inventory. A topic is a source spelling, never a broker, cluster, environment, or runtime identity, and a topic absent from this inventory is not proven absent from the system.',
}

function page(hash: string, inventoryAvailable = false) {
  window.location.hash = hash
  const params = new URLSearchParams(hash.split('?')[1] ?? '')
  return render(
    <StyletronProvider value={engine}>
      <BaseProvider theme={LightTheme}>
        <KafkaTopicsPage params={params} inventoryAvailable={inventoryAvailable} />
      </BaseProvider>
    </StyletronProvider>,
  )
}

beforeEach(() => {
  window.location.hash = ''
  api.fetchKafkaTopicUsage.mockReset().mockResolvedValue(envelope)
  api.composeKafkaTopicInventory.mockReset().mockResolvedValue({ id: 'pb_inv', bundle: envelope.bundle })
  api.fetchTopicInventoryPage.mockReset().mockResolvedValue(inventory)
})
afterEach(cleanup)

test('deep-linked topic renders census first with every citation', async () => {
  page('#/topics?topic=orders-v1')
  expect(api.fetchKafkaTopicUsage).toHaveBeenCalledWith('orders-v1', expect.anything())

  const census = await screen.findByTestId('unresolved-census')
  expect(census.textContent).toContain('At least 3 producer source sites could not be resolved across 1 published run')
  expect(census.textContent).toContain('0 consumer source sites could not be resolved across 1 published run')
  expect(census.textContent).toContain('This view is not complete')
  expect(census.textContent).toContain('Whole-file extraction gaps are reported separately')
  // Every frozen shape class is listed even at zero, and the truncated
  // class renders as a lower bound.
  for (const shapeClass of Object.keys(zeroCensus)) {
    expect(census.textContent).toContain(shapeClass)
  }
  expect(census.textContent).toContain('≥3')
  expect(census.textContent).toContain('lower bounds')

  const usage = screen.getByTestId('topic-usage')
  // The census panel precedes the evidence sections in document order.
  expect(usage.firstElementChild?.getAttribute('data-testid')).toBe('unresolved-census')
  expect(screen.getByTestId('analysis-scope-panel')).toBeTruthy()
  expect(screen.getByText('Scope of this topic answer')).toBeTruthy()

  // A merged assertion cites every supporting site, not just the first.
  expect(screen.getByRole('link', { name: 'example.com/app/svc/producer.go:12' })).toBeTruthy()
  expect(screen.getByRole('link', { name: 'example.com/app/svc/producer.go:30' })).toBeTruthy()
  expect(screen.getByText(/sarama · ProducerMessage · literal\+same-file-const · tier derived/)).toBeTruthy()
  expect(screen.getByText(/segmentio · ReaderConfig.Topic · literal · group billing · tier heuristic/)).toBeTruthy()
})

test('zero published runs renders the no-run explanation, never affirmative zeros', async () => {
  api.fetchKafkaTopicUsage.mockResolvedValue({
    ...envelope,
    bundle: {
      ...envelope.bundle,
      assertions: [],
      evidence: [],
      coverage: {
        ...envelope.bundle.coverage,
        repository_count: 0,
        repositories: [],
      },
      unresolved_census: {
        schema_version: 'kafka-topic-census-v1',
        producer: { ...zeroCensus },
        consumer: { ...zeroCensus },
        published_runs: 0,
        producer_published_runs: 0,
        consumer_published_runs: 0,
      },
    },
  })
  page('#/topics?topic=unknown-topic')
  const census = await screen.findByTestId('unresolved-census')
  expect(census.textContent).toContain('No Kafka extraction run has published')
  expect(census.textContent).not.toContain('could not be resolved from source')
  expect(screen.getByText('0 repositories')).toBeTruthy()
  fireEvent.click(screen.getByText('Coverage certificate'))
  expect(screen.getByTestId('coverage-certificate-empty-universe')).toBeTruthy()
  expect(screen.getByText('No producer with a source-literal spelling of this topic is visible.')).toBeTruthy()
  expect(screen.getByText('No consumer with a source-literal spelling of this topic is visible.')).toBeTruthy()
})

test('a producer-only publication leaves consumer zeros explicitly unmeasured', async () => {
  api.fetchKafkaTopicUsage.mockResolvedValue({
    ...envelope,
    bundle: {
      ...envelope.bundle,
      unresolved_census: {
        schema_version: 'kafka-topic-census-v1',
        producer: { ...zeroCensus, 'selector-expr': 2 },
        consumer: { ...zeroCensus },
        published_runs: 1,
        producer_published_runs: 1,
        consumer_published_runs: 0,
      },
    },
  })
  page('#/topics?topic=orders-v1')
  const census = await screen.findByTestId('unresolved-census')
  expect(census.textContent).toContain('2 producer source sites could not be resolved across 1 published run')
  expect(census.textContent).toContain('No consumer extraction run has published; consumer zeros are not meaningful')
})

test('query failures clear any previous result and same-topic resubmit retries', async () => {
  page('#/topics?topic=orders-v1')
  await screen.findByTestId('topic-usage')

  api.fetchKafkaTopicUsage.mockRejectedValueOnce(new Error('500: store unavailable'))
  fireEvent.submit(screen.getByRole('button', { name: 'Query' }).closest('form') as HTMLFormElement)
  expect(await screen.findByText(/store unavailable/)).toBeTruthy()
  // The previous answer must not linger under the error banner.
  expect(screen.queryByTestId('topic-usage')).toBeNull()

  // Same-topic resubmit re-queries even though the hash cannot change.
  api.fetchKafkaTopicUsage.mockResolvedValueOnce(envelope)
  fireEvent.submit(screen.getByRole('button', { name: 'Query' }).closest('form') as HTMLFormElement)
  expect(await screen.findByTestId('topic-usage')).toBeTruthy()
  expect(api.fetchKafkaTopicUsage).toHaveBeenCalledTimes(3)
})

test('the inventory appears only with its own capability', async () => {
  page('#/topics')
  expect(screen.queryByTestId('topic-inventory')).toBeNull()
  expect(api.composeKafkaTopicInventory).not.toHaveBeenCalled()
})

test('a bare /topics composes once and addresses the bundle from the URL', async () => {
  page('#/topics', true)
  await vi.waitFor(() => expect(window.location.hash).toBe('#/topics?inventory=pb_inv'))
  expect(api.composeKafkaTopicInventory).toHaveBeenCalledTimes(1)
})

test('served inventory rows keep plane states, citations, truncation and paging', async () => {
  page('#/topics?inventory=pb_inv&cursor=cur1', true)
  const view = await screen.findByTestId('topic-inventory')
  expect(api.fetchTopicInventoryPage).toHaveBeenCalledWith('pb_inv', 'cur1', expect.anything())
  // Each row links into the per-spelling detail.
  expect(screen.getByRole('link', { name: 'orders-v1' }).getAttribute('href')).toBe('#/topics?topic=orders-v1')
  expect(view.textContent).toContain('2 sites · 1 file')
  // A measured zero is named as such, never a blank.
  expect(screen.getAllByText('none · measured zero')).toHaveLength(2)
  // Citations pin the exact commit, and omitted ones are counted.
  expect(screen.getByRole('link', { name: 'example.com/app/svc/producer.go@c0ffee123456' })).toBeTruthy()
  expect(view.textContent).toContain('3 more not shown')
  expect(view.textContent).toContain('Counts are lower bounds')
  expect(view.textContent).toContain('2 of 3 topics on this page')
  expect(screen.getByTestId('plane-producer').textContent).toContain('Not measured: topic-name precision')
  expect(screen.getByTestId('unresolved-census')).toBeTruthy()
  expect(view.textContent).toContain(inventory.caveat.split(' A topic')[0])
  fireEvent.click(screen.getByRole('button', { name: 'Next page' }))
  expect(window.location.hash).toBe('#/topics?inventory=pb_inv&cursor=cur2')
})

test('unpublished and unavailable planes never read as zero, and an empty inventory is not an absence claim', async () => {
  api.fetchTopicInventoryPage.mockResolvedValue({
    ...inventory,
    planes: [
      { ...inventory.planes[0], state: 'unpublished', published_runs: 0, topic_count: 0, site_count: 0 },
      { ...inventory.planes[1], state: 'unavailable', released: false, quality: undefined, published_runs: 0, topic_count: 0, site_count: 0 },
    ],
    topics: [],
    truncated: [],
    pagination: { order: 'topic ascending', page_size: 50, returned: 0, total: 0, complete: true },
    unresolved_census: {
      schema_version: 'kafka-topic-census-v1',
      producer: { ...zeroCensus, 'unresolved-ident': 4 },
      consumer: { ...zeroCensus },
      published_runs: 1,
      producer_published_runs: 1,
      consumer_published_runs: 0,
      unavailable_planes: ['consumer'],
    },
  })
  page('#/topics?inventory=pb_inv', true)
  await screen.findByTestId('topic-inventory')
  expect(screen.getByTestId('plane-producer').textContent).toContain('unpublished')
  expect(screen.getByTestId('plane-producer').textContent).toContain("this plane's zeros are not measured")
  expect(screen.getByTestId('plane-consumer').textContent).toContain('not released on this server')
  expect(screen.getByText('No literal topic spelling resolved in scope')).toBeTruthy()
  expect(screen.getByText(/This is not an absence claim/)).toBeTruthy()
  const census = screen.getByTestId('unresolved-census')
  expect(census.textContent).toContain('4 producer source sites could not be resolved')
  expect(census.textContent).toContain('The consumer plane is unavailable on this server (not released)')
  expect(screen.queryByRole('button', { name: 'Next page' })).toBeNull()
})

test('a crossed or inconsistent answer fails closed with a retry, never partial rows', async () => {
  api.fetchTopicInventoryPage.mockResolvedValueOnce({ ...inventory, bundle_id: 'pb_other' })
  page('#/topics?inventory=pb_inv', true)
  expect(await screen.findByText(/names a different inventory/)).toBeTruthy()
  expect(screen.queryByRole('table')).toBeNull()
  api.fetchTopicInventoryPage.mockResolvedValueOnce({ ...inventory, pagination: { ...inventory.pagination, returned: 5 } })
  fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
  expect(await screen.findByText(/disagrees with its pagination/)).toBeTruthy()
  api.fetchTopicInventoryPage.mockResolvedValueOnce(inventory)
  fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
  expect(await screen.findByRole('link', { name: 'orders-v1' })).toBeTruthy()
})

test('leaving a topic for the inventory clears the topic answer', async () => {
  const view = page('#/topics?topic=orders-v1', true)
  await screen.findByTestId('topic-usage')
  window.location.hash = '#/topics?inventory=pb_inv'
  view.rerender(
    <StyletronProvider value={engine}>
      <BaseProvider theme={LightTheme}>
        <KafkaTopicsPage params={new URLSearchParams('inventory=pb_inv')} inventoryAvailable />
      </BaseProvider>
    </StyletronProvider>,
  )
  await screen.findByTestId('topic-inventory')
  expect(screen.queryByTestId('topic-usage')).toBeNull()
})
