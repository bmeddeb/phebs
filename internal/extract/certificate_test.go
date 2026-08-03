package extract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/store"
)

type certificateRunSource struct {
	runs          map[string]store.ExtractionRun
	attempts      map[string]store.ExtractionAttempt
	outcomes      map[string]store.ExtractionDomainOutcome
	runErrors     map[string]error
	outcomeErrors map[string]error
	queried       []string
}

type transitioningCertificateSource struct {
	run            store.ExtractionRun
	oldOutcome     store.ExtractionDomainOutcome
	currentOutcome store.ExtractionDomainOutcome
	settles        bool
	outcomeReads   int
}

type atomicPublishingCertificateSource struct {
	oldRun, currentRun         store.ExtractionRun
	oldAttempt, currentAttempt store.ExtractionAttempt
	oldOutcome, currentOutcome store.ExtractionDomainOutcome
	publishAfter               int
	reads                      int
}

type splitFailureCertificateSource struct {
	run               store.ExtractionRun
	staged, aborted   store.ExtractionAttempt
	published, failed store.ExtractionDomainOutcome
	failureCommitted  bool
	calls             []string
}

func (source *atomicPublishingCertificateSource) current() bool {
	source.reads++
	// Publication commits after the configured point read, so every later
	// read sees the new run, attempt, and outcome together.
	return source.reads > source.publishAfter
}

func (source *atomicPublishingCertificateSource) LatestPublishedRun(
	_ context.Context,
	_ store.ExtractionScope,
) (*store.ExtractionRun, error) {
	run := source.oldRun
	if source.current() {
		run = source.currentRun
	}
	return &run, nil
}

func (source *atomicPublishingCertificateSource) LatestExtractionAttempt(
	_ context.Context,
	_ store.ExtractionScope,
) (*store.ExtractionAttempt, error) {
	attempt := source.oldAttempt
	if source.current() {
		attempt = source.currentAttempt
	}
	return &attempt, nil
}

func (source *atomicPublishingCertificateSource) LatestExtractionDomainOutcome(
	_ context.Context,
	_ store.ExtractionScope,
) (*store.ExtractionDomainOutcome, error) {
	outcome := source.oldOutcome
	if source.current() {
		outcome = source.currentOutcome
	}
	return &outcome, nil
}

func (source *splitFailureCertificateSource) LatestPublishedRun(
	_ context.Context,
	_ store.ExtractionScope,
) (*store.ExtractionRun, error) {
	source.calls = append(source.calls, "run")
	run := source.run
	return &run, nil
}

func (source *splitFailureCertificateSource) LatestExtractionAttempt(
	_ context.Context,
	_ store.ExtractionScope,
) (*store.ExtractionAttempt, error) {
	source.calls = append(source.calls, "attempt")
	attempt := source.aborted
	if !source.failureCommitted {
		attempt = source.staged
		// Model the real split failure transition immediately after this
		// point read: abort the attempt, then persist the failure outcome.
		source.failureCommitted = true
	}
	return &attempt, nil
}

func (source *splitFailureCertificateSource) LatestExtractionDomainOutcome(
	_ context.Context,
	_ store.ExtractionScope,
) (*store.ExtractionDomainOutcome, error) {
	source.calls = append(source.calls, "outcome")
	outcome := source.published
	if source.failureCommitted {
		outcome = source.failed
	}
	return &outcome, nil
}

func (source *transitioningCertificateSource) LatestPublishedRun(
	_ context.Context,
	_ store.ExtractionScope,
) (*store.ExtractionRun, error) {
	run := source.run
	return &run, nil
}

func (source *transitioningCertificateSource) LatestExtractionAttempt(
	_ context.Context,
	_ store.ExtractionScope,
) (*store.ExtractionAttempt, error) {
	return nil, store.ErrNotFound
}

func (source *transitioningCertificateSource) LatestExtractionDomainOutcome(
	_ context.Context,
	_ store.ExtractionScope,
) (*store.ExtractionDomainOutcome, error) {
	source.outcomeReads++
	outcome := source.oldOutcome
	if source.settles && source.outcomeReads > 1 {
		outcome = source.currentOutcome
	}
	return &outcome, nil
}

func certKey(repo, domain string) string { return repo + "\x00" + domain }

func (f *certificateRunSource) LatestPublishedRun(
	_ context.Context,
	scope store.ExtractionScope,
) (*store.ExtractionRun, error) {
	f.queried = append(f.queried, "run\x00"+certKey(scope.Repository, scope.Domain))
	if err := f.runErrors[certKey(scope.Repository, scope.Domain)]; err != nil {
		return nil, err
	}
	run, ok := f.runs[certKey(scope.Repository, scope.Domain)]
	if !ok || run.Commit != scope.Commit || run.UnitDigest != scope.UnitDigest {
		return nil, store.ErrNotFound
	}
	copied := run
	return &copied, nil
}

func (f *certificateRunSource) LatestExtractionAttempt(
	_ context.Context,
	scope store.ExtractionScope,
) (*store.ExtractionAttempt, error) {
	f.queried = append(f.queried, "attempt\x00"+certKey(scope.Repository, scope.Domain))
	attempt, ok := f.attempts[certKey(scope.Repository, scope.Domain)]
	if !ok || attempt.Commit != scope.Commit || attempt.UnitDigest != scope.UnitDigest {
		return nil, store.ErrNotFound
	}
	copied := attempt
	return &copied, nil
}

func (f *certificateRunSource) LatestExtractionDomainOutcome(
	_ context.Context,
	scope store.ExtractionScope,
) (*store.ExtractionDomainOutcome, error) {
	f.queried = append(f.queried, "outcome\x00"+certKey(scope.Repository, scope.Domain))
	if err := f.outcomeErrors[certKey(scope.Repository, scope.Domain)]; err != nil {
		return nil, err
	}
	outcome, ok := f.outcomes[certKey(scope.Repository, scope.Domain)]
	if !ok || outcome.Scope != scope {
		return nil, store.ErrNotFound
	}
	copied := outcome
	return &copied, nil
}

func certRun(repo, domain, commit string, coverage store.CoverageManifest) store.ExtractionRun {
	return store.ExtractionRun{
		ID: "run-" + repo + "-" + domain, Repo: repo, Commit: commit,
		Domain: domain, Extractor: domain + "@1.0.0", Status: "published",
		Coverage: coverage,
	}
}

func certAttempt(run store.ExtractionRun, status string) store.ExtractionAttempt {
	return store.ExtractionAttempt{
		RunID: run.ID, Repo: run.Repo, Commit: run.Commit, Domain: run.Domain,
		UnitDigest: run.UnitDigest, Extractor: run.Extractor, Status: status,
	}
}

