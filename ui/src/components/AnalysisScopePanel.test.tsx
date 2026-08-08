import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, expect, test } from 'vitest'
import { Client } from 'styletron-engine-monolithic'
import { Provider as StyletronProvider } from 'styletron-react'
import { BaseProvider, LightTheme } from 'baseui'
import type {
  AnalysisScopeProjection,
  CoverageCertificate,
  CoverageDomainOutcomeReceipt,
  RepoStatus,
} from '../api'
import {
  AnalysisScopePanel,
} from './AnalysisScopePanel'
import { analysisScopeFromRepoStatus } from './analysisScope'

const engine = new Client()
const commit = '0123456789abcdef0123456789abcdef01234567'

const fullReceipt: CoverageDomainOutcomeReceipt = {
  schema: 'extraction-domain-outcome-receipt-v1',
  domain: 'grpc-caller',
  extractor_version: 'grpcgo@1.5.0',
  disposition: 'published',
  reason: '',
  inventory_ms: 3,
  opened_source_ms: 5,
  extractor_ms: 8,
  staging_ms: 2,
  counts: {
    corpus_files: 6,
    candidate_files: 4,
    excluded_source_files: 2,
    excluded_scip_documents: 1,
    excluded_scip_definitions: 2,
    excluded_scip_occurrences: 3,
    opened_source_attempts: 4,
    opened_source_files: 4,
    facts: 3,
    atoms: 3,
    assertions: 2,
    unresolved: 1,
    staged_chunks: 1,
    staged_rows: 3,
  },
  bytes: {
    planned_declared: 1200,
    excluded_source_declared: 200,
    opened_source: 1000,
  },
  limits: {
    corpus_files: 100000,
    opened_source_attempts: 25000,
    opened_source_files: 25000,
    opened_source_bytes: 64 * 1024,
    facts: 100000,
    source_blob_bytes: 16 * 1024,
    typed_input_bytes: 64 * 1024,
    aggregate_wall_ms: 15 * 60 * 1000,
    mirror_lock_ms: 5 * 60 * 1000,
    domain_wall_ms: 5 * 60 * 1000,
    abort_wall_ms: 10000,
    outcome_wall_ms: 1000,
    max_serial_domains: 17,
    scheduler_identity_bytes: 64 * 1024,
    aggregate_staged_rows: 100000,
    domain_staged_rows: 25000,
  },
}

