import type { ContractImpactReport, ProofBundleEnvelope, TopicInventoryReport } from '../api'

// Neutral UI fixtures, shared by component and native-browser integration.
export const impactCommit = '0123456789abcdef0123456789abcdef01234567'
export const impactReport: ContractImpactReport = {
  schema_version: 'contract-impact-report-v2',
  bundle_id: `pb_${'a'.repeat(64)}`,
  query: {
    kind: 'contract_impact_operation',
    operation: '/shop.Cart/Get',
    domains: ['grpc-consumer'],
  },
  conclusion: {
    text: 'Resolved or matching call evidence was found within the stated evidence scope.',
    coverage_digest: `sha256:${'b'.repeat(64)}`,
  },
  resolved_evidence: [],
  matching_call_evidence: [{
    kind: 'operation_call',
    domain: 'grpc-consumer',
    protocol: 'protobuf',
    assertion_id: 'assertion-known',
    evidence_atom_id: 'atom-known',
    predicate: 'CALLS_OPERATION',
    object: '/shop.Cart/Get',
    repository: 'github.com/acme/cart-client',
    commit: impactCommit,
    path: 'client/cart.go',
    start_byte: 100,
    end_byte: 116,
    start_line: 27,
    end_line: 27,
    tier: 'heuristic',
    code_role: 'production',
    classification: 'matching_call_evidence',
    reason: 'operation object matched without declaration identity',
    fresh: true,
  }],
  extractor_abstentions: [{
    kind: 'unresolved_candidate',
    domain: 'grpc-consumer',
    protocol: 'protobuf',
    assertion_id: 'assertion-unresolved',
    evidence_atom_id: 'atom-unresolved',
    predicate: 'UNRESOLVED_GRPC_CALL',
    object: 'Get',
    repository: 'github.com/acme/cart-client',
    commit: impactCommit,
    path: 'client/ambiguous.go',
    start_byte: 200,
    end_byte: 219,
    start_line: 41,
    end_line: 41,
    tier: 'unresolved',
    code_role: 'test',
    classification: 'extractor_abstention',
    reason: 'method Get matches 2 generated services',
    fresh: false,
  }],
  coverage: {
    schema_version: 'coverage-certificate-v1',
    domains: ['grpc-consumer'],
    repository_count: 2,
    repositories: [{
      repository: 'github.com/acme/cart-client',
      indexed_commit: impactCommit,
      scip_index: 'absent',
      runs: [{
        domain: 'grpc-consumer',
        status: 'published',
        run_id: 'run-known',
        extractor: 'grpcgo',
        commit: impactCommit,
        fresh: true,
        protocols: ['grpc'],
        corpus_file_count: 4,
        candidate_file_count: 2,
        read_file_count: 2,
        read_bytes: 512,
        unresolved_count: 1,
        assertion_count: 2,
        atom_count: 2,
      }],
    }, {
      repository: 'github.com/acme/web',
      indexed_commit: impactCommit,
      scip_index: 'absent',
      runs: [{
        domain: 'grpc-consumer',
        status: 'absent',
        fresh: false,
        corpus_file_count: 1,
        candidate_file_count: 0,
        read_file_count: 0,
        read_bytes: 0,
        unresolved_count: 0,
        assertion_count: 0,
        atom_count: 0,
      }],
    }],
    digest: `sha256:${'b'.repeat(64)}`,
  },
  coverage_rows: [{
    repository: 'github.com/acme/cart-client',
    domain: 'grpc-consumer',
    state: 'covered',
    indexed_commit: impactCommit,
    evidence_commit: impactCommit,
    assertion_count: 2,
    unresolved_count: 1,
    candidate_file_count: 2,
    read_file_count: 2,
  }, {
    repository: 'github.com/acme/web',
    domain: 'grpc-consumer',
    state: 'unsupported',
    reason: 'no published evidence for this domain',
    indexed_commit: impactCommit,
    assertion_count: 0,
    unresolved_count: 0,
    candidate_file_count: 0,
    read_file_count: 0,
  }],
  caveat: 'Evidence is bounded to visible repositories and recorded extractor coverage.',
}

export const zeroCensus = {
  'ambiguous-library-import': 0,
  'call-expr': 0,
  'invalid-topic-literal': 0,
  'non-literal-expr': 0,
  'selector-expr': 0,
  'unresolved-ident': 0,
}

export const kafkaUsage: ProofBundleEnvelope = {
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

const kafkaQuality = {
  card: 'phebs.kafka.producer', claim: 'site coverage', protocol: 'docs/KAFKA_PRODUCER_QUALITY_PROTOCOL.md',
  measured: ['762/762 sites'], unmeasured: ['topic-name precision'],
}

export const kafkaInventory: TopicInventoryReport = {
  schema_version: 'kafka-topic-inventory-v1',
  bundle_id: 'pb_inv',
  planes: [
    { plane: 'producer', domain: 'kafka-producer', released: true, state: 'served', quality: kafkaQuality, published_runs: 1, repositories: 1, topic_count: 1, site_count: 2 },
    { plane: 'consumer', domain: 'kafka-consumer', released: true, state: 'served', quality: { ...kafkaQuality, card: 'phebs.kafka.consumer' }, published_runs: 1, repositories: 1, topic_count: 1, site_count: 1 },
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
  unresolved_census: { ...kafkaUsage.bundle.unresolved_census! },
  coverage: kafkaUsage.bundle.coverage,
  truncated: ['producer:topic-scan'],
  caveat: 'Composed source-spelled topic inventory. A topic is a source spelling, never a broker, cluster, environment, or runtime identity, and a topic absent from this inventory is not proven absent from the system.',
}