func certOutcome(
	t *testing.T,
	scope store.ExtractionScope,
	extractor string,
	disposition store.DomainOutcomeDisposition,
	fullReceipt bool,
) store.ExtractionDomainOutcome {
	t.Helper()
	generation := store.ExtractionGenerationIdentity{
		Extractor:        extractor,
		InventoryPolicy:  "gitlink-boundary-v2",
		DependencyDigest: "sha256:" + strings.Repeat("d", 64),
	}
	generation.Digest = store.ComputeExtractionGenerationDigest(generation)
	receipt := `{"schema":"` + store.ExtractionOutcomeReceiptSchema + `"}`
	if fullReceipt {
		reason := OperationReasonFailed
		switch disposition {
		case store.DomainOutcomePublished:
			reason = OperationReasonPublishedNonempty
		case store.DomainOutcomeUnavailablePrerequisite:
			reason = OperationReasonTypedInputAbsent
		case store.DomainOutcomeTerminalGenerationRefusal:
			reason = OperationReasonLimitRefusal
		}
		encoded, err := json.Marshal(ExtractionDomainOutcomeReceipt{
			Schema: store.ExtractionOutcomeReceiptSchema,
			Domain: scope.Domain, ExtractorVersion: extractor,
			Disposition: disposition, Reason: reason,
			Counts: ExtractionOperationDomainCounts{
				CorpusFiles: 8, CandidateFiles: 5, ExcludedSourceFiles: 2,
			},
			Bytes:  ExtractionOperationDomainBytes{PlannedDeclared: 123},
			Limits: certificateTestReceiptLimits(),
		})
		if err != nil {
			t.Fatal(err)
		}
		receipt = string(encoded)
	}
	outcome := store.ExtractionDomainOutcome{
		Scope: scope, Disposition: disposition, Generation: generation,
		ReceiptSchema: store.ExtractionOutcomeReceiptSchema,
		Receipt:       receipt, RecordedAt: time.Unix(1_700_000_000, 0).UTC(),
	}
	if disposition == store.DomainOutcomePublished {
		outcome.RunID = "run-" + scope.Repository + "-" + scope.Domain
	}
	return outcome
}

func certificateTestReceiptLimits() ExtractionOperationDomainLimits {
	return ExtractionOperationDomainLimits{
		CorpusFiles:            100,
		OpenedSourceAttempts:   400,
		OpenedSourceFiles:      100,
		OpenedSourceBytes:      1 << 20,
		Facts:                  100,
		SourceBlobBytes:        1 << 16,
		TypedInputBytes:        1 << 16,
		AggregateWallMS:        15 * 60 * 1000,
		MirrorLockMS:           5 * 60 * 1000,
		DomainWallMS:           5 * 60 * 1000,
		AbortWallMS:            10 * 1000,
		OutcomeWallMS:          1000,
		MaxSerialDomains:       17,
		SchedulerIdentityBytes: 1 << 16,
		AggregateStagedRows:    100,
		DomainStagedRows:       100,
	}
}

var (
	commitA = strings.Repeat("a", 40)
	commitB = strings.Repeat("b", 40)
)