const certificate: CoverageCertificate = {
  schema_version: 'coverage-certificate-v3',
  domains: ['grpc-caller', 'proto-contract'],
  repository_count: 2,
  digest: `sha256:${'c'.repeat(64)}`,
  repositories: [{
    repository: 'github.com/acme/checkout',
    indexed_commit: commit,
    scope_posture: 'focused',
    analysis_unit: {
      name: 'checkout-api',
      digest: `sha256:${'a'.repeat(64)}`,
      primary_paths: ['services/checkout/**', 'cmd/checkout/**'],
      supporting_paths: ['pkg/contracts/**'],
      typed_index_posture: 'unit-bound',
      typed_index_kind: 'scip',
      typed_index_path: 'index.scip',
    },
    scip_index: 'present',
    runs: [{
      domain: 'grpc-caller',
      status: 'published',
      run_id: 'run-grpc',
      extractor: 'grpcgo@1.5.0',
      commit,
      unit_digest: `sha256:${'a'.repeat(64)}`,
      fresh: true,
      corpus_file_count: 6,
      candidate_file_count: 4,
      read_file_count: 4,
      read_bytes: 1000,
      unresolved_count: 1,
      assertion_count: 2,
      atom_count: 3,
      evidence_scope_posture: 'focused-local',
      candidate_scope: {
        manifest_digest: `sha256:${'b'.repeat(64)}`,
        plane: 'candidate-v4',
        corpus_file_count: 6,
        corpus_declared_bytes: 1200,
        corpus_digest: `sha256:${'d'.repeat(64)}`,
        planned_file_count: 4,
        planned_required_file_count: 4,
        planned_declared_bytes: 1000,
        planned_digest: `sha256:${'e'.repeat(64)}`,
        base_source_file_count: 3,
        excluded_go_test_file_count: 2,
        typed_input: {
          kind: 'scip',
          path: 'index.scip',
          declared_bytes: 400,
          present: true,
        },
      },
      outcome: {
        disposition: 'published',
        generation_digest: `sha256:${'f'.repeat(64)}`,
        extractor: 'grpcgo@1.5.0',
        receipt_schema: fullReceipt.schema,
        receipt_state: 'full',
        receipt: fullReceipt,
      },
    }, {
      domain: 'proto-contract',
      status: 'unpublished',
      fresh: false,
      corpus_file_count: 2,
      candidate_file_count: 2,
      read_file_count: 0,
      read_bytes: 0,
      unresolved_count: 0,
      assertion_count: 0,
      atom_count: 0,
      outcome: {
        disposition: 'terminal_generation_refusal',
        generation_digest: `sha256:${'9'.repeat(64)}`,
        extractor: 'protodecl@1.3.0',
        candidate_control_failure: true,
        receipt_schema: 'extraction-domain-outcome-receipt-v1',
        receipt_state: 'schema_only',
      },
    }],
  }, {
    repository: 'github.com/acme/payments',
    indexed_commit: commit,
    scope_posture: 'focused',
    analysis_unit: {
      name: 'payments-api',
      digest: `sha256:${'1'.repeat(64)}`,
      primary_paths: ['services/payments/**'],
      supporting_paths: [],
      typed_index_posture: 'unavailable',
    },
    scip_index: 'absent',
    runs: [],
  }],
}

function renderPanel(node: ReactNode) {
  return render(
    <StyletronProvider value={engine}>
      <BaseProvider theme={LightTheme}>{node}</BaseProvider>
    </StyletronProvider>,
  )
}

afterEach(cleanup)

