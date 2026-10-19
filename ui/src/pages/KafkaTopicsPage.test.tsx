import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { Client } from 'styletron-engine-monolithic'
import { Provider as StyletronProvider } from 'styletron-react'
import { BaseProvider, LightTheme } from 'baseui'
import type { ProofBundleEnvelope, TopicInventoryReport } from '../api'
import { zeroCensus, kafkaUsage as envelope, kafkaInventory as inventory } from '../test-fixtures/impactKafka'

const api = vi.hoisted(() => ({
  fetchKafkaTopicUsage: vi.fn(),
  composeKafkaTopicInventory: vi.fn(),
  fetchTopicInventoryPage: vi.fn(),
}))
vi.mock('../api', () => api)

import KafkaTopicsPage from './KafkaTopicsPage'

const engine = new Client()

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
  page('#/topics?topic=orders-v1')
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

function reroute(view: ReturnType<typeof page>, query: string) {
  view.rerender(<StyletronProvider value={engine}><BaseProvider theme={LightTheme}>
    <KafkaTopicsPage params={new URLSearchParams(query)} inventoryAvailable />
  </BaseProvider></StyletronProvider>)
}

test('returning to inventory clears the topic error', async () => {
  api.fetchKafkaTopicUsage.mockRejectedValueOnce(new Error('topic unavailable'))
  const view = page('#/topics?topic=orders-v1', true)
  await screen.findByText(/topic unavailable/)
  reroute(view, 'inventory=pb_inv')
  await screen.findByRole('link', { name: 'orders-v1' })
  expect(screen.queryByText(/topic unavailable/)).toBeNull()
})

test('an abandoned inventory composition cannot replace topic navigation', async () => {
  let resolve!: (value: ProofBundleEnvelope) => void
  api.composeKafkaTopicInventory.mockImplementationOnce(() => new Promise<ProofBundleEnvelope>(yes => { resolve = yes }))
  const view = page('#/topics', true)
  reroute(view, 'topic=orders-v1')
  window.location.hash = '#/topics?topic=orders-v1'
  await act(async () => { resolve(envelope) })
  expect(window.location.hash).toBe('#/topics?topic=orders-v1')
})

test.each(['success', 'error'])('a replaced inventory cursor ignores late %s', async (outcome) => {
  let resolve!: (value: TopicInventoryReport) => void
  let reject!: (reason: Error) => void
  api.fetchTopicInventoryPage.mockImplementationOnce(() => new Promise<TopicInventoryReport>((yes, no) => { resolve = yes; reject = no }))
  const view = page('#/topics?inventory=pb_inv', true)
  api.fetchTopicInventoryPage.mockResolvedValueOnce({ ...inventory, topics: [], pagination: { ...inventory.pagination, returned: 0, complete: true, next_cursor: undefined } })
  reroute(view, 'inventory=pb_inv&cursor=cur2')
  await screen.findByText('No literal topic spelling resolved in scope')
  await act(async () => { if (outcome === 'success') resolve(inventory); else reject(new Error('abandoned cursor')) })
  expect(screen.getByText('No literal topic spelling resolved in scope')).toBeTruthy()
  expect(screen.queryByText(/abandoned cursor/)).toBeNull()
  expect(screen.queryByRole('link', { name: 'orders-v1' })).toBeNull()
})

test('topic usage refuses a response for a different spelling', async () => {
  page('#/topics?topic=payments')
  expect(await screen.findByText(/different topic/)).toBeTruthy()
  expect(screen.queryByTestId('topic-usage')).toBeNull()
})

test('unmount aborts the current same-topic retry', async () => {
  const view = page('#/topics?topic=orders-v1')
  await screen.findByTestId('topic-usage')
  let resolve!: (value: ProofBundleEnvelope) => void
  api.fetchKafkaTopicUsage.mockImplementationOnce(() => new Promise<ProofBundleEnvelope>(yes => { resolve = yes }))
  fireEvent.submit(screen.getByRole('button', { name: 'Query' }).closest('form') as HTMLFormElement)
  const signal = api.fetchKafkaTopicUsage.mock.calls[1][1] as AbortSignal
  view.unmount()
  await act(async () => { resolve(envelope) })
  expect(signal.aborted).toBe(true)
})

test.each(['null', '[]', '{"libraries":"sarama"}', '{"shapes":[{}]}', '{"group_ids":42}'])('malformed optional detail %s cannot remove exact citations', async (detail) => {
  api.fetchKafkaTopicUsage.mockResolvedValueOnce({ ...envelope, bundle: { ...envelope.bundle, assertions: [{ ...envelope.bundle.assertions[0], detail }] } })
  page('#/topics?topic=orders-v1')
  expect(await screen.findByRole('link', { name: 'example.com/app/svc/producer.go:12' })).toBeTruthy()
  expect(screen.getByRole('link', { name: 'example.com/app/svc/producer.go:30' })).toBeTruthy()
  expect(screen.queryByText(/\[object Object\]/)).toBeNull()
})
