import { cleanup, render, screen } from '@testing-library/react'
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
        supporting: ['atom-1'], run_id: 'run-p',
        detail: '{"schema":"kafka-topic-evidence-detail-v1","library":"sarama","shape":"ProducerMessage","binding":"literal"}',
      },
      {
        id: 'consumes', predicate: 'CONSUMES_FROM_TOPIC', subject: 'svc/consumer.go',
        object: 'topic:orders-v1', tier: 'heuristic', repo: 'example.com/app',
        supporting: ['atom-2'], run_id: 'run-c',
        detail: '{"schema":"kafka-topic-evidence-detail-v1","library":"segmentio","shape":"ReaderConfig.Topic","binding":"literal","group_id":"billing"}',
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
    ],
    unresolved_census: {
      schema_version: 'kafka-topic-census-v1',
      producer: { ...zeroCensus, 'call-expr': 3 },
      consumer: { ...zeroCensus },
    },
    coverage: { schema_version: 'coverage-v3', domains: [], repository_count: 1, repositories: [], digest: 'sha256:z' },
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

test('deep-linked topic renders census first with rows and citations', async () => {
  page('#/topics?topic=orders-v1')
  expect(api.fetchKafkaTopicUsage).toHaveBeenCalledWith('orders-v1', expect.anything())

  const census = await screen.findByTestId('unresolved-census')
  expect(census.textContent).toContain('3 producer sites and 0 consumer sites could not be resolved from source')
  expect(census.textContent).toContain('this view is not complete')
  // Every frozen shape class is listed even at zero.
  for (const shapeClass of Object.keys(zeroCensus)) {
    expect(census.textContent).toContain(shapeClass)
  }

  const usage = screen.getByTestId('topic-usage')
  // The census panel precedes the evidence sections in document order.
  expect(usage.firstElementChild?.getAttribute('data-testid')).toBe('unresolved-census')

  const citation = screen.getByRole('link', { name: 'example.com/app/svc/producer.go:12' })
  expect(citation.getAttribute('href')).toContain('#/file?')
  expect(screen.getByText(/sarama · ProducerMessage · literal · tier derived/)).toBeTruthy()
  expect(screen.getByText(/segmentio · ReaderConfig.Topic · literal · group billing · tier heuristic/)).toBeTruthy()
})

test('zero-evidence answers keep the census and honest emptiness', async () => {
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
      },
    },
  })
  page('#/topics?topic=unknown-topic')
  const census = await screen.findByTestId('unresolved-census')
  expect(census.textContent).toContain('0 producer sites and 0 consumer sites')
  expect(screen.getByText('No producer with a source-literal spelling of this topic is visible.')).toBeTruthy()
  expect(screen.getByText('No consumer with a source-literal spelling of this topic is visible.')).toBeTruthy()
})

test('query failures surface as errors without a stale result', async () => {
  api.fetchKafkaTopicUsage.mockRejectedValue(new Error('400: topic must contain only [a-zA-Z0-9._-]'))
  page('#/topics?topic=orders-v1')
  expect(await screen.findByText(/topic must contain only/)).toBeTruthy()
  expect(screen.queryByTestId('topic-usage')).toBeNull()
})