test('keeps exact paths behind one repository expansion while exposing v3 outcomes and plane counts', () => {
  renderPanel(
    <AnalysisScopePanel
      id="scope-test"
      certificate={certificate}
      callerGeneration={{
        state: 'current',
        plane: 'repository-overlay',
        repository: 'github.com/acme/checkout',
        commit,
        unit_digest: `sha256:${'a'.repeat(64)}`,
        generation_digest: `sha256:${'8'.repeat(64)}`,
        publication_revision: 11,
        record_counts: {
          candidate_records: 0,
          base_records: 3,
          excluded_go_test_records: 2,
        },
        partition_progress: {
          state: 'partial',
          settled_pair_count: 4,
          succeeded_pair_count: 3,
          refused_pair_count: 1,
        },
      }}
      gaps={[{
        repository: 'github.com/acme/payments',
        domain: 'grpc-caller',
        state: 'unavailable',
        code: 'typed_index_missing',
        reason: 'No unit-bound typed index is published.',
      }]}
    />,
  )

  const panel = screen.getByTestId('analysis-scope-panel')
  expect(panel.getAttribute('data-responsive-layout')).toBe('desktop-columns-mobile-stack')
  expect(screen.getByRole('heading', { name: 'Analysis scope & gaps' })).toBeTruthy()
  expect(screen.getByText('0 candidates')).toBeTruthy()
  expect(screen.getByText('3 base')).toBeTruthy()
  expect(screen.getByText('2 excluded go_test')).toBeTruthy()
  expect(screen.getByText(/4\/\? settled/)).toBeTruthy()
  expect(screen.getByText('1 refused')).toBeTruthy()
  const callerIdentity = screen.getByTestId('caller-generation-identity')
  expect(callerIdentity.textContent).toContain('revision 11')
  expect(callerIdentity.textContent).toContain(`commit ${commit}`)
  expect(callerIdentity.textContent).toContain(`unit sha256:${'a'.repeat(64)}`)
  expect(callerIdentity.textContent).toContain(`generation sha256:${'8'.repeat(64)}`)
  expect(screen.queryByText('services/checkout/**')).toBeNull()

  const details = screen.getByTestId('coverage-certificate-detail') as HTMLDetailsElement
  expect(details.open).toBe(false)
  fireEvent.click(screen.getByText('Coverage certificate'))
  const unavailableChip = screen.getAllByText('unavailable')
    .find((node) => node.getAttribute('data-tone') !== null)
  expect(unavailableChip?.getAttribute('data-tone')).toBe('blue')
  const checkout = screen.getByRole('button', { name: /github.com\/acme\/checkout/ })
  expect(checkout.getAttribute('aria-expanded')).toBe('false')
  expect(checkout.getAttribute('aria-controls')).toBeNull()
  fireEvent.click(checkout)
  expect(checkout.getAttribute('aria-expanded')).toBe('true')
  expect(checkout.getAttribute('aria-controls')).toBe(
    'scope-test-repository-0-github-com-acme-checkout',
  )
  expect(screen.getByText(/services\/checkout\/\*\*/).textContent).toBe(
    'services/checkout/**\ncmd/checkout/**',
  )
  expect(screen.getByText('candidate control refusal')).toBeTruthy()
  expect(screen.getByText(/Schema-only bounded receipt/)).toBeTruthy()
  fireEvent.click(screen.getByText('Bounded domain receipt'))
  expect(screen.getByText('4 opened source files')).toBeTruthy()

  const payments = screen.getByRole('button', { name: /github.com\/acme\/payments/ })
  fireEvent.click(payments)
  expect(payments.getAttribute('aria-expanded')).toBe('true')
  expect(checkout.getAttribute('aria-expanded')).toBe('false')
  expect(checkout.getAttribute('aria-controls')).toBeNull()
  expect(screen.queryByText(/services\/checkout\/\*\*/)).toBeNull()
  expect(screen.getByText('services/payments/**')).toBeTruthy()
  expect(screen.getByText(/does not widen focused source scope/)).toBeTruthy()
})

test('renders retained legacy exclusion receipts as recorded work', () => {
  const retained = JSON.parse(JSON.stringify(certificate)) as CoverageCertificate
  retained.repository_count = 1
  retained.repositories = [retained.repositories[0]]
  retained.repositories[0].runs = [retained.repositories[0].runs[0]]
  const outcome = retained.repositories[0].runs[0].outcome
  if (!outcome) throw new Error('fixture outcome is missing')
  outcome.receipt_state = 'legacy_exclusion_shape'
  outcome.receipt = {
    ...fullReceipt,
    counts: {
      corpus_files: 6,
      candidate_files: 4,
      opened_source_attempts: 4,
      opened_source_files: 4,
      facts: 3,
      atoms: 3,
      assertions: 2,
      unresolved: 1,
      staged_chunks: 1,
      staged_rows: 3,
    },
    bytes: {
      planned_declared: 1200,
      opened_source: 1000,
    },
  }

  renderPanel(<AnalysisScopePanel id="legacy-receipt" certificate={retained} />)
  fireEvent.click(screen.getByText('Coverage certificate'))
  fireEvent.click(screen.getByRole('button', { name: /github.com\/acme\/checkout/ }))

  expect(screen.queryByText(/Schema-only bounded receipt/)).toBeNull()
  fireEvent.click(screen.getByText('Bounded domain receipt'))
  expect(screen.getByText('4 candidate files')).toBeTruthy()
  expect(screen.getByText('4 opened source files')).toBeTruthy()
  expect(screen.getByText('2 assertions')).toBeTruthy()
  expect(screen.getByText('1000 opened bytes')).toBeTruthy()
})