func TestCoverageCertificateDeterministicOverVisibleUniverse(t *testing.T) {
	source := &certificateRunSource{runs: map[string]store.ExtractionRun{
		certKey("alpha", "proto-contract"): certRun("alpha", "proto-contract", commitA, store.CoverageManifest{
			Protocols: []string{"protobuf"}, CorpusFileCount: 9, CandidateFileCount: 4,
			ReadFileCount: 5, ReadBytes: 1234, SourceScopeDigest: "sha256:scope-alpha",
			UnresolvedCount: 2, AssertionCount: 5, AtomCount: 7,
		}),
		certKey("alpha", "scip-proto-field"): certRun("alpha", "scip-proto-field", commitA, store.CoverageManifest{
			Protocols: []string{"scip", "protobuf-generated-accessor-v1"}, AssertionCount: 1, AtomCount: 1,
		}),
		certKey("gamma", "scip-proto-field"): certRun("gamma", "scip-proto-field", commitB, store.CoverageManifest{
			Protocols: []string{"scip-index-absent"},
		}),
	}}
	visible := []store.Repo{
		{Name: "gamma", IndexedCommitHash: commitB},
		{Name: "alpha", IndexedCommitHash: commitA},
		{Name: "beta", IndexedCommitHash: commitB},
	}
	domains := []string{"scip-proto-field", "proto-contract"}
	first, err := BuildCoverageCertificate(context.Background(), source, visible, domains)
	if err != nil {
		t.Fatalf("BuildCoverageCertificate: %v", err)
	}
	second, err := BuildCoverageCertificate(context.Background(), source, visible, domains)
	if err != nil {
		t.Fatalf("second build: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("two builds over equal state differ:\n%+v\n%+v", first, second)
	}
	if first.SchemaVersion != "coverage-certificate-v3" || !strings.HasPrefix(first.Digest, "sha256:") {
		t.Fatalf("schema/digest = %q %q", first.SchemaVersion, first.Digest)
	}
	if got, want := first.Domains, []string{"proto-contract", "scip-proto-field"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("domains = %v, want sorted %v", got, want)
	}
	if first.RepositoryCount != 3 || len(first.Repositories) != 3 {
		t.Fatalf("repository count = %d/%d, want the whole visible universe", first.RepositoryCount, len(first.Repositories))
	}
	rows := []struct {
		index     string
		scipIndex string
		statuses  []string
	}{
		{index: "alpha", scipIndex: "present", statuses: []string{"published", "published"}},
		{index: "beta", scipIndex: "unknown", statuses: []string{"unpublished", "unpublished"}},
		{index: "gamma", scipIndex: "absent", statuses: []string{"unpublished", "published"}},
	}
	for i, row := range rows {
		repo := first.Repositories[i]
		if repo.Repository != row.index || repo.SCIPIndex != row.scipIndex {
			t.Fatalf("repository[%d] = %q scip=%q, want %q scip=%q", i, repo.Repository, repo.SCIPIndex, row.index, row.scipIndex)
		}
		for j, status := range row.statuses {
			if repo.Runs[j].Status != status {
				t.Fatalf("%s run[%d] status = %q, want %q", row.index, j, repo.Runs[j].Status, status)
			}
		}
	}
	alpha := first.Repositories[0].Runs[0]
	if alpha.Domain != "proto-contract" || !alpha.Fresh || alpha.UnresolvedCount != 2 ||
		alpha.RunID == "" || alpha.CorpusFileCount != 9 || alpha.CandidateFileCount != 4 ||
		alpha.ReadFileCount != 5 || alpha.ReadBytes != 1234 || alpha.SourceScopeDigest != "sha256:scope-alpha" {
		t.Fatalf("alpha proto-contract run = %+v", alpha)
	}
}

func TestCoverageCertificateBindsFocusedUnitAndCandidateScope(t *testing.T) {
	unitA, err := (analysisunit.Config{
		Name:       "payments",
		Primary:    []string{"services/payments/src"},
		Supporting: []string{"contracts/payment.proto", "services/payments/index.scip"},
		TypedIndex: &analysisunit.TypedIndex{
			Kind: analysisunit.TypedIndexKindSCIP,
			Path: "services/payments/index.scip",
		},
	}).Scope("alpha").State()
	if err != nil {
		t.Fatal(err)
	}
	unitB, err := (analysisunit.Config{
		Name:       "billing",
		Primary:    []string{"services/billing"},
		Supporting: []string{"contracts/billing.proto"},
	}).Scope("alpha").State()
	if err != nil {
		t.Fatal(err)
	}
	digestA := "sha256:" + strings.Repeat("a", 64)
	digestB := "sha256:" + strings.Repeat("b", 64)
	run := certRun("alpha", "proto-contract", commitA, store.CoverageManifest{
		SourceScopeDigest:           digestA,
		ScopePosture:                "focused-local",
		CandidateManifestDigest:     digestB,
		CandidatePlane:              "local",
		ScopeCorpusFileCount:        3,
		ScopeCorpusDeclaredBytes:    300,
		ScopeCorpusDigest:           digestA,
		PlannedFileCount:            4,
		PlannedRequiredFileCount:    1,
		PlannedDeclaredBytes:        200,
		PlannedScopeDigest:          digestB,
		ExcludedSourceFileCount:     2,
		ExcludedSourceRequiredCount: 1,
		ExcludedSourceDeclaredBytes: 75,
		ExcludedSCIPDocumentCount:   3,
		ExcludedSCIPDefinitionCount: 4,
		ExcludedSCIPOccurrenceCount: 5,
		TypedInputKind:              analysisunit.TypedIndexKindSCIP,
		TypedInputPath:              "services/payments/index.scip",
		TypedInputObjectID:          strings.Repeat("c", 40),
		TypedInputDeclaredBytes:     100,
		TypedInputDigest:            digestA,
		TypedInputPresent:           true,
		CorpusFileCount:             3,
		CandidateFileCount:          1,
		ReadFileCount:               2,
		ReadBytes:                   200,
	})
	run.UnitDigest = unitA.Digest
	source := &certificateRunSource{
		runs: map[string]store.ExtractionRun{
			certKey("alpha", "proto-contract"): run,
		},
	}
	focused, err := BuildCoverageCertificate(
		context.Background(),
		source,
		[]store.Repo{{
			Name: "alpha", IndexedCommitHash: commitA,
			IndexedAnalysisUnit: unitA,
		}},
		[]string{"proto-contract"},
	)
	if err != nil {
		t.Fatal(err)
	}
	repository := focused.Repositories[0]
	if repository.ScopePosture != "focused" ||
		repository.AnalysisUnit == nil ||
		repository.AnalysisUnit.Digest != unitA.Digest ||
		repository.AnalysisUnit.TypedIndexPath != "services/payments/index.scip" {
		t.Fatalf("focused repository scope = %+v", repository)
	}
	covered := repository.Runs[0]
	if !covered.Fresh ||
		covered.UnitDigest != unitA.Digest ||
		covered.EvidenceScopePosture != "focused-local" ||
		covered.CandidateScope == nil ||
		covered.CandidateScope.ManifestDigest != digestB ||
		covered.CandidateScope.PlannedFileCount != 4 ||
		covered.CandidateScope.BaseSourceFileCount == nil ||
		*covered.CandidateScope.BaseSourceFileCount != 2 ||
		covered.CandidateScope.ExcludedGoTestCount == nil ||
		*covered.CandidateScope.ExcludedGoTestCount != 2 ||
		covered.CandidateScope.ExcludedSourceFileCount != 2 ||
		covered.CandidateScope.ExcludedSourceRequiredCount != 1 ||
		covered.CandidateScope.ExcludedSourceDeclaredBytes != 75 ||
		covered.CandidateScope.ExcludedSCIPDocumentCount != 3 ||
		covered.CandidateScope.ExcludedSCIPDefinitionCount != 4 ||
		covered.CandidateScope.ExcludedSCIPOccurrenceCount != 5 ||
		covered.CandidateScope.TypedInput == nil ||
		!covered.CandidateScope.TypedInput.Present {
		t.Fatalf("focused run coverage = %+v", covered)
	}
	wholeLaneCoverage := run.Coverage
	wholeLaneCoverage.ScopePosture = "repository-overlay"
	wholeLaneCoverage.CandidatePlane = "caller"
	wholeLaneCoverage.ExcludedSourceFileCount = 0
	wholeLaneCoverage.ExcludedSourceRequiredCount = 0
	wholeLaneCoverage.ExcludedSourceDeclaredBytes = 0
	wholeLaneCoverage.ExcludedSCIPDocumentCount = 0
	wholeLaneCoverage.ExcludedSCIPDefinitionCount = 0
	wholeLaneCoverage.ExcludedSCIPOccurrenceCount = 0
	wholeLaneScope, err := certificateCandidateScope(wholeLaneCoverage)
	if err != nil {
		t.Fatal(err)
	}
	if wholeLaneScope.BaseSourceFileCount != nil ||
		wholeLaneScope.ExcludedGoTestCount != nil {
		t.Fatalf("repository-overlay coverage invented lane counts: %+v", wholeLaneScope)
	}

	other, err := BuildCoverageCertificate(
		context.Background(),
		source,
		[]store.Repo{{
			Name: "alpha", IndexedCommitHash: commitA,
			IndexedAnalysisUnit: unitB,
		}},
		[]string{"proto-contract"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if other.Repositories[0].Runs[0].Status != "unpublished" ||
		other.Repositories[0].Runs[0].UnitDigest != unitB.Digest ||
		other.Digest == focused.Digest {
		t.Fatalf("different same-HEAD unit reused focused evidence: %+v", other)
	}

	legacyUnit := analysisunit.CloneState(unitB)
	legacyUnit.SearchIndexPosture = analysisunit.SearchIndexWholeRepository
	legacy, err := BuildCoverageCertificate(
		context.Background(),
		source,
		[]store.Repo{{
			Name: "alpha", IndexedCommitHash: commitA,
			IndexedAnalysisUnit: legacyUnit,
		}},
		[]string{"proto-contract"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.Repositories[0].ScopePosture != analysisunit.SearchIndexWholeRepository ||
		legacy.Repositories[0].AnalysisUnit == nil {
		t.Fatalf("legacy T30.2 scope posture was rewritten: %+v", legacy.Repositories[0])
	}
}

func TestCoverageCertificateCandidateScopePreservesRetainedV2Shape(t *testing.T) {
	legacyJSON := `{"manifest_digest":"sha256:` + strings.Repeat("a", 64) +
		`","plane":"caller","corpus_file_count":3,"corpus_declared_bytes":30,` +
		`"corpus_digest":"sha256:` + strings.Repeat("b", 64) +
		`","planned_file_count":2,"planned_required_file_count":1,` +
		`"planned_declared_bytes":20,"planned_digest":"sha256:` +
		strings.Repeat("c", 64) + `"}`
	var scope CertificateCandidateScope
	if err := json.Unmarshal([]byte(legacyJSON), &scope); err != nil {
		t.Fatal(err)
	}
	roundTrip, err := json.Marshal(scope)
	if err != nil {
		t.Fatal(err)
	}
	if string(roundTrip) != legacyJSON {
		t.Fatalf("retained candidate scope changed: %s", roundTrip)
	}
	certificate := &CoverageCertificate{
		SchemaVersion: "coverage-certificate-v2",
		Repositories: []CertificateRepository{{
			Repository: "alpha",
			Runs: []CertificateRun{{
				Domain: "grpc-consumer", CandidateScope: &scope,
			}},
		}},
	}
	if err := certificate.ValidateCanonicalShape(); err != nil {
		t.Fatalf("retained v2 candidate scope was rejected: %v", err)
	}
	certificate.SchemaVersion = certificateSchemaVersion
	if err := certificate.ValidateCanonicalShape(); err == nil {
		t.Fatal("v3 certificate accepted omitted exclusion counters")
	}
}

func TestCoverageCertificateV3CandidateScopeSourceLanePresence(t *testing.T) {
	zero := 0
	newCertificate := func(
		schema, posture, plane string,
		base, excludedGoTest *int,
	) *CoverageCertificate {
		return &CoverageCertificate{
			SchemaVersion: schema,
			Repositories: []CertificateRepository{{
				Repository: "alpha",
				Runs: []CertificateRun{{
					Domain:               "grpc-consumer",
					EvidenceScopePosture: posture,
					CandidateScope: &CertificateCandidateScope{
						Plane:               plane,
						BaseSourceFileCount: base,
						ExcludedGoTestCount: excludedGoTest,
					},
				}},
			}},
		}
	}
	tests := []struct {
		name              string
		schema            string
		posture           string
		plane             string
		base              *int
		excludedGoTest    *int
		wantErr           bool
		wantMarshalFields bool
	}{
		{
			name:    "focused local carries explicit zeroes",
			schema:  certificateSchemaVersion,
			posture: "focused-local", plane: "local",
			base: &zero, excludedGoTest: &zero,
			wantMarshalFields: true,
		},
		{
			name:    "focused local missing base",
			schema:  certificateSchemaVersion,
			posture: "focused-local", plane: "local",
			excludedGoTest: &zero, wantErr: true,
		},
		{
			name:    "focused local missing excluded go test",
			schema:  certificateSchemaVersion,
			posture: "focused-local", plane: "local",
			base: &zero, wantErr: true,
		},
		{
			name:    "focused local missing both",
			schema:  certificateSchemaVersion,
			posture: "focused-local", plane: "local",
			wantErr: true,
		},
		{
			name:    "repository overlay omits both",
			schema:  certificateSchemaVersion,
			posture: "repository-overlay", plane: "caller",
		},
		{
			name:    "repository overlay carries base",
			schema:  certificateSchemaVersion,
			posture: "repository-overlay", plane: "caller",
			base: &zero, wantErr: true,
		},
		{
			name:    "repository overlay carries excluded go test",
			schema:  certificateSchemaVersion,
			posture: "repository-overlay", plane: "caller",
			excludedGoTest: &zero, wantErr: true,
		},
		{
			name:    "whole repository carries both",
			schema:  certificateSchemaVersion,
			posture: "whole-repository", plane: "repository",
			base: &zero, excludedGoTest: &zero, wantErr: true,
		},
		{
			name:    "retained v2 focused omission remains readable",
			schema:  "coverage-certificate-v2",
			posture: "focused-local", plane: "local",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			certificate := newCertificate(
				test.schema, test.posture, test.plane,
				test.base, test.excludedGoTest,
			)
			err := certificate.ValidateCanonicalShape()
			if (err != nil) != test.wantErr {
				t.Fatalf("ValidateCanonicalShape() error = %v, wantErr %t", err, test.wantErr)
			}
			encoded, marshalErr := json.Marshal(certificate.Repositories[0].Runs[0].CandidateScope)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			hasBase := strings.Contains(string(encoded), `"base_source_file_count":0`)
			hasExcluded := strings.Contains(string(encoded), `"excluded_go_test_file_count":0`)
			if test.wantMarshalFields && (!hasBase || !hasExcluded) {
				t.Fatalf("focused-local/local zero counts were not explicit: %s", encoded)
			}
			if !test.wantErr && !test.wantMarshalFields &&
				(hasBase || hasExcluded) {
				t.Fatalf("non-focused or retained shape invented source-lane counts: %s", encoded)
			}
		})
	}
}

func TestCoverageCertificateProjectsDurableDomainOutcomes(t *testing.T) {
	dispositions := []store.DomainOutcomeDisposition{
		store.DomainOutcomePublished,
		store.DomainOutcomeUnavailablePrerequisite,
		store.DomainOutcomeTerminalGenerationRefusal,
		store.DomainOutcomeRetryableFailure,
	}
	for _, disposition := range dispositions {
		t.Run(string(disposition), func(t *testing.T) {
			domain := "proto-contract"
			scope := store.ExtractionScope{
				Repository: "alpha", Commit: commitA, Domain: domain,
			}
			outcome := certOutcome(
				t, scope, domain+"@1.0.0", disposition, true,
			)
			source := &certificateRunSource{
				outcomes: map[string]store.ExtractionDomainOutcome{
					certKey(scope.Repository, domain): outcome,
				},
			}
			if disposition == store.DomainOutcomePublished {
				source.runs = map[string]store.ExtractionRun{
					certKey(scope.Repository, domain): certRun(
						scope.Repository, domain, scope.Commit,
						store.CoverageManifest{},
					),
				}
			}
			certificate, err := BuildCoverageCertificate(
				context.Background(), source,
				[]store.Repo{{Name: scope.Repository, IndexedCommitHash: scope.Commit}},
				[]string{domain},
			)
			if err != nil {
				t.Fatal(err)
			}
			got := certificate.Repositories[0].Runs[0].Outcome
			if got == nil || got.Disposition != disposition ||
				got.GenerationDigest != outcome.Generation.Digest ||
				got.Extractor != outcome.Generation.Extractor ||
				got.ReceiptSchema != store.ExtractionOutcomeReceiptSchema ||
				got.ReceiptState != "full" || got.Receipt == nil ||
				got.Receipt.Domain != domain ||
				got.Receipt.Disposition != disposition {
				t.Fatalf("projected outcome = %+v", got)
			}
			encoded, err := json.Marshal(certificate)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), "recorded_at") ||
				strings.Contains(string(encoded), outcome.RecordedAt.Format(time.RFC3339)) {
				t.Fatalf("certificate leaked outcome time: %s", encoded)
			}
		})
	}
}

func TestCoverageCertificateAcceptsRetryablePublishFailureReasons(t *testing.T) {
	tests := []struct {
		name   string
		stats  corpusStats
		facts  int
		reason string
	}{
		{
			name: "published nonempty",
			stats: corpusStats{
				candidateFileCount: 1,
			},
			facts:  1,
			reason: OperationReasonPublishedNonempty,
		},
		{
			name: "typed input absent",
			stats: corpusStats{
				candidateFileCount: 1,
				typedInputKind:     analysisunit.TypedIndexKindSCIP,
			},
			reason: OperationReasonTypedInputAbsent,
		},
		{
			name:   "no candidates",
			stats:  corpusStats{},
			reason: OperationReasonNoCandidates,
		},
		{
			name: "published empty",
			stats: corpusStats{
				candidateFileCount: 1,
			},
			reason: OperationReasonPublishedEmpty,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			domain := "proto-contract"
			scope := store.ExtractionScope{
				Repository: "alpha", Commit: commitA, Domain: domain,
			}
			outcome := certOutcome(
				t, scope, domain+"@1.0.0",
				store.DomainOutcomeRetryableFailure, true,
			)
			var receipt ExtractionDomainOutcomeReceipt
			if err := json.Unmarshal([]byte(outcome.Receipt), &receipt); err != nil {
				t.Fatal(err)
			}
			receipt.Reason = successfulOperationReason(test.stats, test.facts)
			if receipt.Reason != test.reason {
				t.Fatalf("writer reason = %q, want %q", receipt.Reason, test.reason)
			}
			encoded, err := json.Marshal(receipt)
			if err != nil {
				t.Fatal(err)
			}
			outcome.Receipt = string(encoded)

			certificate, err := BuildCoverageCertificate(
				context.Background(),
				&certificateRunSource{outcomes: map[string]store.ExtractionDomainOutcome{
					certKey(scope.Repository, domain): outcome,
				}},
				[]store.Repo{{Name: scope.Repository, IndexedCommitHash: scope.Commit}},
				[]string{domain},
			)
			if err != nil {
				t.Fatalf("writer-compatible retryable receipt was rejected: %v", err)
			}
			got := certificate.Repositories[0].Runs[0].Outcome
			if got == nil || got.Receipt == nil ||
				got.Receipt.Reason != test.reason {
				t.Fatalf("projected outcome = %+v", got)
			}
		})
	}
}

func TestCoverageCertificateOutcomeSchemaOnlyReceiptIsExplicit(t *testing.T) {
	domain := "proto-contract"
	scope := store.ExtractionScope{
		Repository: "alpha", Commit: commitA, Domain: domain,
	}
	outcome := certOutcome(
		t, scope, domain+"@1.0.0", store.DomainOutcomeRetryableFailure, false,
	)
	certificate, err := BuildCoverageCertificate(
		context.Background(),
		&certificateRunSource{outcomes: map[string]store.ExtractionDomainOutcome{
			certKey(scope.Repository, domain): outcome,
		}},
		[]store.Repo{{Name: scope.Repository, IndexedCommitHash: scope.Commit}},
		[]string{domain},
	)
	if err != nil {
		t.Fatal(err)
	}
	got := certificate.Repositories[0].Runs[0].Outcome
	if got == nil || got.ReceiptState != "schema_only" || got.Receipt != nil ||
		got.ReceiptSchema != store.ExtractionOutcomeReceiptSchema {
		t.Fatalf("schema-only outcome = %+v", got)
	}
}

func TestCoverageCertificateOutcomeTimestampDoesNotChangeDigest(t *testing.T) {
	domain := "proto-contract"
	scope := store.ExtractionScope{
		Repository: "alpha", Commit: commitA, Domain: domain,
	}
	outcome := certOutcome(
		t, scope, domain+"@1.0.0", store.DomainOutcomeRetryableFailure, true,
	)
	build := func(value store.ExtractionDomainOutcome) string {
		t.Helper()
		certificate, err := BuildCoverageCertificate(
			context.Background(),
			&certificateRunSource{outcomes: map[string]store.ExtractionDomainOutcome{
				certKey(scope.Repository, domain): value,
			}},
			[]store.Repo{{Name: scope.Repository, IndexedCommitHash: scope.Commit}},
			[]string{domain},
		)
		if err != nil {
			t.Fatal(err)
		}
		return certificate.Digest
	}
	baseline := build(outcome)
	outcome.RecordedAt = outcome.RecordedAt.Add(24 * time.Hour)
	if changed := build(outcome); changed != baseline {
		t.Fatalf("outcome timestamp changed certificate digest: %s != %s", changed, baseline)
	}
}

func TestCoverageCertificateRetriesAtomicPublicationTransition(t *testing.T) {
	domain := "proto-contract"
	scope := store.ExtractionScope{
		Repository: "alpha", Commit: commitA, Domain: domain,
	}
	run := certRun(scope.Repository, domain, scope.Commit, store.CoverageManifest{})
	run.ID = "run-new"
	oldOutcome := certOutcome(
		t, scope, run.Extractor, store.DomainOutcomePublished, true,
	)
	oldOutcome.RunID = "run-old"
	currentOutcome := oldOutcome
	currentOutcome.RunID = run.ID

	for _, test := range []struct {
		name         string
		publishAfter int
	}{
		{name: "publication after run read", publishAfter: 1},
		{name: "publication after outcome read", publishAfter: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			oldRun := run
			oldRun.ID = "run-old"
			oldAttempt := store.ExtractionAttempt{
				RunID: oldRun.ID, Repo: scope.Repository, Commit: scope.Commit,
				Domain: domain, Extractor: run.Extractor, Status: "published",
			}
			currentAttempt := oldAttempt
			currentAttempt.RunID = run.ID
			oldRetryable := certOutcome(
				t, scope, run.Extractor,
				store.DomainOutcomeRetryableFailure, true,
			)
			currentPublished := certOutcome(
				t, scope, run.Extractor, store.DomainOutcomePublished, true,
			)
			currentPublished.RunID = run.ID
			source := &atomicPublishingCertificateSource{
				oldRun: oldRun, currentRun: run,
				oldAttempt: oldAttempt, currentAttempt: currentAttempt,
				oldOutcome: oldRetryable, currentOutcome: currentPublished,
				publishAfter: test.publishAfter,
			}
			certificate, err := BuildCoverageCertificate(
				context.Background(), source,
				[]store.Repo{{Name: scope.Repository, IndexedCommitHash: scope.Commit}},
				[]string{domain},
			)
			if err != nil {
				t.Fatal(err)
			}
			got := certificate.Repositories[0].Runs[0]
			if source.reads != 6 || got.RunID != run.ID || got.Outcome == nil ||
				got.Outcome.Disposition != store.DomainOutcomePublished {
				t.Fatalf(
					"atomic point-read transition = %d reads / %+v",
					source.reads, got,
				)
			}
		})
	}

	t.Run("settles on the next coherent read", func(t *testing.T) {
		source := &transitioningCertificateSource{
			run: run, oldOutcome: oldOutcome, currentOutcome: currentOutcome,
			settles: true,
		}
		certificate, err := BuildCoverageCertificate(
			context.Background(), source,
			[]store.Repo{{Name: scope.Repository, IndexedCommitHash: scope.Commit}},
			[]string{domain},
		)
		if err != nil {
			t.Fatal(err)
		}
		if source.outcomeReads != 2 ||
			certificate.Repositories[0].Runs[0].Outcome == nil ||
			certificate.Repositories[0].Runs[0].RunID != run.ID {
			t.Fatalf(
				"transition retry reads/result = %d / %+v",
				source.outcomeReads, certificate.Repositories[0].Runs[0],
			)
		}
	})

	t.Run("persistent transition is a typed conflict", func(t *testing.T) {
		source := &transitioningCertificateSource{
			run: run, oldOutcome: oldOutcome, currentOutcome: currentOutcome,
		}
		_, err := BuildCoverageCertificate(
			context.Background(), source,
			[]store.Repo{{Name: scope.Repository, IndexedCommitHash: scope.Commit}},
			[]string{domain},
		)
		if !errors.Is(err, store.ErrConflict) ||
			source.outcomeReads != certificateDomainSnapshotAttempts {
			t.Fatalf(
				"persistent transition = %v after %d reads",
				err, source.outcomeReads,
			)
		}
	})
}

