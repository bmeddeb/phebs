package extract

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/analysisunit"
	"github.com/bmeddeb/phebs/internal/store"
)

type certificateRunSource struct {
	runs      map[string]store.ExtractionRun
	attempts  map[string]store.ExtractionAttempt
	runErrors map[string]error
	queried   []string
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
	if first.SchemaVersion != "coverage-certificate-v2" || !strings.HasPrefix(first.Digest, "sha256:") {
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
		SourceScopeDigest:        digestA,
		ScopePosture:             "focused-local",
		CandidateManifestDigest:  digestB,
		CandidatePlane:           "local",
		ScopeCorpusFileCount:     3,
		ScopeCorpusDeclaredBytes: 300,
		ScopeCorpusDigest:        digestA,
		PlannedFileCount:         2,
		PlannedRequiredFileCount: 1,
		PlannedDeclaredBytes:     200,
		PlannedScopeDigest:       digestB,
		TypedInputKind:           analysisunit.TypedIndexKindSCIP,
		TypedInputPath:           "services/payments/index.scip",
		TypedInputObjectID:       strings.Repeat("c", 40),
		TypedInputDeclaredBytes:  100,
		TypedInputDigest:         digestA,
		TypedInputPresent:        true,
		CorpusFileCount:          3,
		CandidateFileCount:       1,
		ReadFileCount:            2,
		ReadBytes:                200,
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
		covered.CandidateScope.PlannedFileCount != 2 ||
		covered.CandidateScope.TypedInput == nil ||
		!covered.CandidateScope.TypedInput.Present {
		t.Fatalf("focused run coverage = %+v", covered)
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