test('bounds mounted repository summaries and reports omitted authority honestly', () => {
  const scopes: AnalysisScopeProjection[] = Array.from({ length: 25 }, (_, index) => ({
    repository: `github.com/acme/repository-${index.toString().padStart(2, '0')}`,
    commit,
    scope_posture: 'whole-repository',
  }))
  renderPanel(<AnalysisScopePanel id="bounded-scope" scopes={scopes} />)

  const details = screen.getByTestId('analysis-scope-detail')
  fireEvent.click(within(details).getByText('Exact repository scope'))
  expect(within(details).getAllByRole('button')).toHaveLength(24)
  expect(screen.getByText(/1 additional repository is not mounted/).textContent).toContain(
    '1 additional repository is not mounted',
  )
  expect(screen.queryByText('github.com/acme/repository-24')).toBeNull()
})

test('bounds capability and gap rows independently of their reported totals', () => {
  renderPanel(
    <AnalysisScopePanel
      id="bounded-rows"
      statusRows={Array.from({ length: 25 }, (_, index) => ({
        label: `capability-${index}`,
        state: 'available',
      }))}
      gaps={Array.from({ length: 25 }, (_, index) => ({
        code: `gap-${index}`,
        state: 'unavailable',
      }))}
    />,
  )

  expect(screen.getByText(/1 additional capability state is not mounted/)).toBeTruthy()
  expect(screen.getByText(/1 additional analysis gap is not mounted/)).toBeTruthy()
  expect(screen.queryByText('capability-24')).toBeNull()
  expect(screen.queryByText('gap-24')).toBeNull()
  expect(screen.getByText('25 explicit gaps')).toBeTruthy()
})

test('adapts repository status without inventing a focused unit', () => {
  const whole = analysisScopeFromRepoStatus({
    name: 'github.com/acme/whole',
    clone_url: '',
    indexed_commit_hash: commit,
    orphaned: false,
    last_index_job_state: 'unavailable',
  } satisfies RepoStatus)
  expect(whole).toEqual({
    repository: 'github.com/acme/whole',
    commit,
    scope_posture: 'whole-repository',
    analysis_unit: undefined,
  })
})

test('preserves a retained whole-repository unit and labels its replacement gap', () => {
  const status = {
    name: 'github.com/acme/legacy',
    clone_url: '',
    indexed_commit_hash: commit,
    orphaned: false,
    last_index_job_state: 'unavailable',
    analysis_unit: {
      schema: 'analysis-unit-v1',
      name: 'legacy-service',
      digest: `sha256:${'7'.repeat(64)}`,
      primary_paths: ['services/legacy/**'],
      supporting_paths: [],
      primary_path_count: 1,
      supporting_path_count: 0,
      search_index_posture: 'whole-repository',
      typed_index_posture: 'repository-root-unbound',
    },
  } satisfies RepoStatus
  const scope = analysisScopeFromRepoStatus(status)
  expect(scope.scope_posture).toBe('whole-repository')

  renderPanel(<AnalysisScopePanel id="legacy-scope" scopes={[scope]} />)
  const details = screen.getByTestId('analysis-scope-detail')
  fireEvent.click(within(details).getByText('Exact repository scope'))
  const repository = within(details).getByRole('button', {
    name: /github.com\/acme\/legacy.*service unit legacy-service/,
  })
  fireEvent.click(repository)
  expect(screen.getByText('services/legacy/**')).toBeTruthy()
  expect(screen.getByText(/active unit still uses the retained whole-repository/))
    .toBeTruthy()
})

test('keeps retained coverage failures visible and non-green beside a newer outcome', () => {
  const publishedRun = certificate.repositories[0].runs[0]
  const retained: CoverageCertificate = {
    ...certificate,
    schema_version: 'coverage-certificate-v2',
    repository_count: 1,
    repositories: [{
      ...certificate.repositories[0],
      runs: [{
        ...publishedRun,
        failures: ['parser refused one candidate'],
      }],
    }],
  }
  renderPanel(<AnalysisScopePanel id="retained-coverage" certificate={retained} />)

  fireEvent.click(screen.getByText('Coverage certificate'))
  fireEvent.click(screen.getByRole('button', {
    name: /github.com\/acme\/checkout/,
  }))
  const state = screen.getByText('published · 1 recorded failure')
  expect(state.getAttribute('data-tone')).toBe('amber')
  expect(screen.getByText(/Retained failure class: parser refused one candidate/))
    .toBeTruthy()
})