func TestCoverageCertificateDoesNotMixSplitFailureTransition(t *testing.T) {
	domain := "proto-contract"
	scope := store.ExtractionScope{
		Repository: "alpha", Commit: commitA, Domain: domain,
	}
	run := certRun(scope.Repository, domain, scope.Commit, store.CoverageManifest{})
	published := certOutcome(
		t, scope, run.Extractor, store.DomainOutcomePublished, true,
	)
	published.RunID = run.ID
	failed := certOutcome(
		t, scope, run.Extractor, store.DomainOutcomeRetryableFailure, true,
	)
	staged := certAttempt(run, "staged")
	staged.RunID = "replacement-run"
	aborted := staged
	aborted.Status = "aborted"
	source := &splitFailureCertificateSource{
		run: run, staged: staged, aborted: aborted,
		published: published, failed: failed,
	}

	certificate, err := BuildCoverageCertificate(
		context.Background(), source,
		[]store.Repo{{Name: scope.Repository, IndexedCommitHash: scope.Commit}},
		[]string{domain},
	)
	if err != nil {
		t.Fatal(err)
	}
	got := certificate.Repositories[0].Runs[0]
	if !reflect.DeepEqual(source.calls, []string{"run", "outcome", "attempt"}) ||
		got.LatestAttempt == nil || got.LatestAttempt.Status != "staged" ||
		got.Outcome == nil || got.Outcome.Disposition != store.DomainOutcomePublished {
		t.Fatalf("split failure point reads = %v / %+v", source.calls, got)
	}
}

