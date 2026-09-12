package t421

import (
	"context"
	"crypto/sha256"
	"io"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

// Explicitly modeled completed transport rows; no native auth, index or
// completed-run authority is claimed by these conversion/alias tests.
func TestEpochQueryReceiptTransportUnits(t *testing.T) {
	for _, row := range epochProductTestRows(t) {
		value, err := completedProductTransport(row, "modeled-transport", testDigest("actual-modeled-authority"))
		if err != nil {
			t.Fatal(err)
		}
		denied := row.Code == "404" || row.Code == "unknown_repository"
		wantDecision, wantRepositories := "t421-authorized-visible-v1", uint64(1)
		if denied {
			wantDecision, wantRepositories = "t421-authorized-hidden-v1", 0
		}
		if value.AuthorizationDecision != wantDecision || value.AuthorizedRepositories != wantRepositories ||
			value.AuthorizationDecisions != 1 || value.AuthoritySnapshots != 1 ||
			value.ControlReads != row.ControlFileReads+row.StoreReadAttempts || value.MemberReads != row.MemberVisits ||
			value.Pages != row.Pages || value.ProjectionSHA256 != row.ProjectionSHA256 {
			t.Fatal("actual modeled transport units changed", value)
		}
	}
	for _, mode := range []string{"overflow", "unknown_code", "missing_observation", "two_repositories", "foreign_observation"} {
		row := epochProductTestRows(t)[0]
		switch mode {
		case "overflow":
			row.ControlFileReads, row.StoreReadAttempts = ^uint64(0), 1
		case "unknown_code":
			row.Code = "500"
		case "missing_observation":
			row.VisibleRepositoriesObserved = false
		case "two_repositories":
			row.VisibleRepositories = 2
		case "foreign_observation":
			row.Name = "first_service"
		}
		if _, err := completedProductTransport(row, "modeled", testDigest("actual-modeled-authority")); err == nil {
			t.Fatal("accepted refusal case", mode)
		}
	}
}

func TestEpochQueryReceiptRequiresOwnedCompletion(t *testing.T) {
	for _, run := range []*ExecutionEpochOneRun{nil, {}, {flow: &ExecutionEpochOne{}}} {
		if run.recordProductQueryEvidence(t.Context()) == nil {
			t.Fatal("unbound run minted completed evidence")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if (&ExecutionEpochOneRun{}).recordProductQueryEvidence(ctx) == nil {
		t.Fatal("canceled operation minted evidence")
	}
}

func TestEpochQueryReceiptDetachedStoppedPrefix(t *testing.T) {
	done := make(chan struct{})
	close(done)
	evidence := &QueryEvidence{Phase: "product_queries", Outcome: "passed", Results: []QueryResult{{Name: "modeled"}}}
	run := &ExecutionEpochOneRun{done: done, flow: &ExecutionEpochOne{}, err: ErrExecutionEpochOne,
		result: ExecutionEpochOneResult{QueryResults: cloneProductQueryEvidence(evidence), ProductQueries: epochProductTestRows(t)}}
	evidence.Results[0].Name = "caller-mutated"
	first, err := run.Wait(t.Context())
	if err == nil || first.QueryResults == nil || first.QueryResults.Results[0].Name != "modeled" {
		t.Fatal("later stopped result erased or aliased earlier modeled evidence")
	}
	first.QueryResults.Results[0].Name = "returned-mutated"
	first.ProductQueries[0].Name = "returned-mutated"
	second, _ := run.Wait(t.Context())
	if second.QueryResults.Results[0].Name != "modeled" || second.ProductQueries[0].Name == "returned-mutated" {
		t.Fatal("repeated Wait exposed owned evidence")
	}
}

// The PC object and socket exchanges are real; its peer only echoes three
// modeled ACKs. The owner state, F bodies, byte samples and query rows below
// are explicitly supplied. This is no inherited/native phase acceptance proof.
func modeledQueryReceiptControl(t *testing.T, open bool) *dispatchadmission.PhaseControl {
	t.Helper()
	parent, child, err := dispatchadmission.NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	control, err := dispatchadmission.NewPhaseControl(t.Context(), parent, [32]byte{1},
		dispatchadmission.PhaseControlConfig{OwnerControl: true, Phases: []uint32{14}, InitialPhase: 14,
			MaximumPhases: 1, MaximumWireBytes: 8 * dispatchadmission.FrameBytes, Timeout: time.Second})
	if err != nil {
		_ = parent.Close()
		_ = child.Close()
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 3 {
			var raw [dispatchadmission.FrameBytes]byte
			if _, err := io.ReadFull(child, raw[:]); err != nil {
				return
			}
			if n, err := child.Write(raw[:]); err != nil || n != len(raw) {
				return
			}
		}
	}()
	t.Cleanup(func() { _ = control.Close(); _ = child.Close(); <-done })
	if control.DrainOwners(t.Context()) != nil || control.OpenRequests(t.Context()) != nil {
		t.Fatal("modeled PC preparation refused")
	}
	if !open && control.FenceRequests(t.Context()) != nil {
		t.Fatal("modeled PC fence refused")
	}
	return control
}

func modeledQueryReceiptRun(t *testing.T, plan Plan, open bool) *ExecutionEpochOneRun {
	t.Helper()
	correction := *plan.Correction
	plan.Correction = &correction
	authority := AuthorityPhaseResult{Phase: "product_queries", Outcome: "passed", AuthorityState: AuthorityState{Current: true}}
	queryAuthority := epochQueryAuthority{CatalogSourceGenerationSHA256: testDigest("modeled-catalog-source"),
		ResolverNamespaceGenerationSHA256: testDigest("modeled-namespace-generation"),
		ResolverNamespaceRootSHA256:       testDigest("modeled-namespace-root")}
	// These are hashes of two equal supplied complete F wire values, not a
	// measurement or a substitution for the genuine F decoder's full checks.
	value := epochFinalResponse{Schema: "t421-final-authority-source-free-v1", QueryAuthority: &queryAuthority}
	digest := sha256.Sum256(epochTestJSON(t, value, true))
	run := &ExecutionEpochOneRun{flow: &ExecutionEpochOne{plan: plan}, control: modeledQueryReceiptControl(t, open),
		epoch: ExecutionEpochConfig{Epoch: 5}, healthy: true, warm: true, productExecutionUsed: true,
		stop: make(chan struct{}), done: make(chan struct{}), restoredExecutionDone: make(chan struct{})}
	reader := &executionEpochInspection{run: run, plan: plan, projection: PhaseStateProjection{Phase: "product_queries"},
		finalUsed: true, productQueriesComplete: true, productFinalCalls: 2,
		productQueries: epochProductTestRows(t), productFirstFinalOrdinal: 9, next: 49,
		productAuthority: authority, productQueryAuthority: queryAuthority, productBaseline: &digest,
		productFinalDigests: [2][sha256.Size]byte{digest, digest},
		restoredSamples:     ExecutionRestoredSamples{ArchiveComplete: true, CollectionComplete: true, ProductComplete: true}}
	run.inspection = reader
	for i, phase := range []string{"archive_restore", "lifecycle_collection", "product_queries"} {
		reader.evidence.rows = append(reader.evidence.rows, ExecutionPhaseInspection{ServerEpoch: 5, Phase: phase,
			SelectorAccepted: true, Final: &ExecutionInspectionFinal{Ordinal: uint64(i + 1)}})
		count := min(uint64(i+1), 2)
		reader.restoredSamples.Phases[i] = ExecutionWorkspaceBytePhase{Attempts: count, Completed: count}
	}
	last := &reader.evidence.rows[2]
	last.Final.Ordinal, last.Final.Authority, last.NextOrdinal = 48, authority.AuthorityState, 49
	for _, row := range reader.productQueries {
		last.Reads.ControlFileReads += row.ControlFileReads
		last.Reads.StoreReadAttempts += row.StoreReadAttempts
		last.Reads.MemberVisits += row.MemberVisits
	}
	return run
}

func TestEpochQueryReceiptModeledOwnedBoundary(t *testing.T) {
	plan := accountingTestPlan(t)
	for _, mode := range []string{"success", "missing_raw_f", "unequal_raw_f", "changed_baseline", "one_f",
		"wrong_phase", "wrong_epoch", "unfinished", "missing_queries", "missing_sample", "sample_unavailable",
		"open_fence", "unaccepted_selector", "ordinal_gap", "canceled", "repeat", "wrong_policy", "missing_policy", "missing_namespace_generation", "missing_namespace_root", "legacy"} {
		t.Run(mode, func(t *testing.T) {
			run := modeledQueryReceiptRun(t, plan, mode == "open_fence")
			reader := run.inspection
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch mode {
			case "missing_raw_f":
				reader.productFinalDigests[1] = [sha256.Size]byte{}
			case "unequal_raw_f":
				reader.productFinalDigests[1][0] ^= 1
			case "changed_baseline":
				changed := sha256.Sum256([]byte("different F"))
				reader.productBaseline = &changed
			case "one_f":
				reader.productFinalCalls = 1
			case "wrong_phase":
				reader.projection.Phase = "lifecycle_collection"
			case "wrong_epoch":
				run.epoch.Epoch = 4
			case "unfinished":
				reader.restoredSamples.ProductComplete = false
			case "missing_queries":
				reader.productQueries = reader.productQueries[:21]
			case "missing_sample":
				reader.restoredSamples.Phases[2].Completed = 1
			case "sample_unavailable":
				reader.restoredSamples.Unavailable = true
			case "unaccepted_selector":
				reader.evidence.rows[2].SelectorAccepted = false
			case "ordinal_gap":
				reader.productQueries[0].FirstOrdinal++
			case "canceled":
				cancel()
			case "wrong_policy":
				reader.plan.Correction.ReadAccountingPolicy = ""
			case "missing_policy":
				reader.plan.Correction = nil
			case "missing_namespace_generation":
				reader.productQueryAuthority.ResolverNamespaceGenerationSHA256 = ""
			case "missing_namespace_root":
				reader.productQueryAuthority.ResolverNamespaceRootSHA256 = ""
			case "legacy":
				reader.plan.Schema = PlanV2Schema
			}
			err := run.recordProductQueryEvidence(ctx)
			valid := mode == "success" || mode == "repeat"
			if (err == nil) != valid || (reader.productQueryEvidence != nil) != valid {
				t.Fatal("modeled completion disposition", mode, err)
			}
			if !valid {
				return
			}
			evidence := reader.productQueryEvidence
			if mode == "repeat" && run.recordProductQueryEvidence(ctx) == nil {
				t.Fatal("repeat minted another accepted inventory")
			}
			hash, err := authorityResultSHA256([]AuthorityPhaseResult{reader.productAuthority}, "product_queries")
			if err != nil {
				t.Fatal(err)
			}
			counts := reader.evidence.rows[2].Reads
			// Check K=C+S against the unchanged receipt validator, using the
			// supplied actual-prefix model rather than work-envelope maxima.
			metrics := ReceiptMetrics{ControlReads: CountMetric(counts.ControlFileReads + counts.StoreReadAttempts), MemberReads: CountMetric(counts.MemberVisits)}
			if err := validateQueryEvidence(*evidence, "passed", hash, metrics, reader.plan); err != nil {
				t.Fatal(err)
			}
			first := reader.productQueries[0]
			if evidence.Results[0].HTTP.ControlReads != first.ControlFileReads+first.StoreReadAttempts ||
				evidence.Results[0].HTTP.MemberReads != first.MemberVisits || evidence.Results[0].HTTP.AuthorityBeforeSHA256 != hash {
				t.Fatal("captured evidence replaced measured model values")
			}
			reader.productQueries[0].Code = "caller-mutated"
			copy := cloneProductQueryEvidence(evidence)
			copy.Results[0].Name = "caller-mutated"
			if evidence.Results[0].HTTP.Code != first.Code || evidence.Results[0].Name == "caller-mutated" {
				t.Fatal("capture or detached copy exposed mutable aliases")
			}
		})
	}
}