test('normalizes a legal null supporting-path selection without crashing', () => {
  const nullSupportingPaths: CoverageCertificate = {
    ...certificate,
    repository_count: 1,
    repositories: [{
      ...certificate.repositories[0],
      analysis_unit: {
        ...certificate.repositories[0].analysis_unit!,
        supporting_paths: null,
      },
      runs: [],
    }],
  }
  renderPanel(
    <AnalysisScopePanel id="null-supporting-paths" certificate={nullSupportingPaths} />,
  )

  fireEvent.click(screen.getByText('Coverage certificate'))
  fireEvent.click(screen.getByRole('button', {
    name: /github.com\/acme\/checkout/,
  }))
  expect(screen.getByText('Supporting paths · 0')).toBeTruthy()
  expect(screen.getByText('none selected')).toBeTruthy()
})

test('renders a zero-repository certificate and keeps explicit gaps non-green', () => {
  renderPanel(
    <AnalysisScopePanel
      id="empty-certificate"
      certificate={{
        schema_version: 'coverage-certificate-v3',
        domains: ['grpc-caller'],
        repository_count: 0,
        repositories: [],
        digest: `sha256:${'0'.repeat(64)}`,
      }}
      gaps={[{
        state: 'enabled',
        code: 'bounded_gap',
      }]}
    />,
  )

  expect(screen.getByText('0 repositories')).toBeTruthy()
  fireEvent.click(screen.getByText('Coverage certificate'))
  expect(screen.getByTestId('coverage-certificate-empty-universe')).toBeTruthy()
  expect(screen.getByText('enabled').getAttribute('data-tone')).toBe('amber')
})

test('does not present unavailable caller progress as measured zeroes', () => {
  renderPanel(
    <AnalysisScopePanel
      id="unavailable-caller-progress"
      callerGeneration={{
        state: 'missing',
        plane: 'repository-overlay',
        repository: 'github.com/acme/checkout',
        partition_progress: {
          state: 'unavailable',
          settled_pair_count: 0,
          succeeded_pair_count: 0,
          refused_pair_count: 0,
        },
      }}
    />,
  )

  expect(screen.getByText('partitions unavailable')).toBeTruthy()
  expect(screen.queryByText(/0\/\? settled/)).toBeNull()
  expect(screen.queryByText(/0 succeeded/)).toBeNull()
  expect(screen.queryByText(/0 refused/)).toBeNull()
})

test('normalizes a nullable transport primary-path selection defensively', () => {
  const status = {
    name: 'github.com/acme/null-primary',
    clone_url: 'https://github.com/acme/null-primary.git',
    orphaned: false,
    last_index_job_state: 'unavailable',
    analysis_unit: {
      schema: 'analysis-unit-v1',
      name: 'null-primary-service',
      digest: `sha256:${'8'.repeat(64)}`,
      // The transport schema admits null even though a valid focused state
      // requires a non-empty primary selection.
      primary_paths: null,
      supporting_paths: null,
      primary_path_count: 0,
      supporting_path_count: 0,
      search_index_posture: 'focused',
      typed_index_posture: 'repository-root-unbound',
    },
  } satisfies RepoStatus
  const scope = analysisScopeFromRepoStatus(status)

  renderPanel(<AnalysisScopePanel id="null-primary-scope" scopes={[scope]} />)
  const details = screen.getByTestId('analysis-scope-detail')
  fireEvent.click(within(details).getByText('Exact repository scope'))
  fireEvent.click(within(details).getByRole('button', {
    name: /github.com\/acme\/null-primary/,
  }))
  expect(screen.getByText('Primary paths · 0')).toBeTruthy()
  expect(screen.getByText('Supporting paths · 0')).toBeTruthy()
})