func TestCoverageCertificateRejectsInconsistentDomainOutcomeReceipt(t *testing.T) {
	domain := "proto-contract"
	scope := store.ExtractionScope{
		Repository: "alpha", Commit: commitA, Domain: domain,
	}
	baseline := certOutcome(
		t, scope, domain+"@1.0.0", store.DomainOutcomeRetryableFailure, true,
	)
	mutateReceipt := func(
		outcome *store.ExtractionDomainOutcome,
		mutate func(*ExtractionDomainOutcomeReceipt),
	) {
		var receipt ExtractionDomainOutcomeReceipt
		if err := json.Unmarshal([]byte(outcome.Receipt), &receipt); err != nil {
			t.Fatal(err)
		}
		mutate(&receipt)
		encoded, err := json.Marshal(receipt)
		if err != nil {
			t.Fatal(err)
		}
		outcome.Receipt = string(encoded)
	}
	tests := []struct {
		name   string
		mutate func(*store.ExtractionDomainOutcome)
	}{
		{name: "domain", mutate: func(outcome *store.ExtractionDomainOutcome) {
			var receipt ExtractionDomainOutcomeReceipt
			if err := json.Unmarshal([]byte(outcome.Receipt), &receipt); err != nil {
				t.Fatal(err)
			}
			receipt.Domain = "other"
			encoded, _ := json.Marshal(receipt)
			outcome.Receipt = string(encoded)
		}},
		{name: "extractor", mutate: func(outcome *store.ExtractionDomainOutcome) {
			var receipt ExtractionDomainOutcomeReceipt
			if err := json.Unmarshal([]byte(outcome.Receipt), &receipt); err != nil {
				t.Fatal(err)
			}
			receipt.ExtractorVersion = "2.0.0"
			encoded, _ := json.Marshal(receipt)
			outcome.Receipt = string(encoded)
		}},
		{name: "disposition", mutate: func(outcome *store.ExtractionDomainOutcome) {
			var receipt ExtractionDomainOutcomeReceipt
			if err := json.Unmarshal([]byte(outcome.Receipt), &receipt); err != nil {
				t.Fatal(err)
			}
			receipt.Disposition = store.DomainOutcomeTerminalGenerationRefusal
			encoded, _ := json.Marshal(receipt)
			outcome.Receipt = string(encoded)
		}},
		{name: "unknown field", mutate: func(outcome *store.ExtractionDomainOutcome) {
			outcome.Receipt = strings.TrimSuffix(outcome.Receipt, "}") + `,"diagnostic":"raw"}`
		}},
		{name: "reason incompatible with disposition", mutate: func(outcome *store.ExtractionDomainOutcome) {
			mutateReceipt(outcome, func(receipt *ExtractionDomainOutcomeReceipt) {
				receipt.Reason = OperationReasonAlreadyCurrent
			})
		}},
		{name: "opened files exceed attempts", mutate: func(outcome *store.ExtractionDomainOutcome) {
			mutateReceipt(outcome, func(receipt *ExtractionDomainOutcomeReceipt) {
				receipt.Counts.OpenedSourceFiles = 1
			})
		}},
		{name: "candidate and excluded files overlap", mutate: func(outcome *store.ExtractionDomainOutcome) {
			mutateReceipt(outcome, func(receipt *ExtractionDomainOutcomeReceipt) {
				receipt.Counts.CandidateFiles = receipt.Counts.CorpusFiles - 1
				receipt.Counts.ExcludedSourceFiles = 2
			})
		}},
		{name: "excluded bytes exceed plan", mutate: func(outcome *store.ExtractionDomainOutcome) {
			mutateReceipt(outcome, func(receipt *ExtractionDomainOutcomeReceipt) {
				receipt.Counts.ExcludedSourceFiles = 1
				receipt.Bytes.ExcludedSourceDeclared =
					receipt.Bytes.PlannedDeclared + 1
			})
		}},
		{name: "zero frozen limit", mutate: func(outcome *store.ExtractionDomainOutcome) {
			mutateReceipt(outcome, func(receipt *ExtractionDomainOutcomeReceipt) {
				receipt.Limits.DomainWallMS = 0
			})
		}},
		{name: "generation digest", mutate: func(outcome *store.ExtractionDomainOutcome) {
			outcome.Generation.Digest = "sha256:" + strings.Repeat("e", 64)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			outcome := baseline
			test.mutate(&outcome)
			_, err := BuildCoverageCertificate(
				context.Background(),
				&certificateRunSource{outcomes: map[string]store.ExtractionDomainOutcome{
					certKey(scope.Repository, domain): outcome,
				}},
				[]store.Repo{{Name: scope.Repository, IndexedCommitHash: scope.Commit}},
				[]string{domain},
			)
			if err == nil {
				t.Fatal("inconsistent durable outcome entered a certificate")
			}
		})
	}
}

