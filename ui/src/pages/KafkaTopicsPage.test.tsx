import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { Client } from 'styletron-engine-monolithic'
import { Provider as StyletronProvider } from 'styletron-react'
import { BaseProvider, LightTheme } from 'baseui'
import type { ProofBundleEnvelope } from '../api'

const api = vi.hoisted(() => ({
  fetchKafkaTopicUsage: vi.fn(),
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
    coverage: { schema_version: 'coverage-certificate-v1', domains: [], repository_count: 1, repositories: [], digest: 'sha256:z' },
    extractor_versions: [],
    caveat: 'Source evidence only; never a completeness claim.',
  },
}

function page(hash: string) {
  window.location.hash = hash
  const params = new URLSearchParams(hash.split('?')[1] ?? '')
  return render(
    <StyletronProvider value={engine}>
      <BaseProvider theme={LightTheme}>
        <KafkaTopicsPage params={params} />
      </BaseProvider>
    </StyletronProvider>,
  )
}

beforeEach(() => {
  window.location.hash = ''
  api.fetchKafkaTopicUsage.mockReset().mockResolvedValue(envelope)
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