func TestCoverageCertificateDisclosesApplicableAbsentTypedInput(
	t *testing.T,
) {
	unit, err := (analysisunit.Config{
		Name:    "payments",
		Primary: []string{"services/payments"},
	}).Scope("alpha").State()
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	manifestDigest := "sha256:" + strings.Repeat("b", 64)
	run := certRun(
		"alpha",
		"scip-proto-field",
		commitA,
		store.CoverageManifest{
			SourceScopeDigest:       digest,
			ScopePosture:            "focused-local",
			CandidateManifestDigest: manifestDigest,
			CandidatePlane:          "local",
			ScopeCorpusDigest:       digest,
			PlannedScopeDigest:      digest,
			TypedInputKind:          analysisunit.TypedIndexKindSCIP,
			Protocols:               []string{"scip-index-absent"},
		},
	)
	run.UnitDigest = unit.Digest
	certificate, err := BuildCoverageCertificate(
		context.Background(),
		&certificateRunSource{
			runs: map[string]store.ExtractionRun{
				certKey("alpha", "scip-proto-field"): run,
			},
		},
		[]store.Repo{{
			Name: "alpha", IndexedCommitHash: commitA,
			IndexedAnalysisUnit: unit,
		}},
		[]string{"scip-proto-field"},
	)
	if err != nil {
		t.Fatal(err)
	}
	candidateScope := certificate.Repositories[0].Runs[0].CandidateScope
	if candidateScope == nil {
		t.Fatal("candidate scope omitted for explicit zero-count projection")
	}
	typed := candidateScope.TypedInput
	if typed == nil ||
		typed.Kind != analysisunit.TypedIndexKindSCIP ||
		typed.Present ||
		typed.Path != "" ||
		typed.ObjectID != "" ||
		typed.DeclaredBytes != 0 ||
		typed.Digest != "" {
		t.Fatalf("applicable absent typed input = %+v", typed)
	}
	data, err := json.Marshal(certificate)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(
		string(data),
		`"typed_input":{"kind":"scip","declared_bytes":0,"present":false}`,
	) {
		t.Fatalf("certificate omitted explicit typed absence: %s", data)
	}
}

func TestCoverageCertificateRefusesInvalidStoredCoverage(t *testing.T) {
	const (
		repository = "alpha"
		domain     = "proto-contract"
	)
	_, err := BuildCoverageCertificate(
		context.Background(),
		&certificateRunSource{
			runErrors: map[string]error{
				certKey(repository, domain): fmt.Errorf(
					"latest published run: invalid stored coverage",
				),
			},
		},
		[]store.Repo{{
			Name: repository, IndexedCommitHash: commitA,
		}},
		[]string{domain},
	)
	if err == nil ||
		!strings.Contains(err.Error(), "invalid stored coverage") {
		t.Fatalf("invalid stored coverage certificate = %v", err)
	}
}

func TestCoverageCertificateExcludesPublicationTime(t *testing.T) {
	key := certKey("alpha", "proto-contract")
	run := certRun("alpha", "proto-contract", commitA, store.CoverageManifest{
		SourceScopeDigest: "sha256:scope",
	})
	firstTime := time.Date(2026, 7, 22, 1, 0, 0, 0, time.UTC)
	secondTime := firstTime.Add(12 * time.Hour)
	firstRun, secondRun := run, run
	firstRun.PublishedAt, secondRun.PublishedAt = &firstTime, &secondTime
	build := func(published store.ExtractionRun) *CoverageCertificate {
		t.Helper()
		certificate, err := BuildCoverageCertificate(context.Background(), &certificateRunSource{
			runs: map[string]store.ExtractionRun{key: published},
		}, []store.Repo{{Name: "alpha", IndexedCommitHash: commitA}}, []string{"proto-contract"})
		if err != nil {
			t.Fatal(err)
		}
		return certificate
	}
	first, second := build(firstRun), build(secondRun)
	if first.Digest != second.Digest || !reflect.DeepEqual(first, second) {
		t.Fatalf("publication time changed certificate: %s != %s", first.Digest, second.Digest)
	}
	data, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "published_at") {
		t.Fatalf("certificate contains publication time: %s", data)
	}
}

func TestCoverageCertificateBindsExactRunAndSourceScope(t *testing.T) {
	key := certKey("alpha", "proto-contract")
	run := certRun("alpha", "proto-contract", commitA, store.CoverageManifest{
		CorpusFileCount: 8, CandidateFileCount: 3, ReadFileCount: 4, ReadBytes: 512,
		SourceScopeDigest: "sha256:scope-a", AssertionCount: 2, AtomCount: 2,
	})
	build := func(value store.ExtractionRun) string {
		t.Helper()
		certificate, err := BuildCoverageCertificate(context.Background(), &certificateRunSource{
			runs: map[string]store.ExtractionRun{key: value},
		}, []store.Repo{{Name: "alpha", IndexedCommitHash: commitA}}, []string{"proto-contract"})
		if err != nil {
			t.Fatal(err)
		}
		return certificate.Digest
	}
	baseline := build(run)
	rows := []struct {
		name   string
		mutate func(*store.ExtractionRun)
	}{
		{name: "run id", mutate: func(r *store.ExtractionRun) { r.ID += "-replacement" }},
		{name: "scope digest", mutate: func(r *store.ExtractionRun) { r.Coverage.SourceScopeDigest = "sha256:scope-b" }},
		{name: "corpus files", mutate: func(r *store.ExtractionRun) { r.Coverage.CorpusFileCount++ }},
		{name: "candidate files", mutate: func(r *store.ExtractionRun) { r.Coverage.CandidateFileCount++ }},
		{name: "read files", mutate: func(r *store.ExtractionRun) { r.Coverage.ReadFileCount++ }},
		{name: "read bytes", mutate: func(r *store.ExtractionRun) { r.Coverage.ReadBytes++ }},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			changed := run
			row.mutate(&changed)
			if got := build(changed); got == baseline {
				t.Fatalf("%s did not change certificate digest", row.name)
			}
		})
	}
}

func TestCoverageCertificateBindsGitlinkBoundaryState(t *testing.T) {
	key := certKey("alpha", "proto-contract")
	run := certRun("alpha", "proto-contract", commitA, store.CoverageManifest{
		SourceScopeDigest:      "sha256:scope-a",
		InventoryPolicy:        corpusInventoryPolicy,
		GitlinkCount:           3,
		GitlinkDigest:          "sha256:" + strings.Repeat("a", 64),
		GitlinkSamplePaths:     []string{"idl", "vendor/idl"},
		GitlinkSampleTruncated: true,
	})
	build := func(value store.ExtractionRun) *CoverageCertificate {
		t.Helper()
		certificate, err := BuildCoverageCertificate(context.Background(), &certificateRunSource{
			runs: map[string]store.ExtractionRun{key: value},
		}, []store.Repo{{Name: "alpha", IndexedCommitHash: commitA}}, []string{"proto-contract"})
		if err != nil {
			t.Fatal(err)
		}
		return certificate
	}

	baseline := build(run)
	got := baseline.Repositories[0].Runs[0]
	if got.InventoryPolicy != corpusInventoryPolicy || got.GitlinkCount != 3 ||
		got.GitlinkDigest != run.Coverage.GitlinkDigest ||
		!reflect.DeepEqual(got.GitlinkSamplePaths, run.Coverage.GitlinkSamplePaths) ||
		!got.GitlinkSampleTruncated {
		t.Fatalf("certificate boundary state = %+v", got)
	}
	rows := []struct {
		name   string
		mutate func(*store.ExtractionRun)
	}{
		{name: "policy", mutate: func(r *store.ExtractionRun) { r.Coverage.InventoryPolicy = "gitlink-boundary-v3" }},
		{name: "count", mutate: func(r *store.ExtractionRun) { r.Coverage.GitlinkCount++ }},
		{name: "digest", mutate: func(r *store.ExtractionRun) {
			r.Coverage.GitlinkDigest = "sha256:" + strings.Repeat("b", 64)
		}},
		{name: "sample paths", mutate: func(r *store.ExtractionRun) {
			r.Coverage.GitlinkSamplePaths = []string{"idl", "vendor/other"}
		}},
		{name: "sample truncation", mutate: func(r *store.ExtractionRun) {
			r.Coverage.GitlinkSampleTruncated = false
		}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			changed := run
			changed.Coverage.GitlinkSamplePaths = append([]string(nil), run.Coverage.GitlinkSamplePaths...)
			row.mutate(&changed)
			if gotDigest := build(changed).Digest; gotDigest == baseline.Digest {
				t.Fatalf("%s did not change certificate digest", row.name)
			}
		})
	}

	legacy := run
	legacy.Coverage.InventoryPolicy = ""
	legacy.Coverage.GitlinkCount = 0
	legacy.Coverage.GitlinkDigest = ""
	legacy.Coverage.GitlinkSamplePaths = nil
	legacy.Coverage.GitlinkSampleTruncated = false
	data, err := json.Marshal(build(legacy))
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{
		"inventory_policy", "gitlink_count", "gitlink_digest",
		"gitlink_sample_paths", "gitlink_sample_truncated",
	} {
		if strings.Contains(string(data), field) {
			t.Fatalf("legacy certificate contains %q: %s", field, data)
		}
	}
}

// AC (T13.3): the certificate provably changes when one repository's
// extraction fails. This includes a forced same-commit replacement: the old
// publication remains fresh, but the durable latest-attempt marker moves from
// the published run to the aborted replacement.
func TestCoverageCertificateChangesWhenExtractionFails(t *testing.T) {
	visible := []store.Repo{{Name: "alpha", IndexedCommitHash: commitB}}
	domains := []string{"scip-proto-field"}
	key := certKey("alpha", "scip-proto-field")
	published := certRun("alpha", "scip-proto-field", commitB, store.CoverageManifest{
		Protocols: []string{"scip"}, AssertionCount: 3, AtomCount: 3,
		CorpusFileCount: 4, CandidateFileCount: 2, ReadFileCount: 2, ReadBytes: 99,
		SourceScopeDigest: "sha256:scope",
	})
	healthy := &certificateRunSource{
		runs:     map[string]store.ExtractionRun{key: published},
		attempts: map[string]store.ExtractionAttempt{key: certAttempt(published, "published")},
	}
	baseline, err := BuildCoverageCertificate(context.Background(), healthy, visible, domains)
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}
	failedAttempt := certAttempt(published, "aborted")
	failedAttempt.RunID = "run-alpha-scip-proto-field-replacement"
	failedAttempt.Extractor = "scip-proto-field@2.0.0"
	failed, err := BuildCoverageCertificate(context.Background(), &certificateRunSource{
		runs:     map[string]store.ExtractionRun{key: published},
		attempts: map[string]store.ExtractionAttempt{key: failedAttempt},
	}, visible, domains)
	if err != nil {
		t.Fatalf("failed replacement: %v", err)
	}
	if failed.Digest == baseline.Digest {
		t.Fatal("same-commit failed replacement did not change the certificate digest")
	}
	run := failed.Repositories[0].Runs[0]
	if !run.Fresh || run.RunID != published.ID || run.LatestAttempt == nil ||
		run.LatestAttempt.Status != "aborted" || run.LatestAttempt.RunID != failedAttempt.RunID ||
		run.LatestAttempt.Failure == "" {
		t.Fatalf("failed replacement certificate = %+v", run)
	}
}

func TestCoverageCertificateSCIPAvailabilityRequiresCurrentRevision(t *testing.T) {
	source := &certificateRunSource{runs: map[string]store.ExtractionRun{
		certKey("alpha", "scip-proto-field"): certRun("alpha", "scip-proto-field", commitA,
			store.CoverageManifest{Protocols: []string{"scip"}}),
	}}
	certificate, err := BuildCoverageCertificate(context.Background(), source,
		[]store.Repo{{Name: "alpha", IndexedCommitHash: commitB}}, []string{"scip-proto-field"})
	if err != nil {
		t.Fatal(err)
	}
	if got := certificate.Repositories[0].SCIPIndex; got != "unknown" {
		t.Fatalf("SCIP availability from stale run = %q, want unknown", got)
	}
}

// AC (T13.3, adversarial): an invisible repository must not leak through
// names or counts. Whatever happens to hidden state, the visible caller's
// certificate stays byte-identical, never names the hidden repository, and
// the builder never even queries it.
func TestCoverageCertificateNoInvisibleRepoLeakage(t *testing.T) {
	visible := []store.Repo{{Name: "alpha", IndexedCommitHash: commitA}}
	domains := []string{"scip-proto-field", "proto-contract"}
	alphaRun := certRun("alpha", "scip-proto-field", commitA, store.CoverageManifest{Protocols: []string{"scip"}})

	rows := []struct {
		name     string
		runs     map[string]store.ExtractionRun
		attempts map[string]store.ExtractionAttempt
	}{
		{name: "hidden repo absent", runs: map[string]store.ExtractionRun{
			certKey("alpha", "scip-proto-field"): alphaRun,
		}},
		{name: "hidden repo published", runs: map[string]store.ExtractionRun{
			certKey("alpha", "scip-proto-field"): alphaRun,
			certKey("omega-secret", "scip-proto-field"): certRun("omega-secret", "scip-proto-field", commitB, store.CoverageManifest{
				Protocols: []string{"scip"}, AssertionCount: 999_999, AtomCount: 999_999,
			}),
		}},
		{name: "hidden repo failing with distinctive failure", runs: map[string]store.ExtractionRun{
			certKey("alpha", "scip-proto-field"): alphaRun,
		}, attempts: map[string]store.ExtractionAttempt{
			certKey("omega-secret", "proto-contract"): {
				RunID: "omega-failure", Repo: "omega-secret", Commit: commitB,
				Domain: "proto-contract", Extractor: "secret@1", Status: "aborted",
			},
		}},
	}
	var serialized []string
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			source := &certificateRunSource{runs: row.runs, attempts: row.attempts}
			certificate, err := BuildCoverageCertificate(context.Background(), source, visible, domains)
			if err != nil {
				t.Fatalf("BuildCoverageCertificate: %v", err)
			}
			data, err := json.Marshal(certificate)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if strings.Contains(string(data), "omega") {
				t.Fatalf("certificate names an invisible repository: %s", data)
			}
			if certificate.RepositoryCount != 1 {
				t.Fatalf("repository count = %d, want the visible universe only", certificate.RepositoryCount)
			}
			for _, key := range source.queried {
				if !strings.Contains(key, "\x00alpha\x00") {
					t.Fatalf("builder queried an invisible repository: %q", key)
				}
			}
			serialized = append(serialized, string(data))
		})
	}
	for i := 1; i < len(serialized); i++ {
		if serialized[i] != serialized[0] {
			t.Fatalf("hidden state changed the visible certificate:\n%s\n%s", serialized[0], serialized[i])
		}
	}
}

func TestCoverageCertificateRejectsInconsistentInput(t *testing.T) {
	run := certRun("alpha", "d", commitA, store.CoverageManifest{})
	manyDomains := make([]string, 65)
	for i := range manyDomains {
		manyDomains[i] = "d" + strings.Repeat("x", i)
	}
	rows := []struct {
		name     string
		visible  []store.Repo
		domains  []string
		runs     map[string]store.ExtractionRun
		attempts map[string]store.ExtractionAttempt
	}{
		{name: "no domains", visible: []store.Repo{{Name: "alpha"}}, domains: nil},
		{name: "empty domain", visible: []store.Repo{{Name: "alpha"}}, domains: []string{""}},
		{name: "invalid domain", visible: []store.Repo{{Name: "alpha"}}, domains: []string{"bad/domain"}},
		{name: "too many domains", visible: []store.Repo{{Name: "alpha"}}, domains: manyDomains},
		{name: "duplicate domain", visible: []store.Repo{{Name: "alpha"}}, domains: []string{"d", "d"}},
		{name: "empty repo name", visible: []store.Repo{{Name: ""}}, domains: []string{"d"}},
		{name: "duplicate repo", visible: []store.Repo{{Name: "alpha"}, {Name: "alpha"}}, domains: []string{"d"}},
		{name: "deleting repo", visible: []store.Repo{{Name: "alpha", Deleting: true}}, domains: []string{"d"}},
		{
			name: "run repo mismatch", visible: []store.Repo{{Name: "alpha", IndexedCommitHash: commitA}}, domains: []string{"d"},
			runs: map[string]store.ExtractionRun{certKey("alpha", "d"): func() store.ExtractionRun {
				bad := run
				bad.Repo = "other"
				return bad
			}()},
		},
		{
			name: "unpublished run status", visible: []store.Repo{{Name: "alpha", IndexedCommitHash: commitA}}, domains: []string{"d"},
			runs: map[string]store.ExtractionRun{certKey("alpha", "d"): func() store.ExtractionRun {
				bad := run
				bad.Status = "staged"
				return bad
			}()},
		},
		{
			name: "attempt repo mismatch", visible: []store.Repo{{Name: "alpha", IndexedCommitHash: commitA}}, domains: []string{"d"},
			attempts: map[string]store.ExtractionAttempt{certKey("alpha", "d"): {
				RunID: "attempt", Repo: "other", Commit: commitA, Domain: "d",
				Extractor: "d@2", Status: "aborted",
			}},
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			source := &certificateRunSource{runs: row.runs, attempts: row.attempts}
			if _, err := BuildCoverageCertificate(context.Background(), source, row.visible, row.domains); err == nil {
				t.Fatal("inconsistent input built a certificate")
			}
		})
	}
}
