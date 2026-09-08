//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/connection"
	"github.com/surrealdb/surrealdb.go/pkg/models"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
	"github.com/gofrs/flock"
)

func TestServiceStateV3PreimageHandoffOwnerProjection(t *testing.T) {
	ordinary, selected := serviceStateV3PreimageOwnerSQL(false), serviceStateV3PreimageOwnerSQL(true)
	const projection = "SELECT id, root_digest, repository, root_bytes, recorded_at FROM $root_rid"
	if strings.Count(ordinary, "SELECT * FROM $root_rid") != 1 ||
		strings.Count(selected, projection) != 1 || strings.Contains(selected, "SELECT * FROM $root_rid") ||
		strings.Replace(selected, projection, "SELECT * FROM $root_rid", 1) != ordinary {
		t.Fatal("handoff must change only the closed root metadata projection")
	}
}

func TestServiceStateV3PreimageHandoffAccounting(t *testing.T) {
	for _, mode := range []string{"empty", "wrong_selector", "malformed_summary", "orphan", "known_failure", "unknown_failure", "commit_failure", "canceled", "read_limit"} {
		t.Run(mode, func(t *testing.T) {
			base, owner, controller := storeAccountingFixture(t, 40, 2)
			limits := readaccounting.Counts{StoreReadAttempts: 9, StoreWriteAttempts: 2}
			if mode == "read_limit" {
				limits.StoreReadAttempts = 0
			}
			ctx, ledger, err := readaccounting.Start(base, limits)
			if err != nil {
				t.Fatal(err)
			}
			db, native := storeAccountingDB(t, ctx, owner)
			selected := ServiceRuntimeSelector{
				Schema: ServiceRuntimeSelectorSchema, Repository: "example.com/neutral/handoff",
				Backend: ServiceRuntimeV3, CatalogRootDigest: selectorTestDigest("1"),
				CatalogControlRevision: 1, StateControlRevision: 1,
				StateSummaryDigest: selectorTestDigest("2"), SearchGenerationDigest: selectorTestDigest("3"),
				RelationshipGenerationDigest: selectorTestDigest("4"), RelationshipRootDigest: selectorTestDigest("5"),
				ControlRevision: 1, ChangedAt: storeTimestamp(time.Now()),
			}
			selected.Digest = serviceRuntimeSelectorDigest(selected)
			queries, commits := 0, 0
			uuid := models.UUID{}
			uuid.UUID[0] = 7
			native.call = func(_ context.Context, req *connection.RPCRequest) (any, error) {
				switch req.Method {
				case "begin":
					return uuid, nil
				case "cancel":
					return nil, nil
				case "commit":
					commits++
					if mode == "commit_failure" {
						return nil, errors.New("unknown commit transport")
					}
					return nil, nil
				case "query":
					queries++
					actual, ok := req.Txn.(*models.UUID)
					if !ok || actual == nil || *actual != uuid {
						return nil, errors.New("handoff lost native transaction")
					}
					if queries == 1 {
						if req.Params[0] != "SELECT * FROM $rid LIMIT 1" {
							return nil, errors.New("selector point check missing")
						}
						prefix, err := controller.Snapshot()
						if err != nil || prefix.Transactions != 1 || prefix.Rows != 0 {
							return nil, errors.New("selector check lacked acknowledged transaction")
						}
						if mode == "known_failure" {
							return nil, &surrealdb.QueryError{Message: "neutral fence failure"}
						}
						if mode == "unknown_failure" {
							return nil, errors.New("unknown fence transport")
						}
						if mode == "wrong_selector" {
							return queueAccountingOK([]any{}), nil
						}
						content := serviceRuntimeSelectorContent(selected)
						content["id"] = serviceRuntimeSelectorID(selected.Repository)
						return queueAccountingOK([]any{content}), nil
					}
					if queries == 2 && mode == "malformed_summary" {
						return queueAccountingOK([]any{map[string]any{"repository": selected.Repository}}), nil
					}
					if queries == 3 && mode == "orphan" {
						return queueAccountingOK([]any{map[string]any{"id": models.NewRecordID("service_state_v3_preimage", "orphan")}}), nil
					}
					return queueAccountingOK([]any{}), nil
				default:
					return nil, errors.New("unexpected native call")
				}
			}
			if mode == "canceled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			result, callErr := (&Surreal{db: db, accounting: owner}).DrainServiceStateV3PreimageHandoff(ctx, selected)
			counts, ledgerErr := ledger.Finish()
			wantReads := uint64(1)
			switch mode {
			case "empty", "orphan", "commit_failure":
				wantReads = 3
			case "malformed_summary":
				wantReads = 2
			case "canceled":
				wantReads = 0
			}
			if (callErr == nil) != (mode == "empty") || result.Done != (mode == "empty") || result.Deleted != 0 ||
				counts != (readaccounting.Counts{StoreReadAttempts: wantReads}) ||
				(ledgerErr != nil) != (mode == "read_limit") {
				t.Fatalf("result=%+v err=%v ledger=%+v/%v", result, callErr, counts, ledgerErr)
			}
			if commits != 0 && mode != "empty" && mode != "commit_failure" {
				t.Fatalf("refusal committed: %d", commits)
			}
		})
	}
}

// This uses one fresh native store, real catalog/state plans and selector CAS;
// it is a bounded store regression, not a production phase or ceremony proof.
func TestServiceStateV3PreimageHandoffNative(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	directory := t.TempDir()
	ordinary, err := OpenLocalMemory(ctx, directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ordinary.Close(context.Background()) })
	fixture := newServiceRuntimeSelectorFixtureStore(ctx, t, ordinary)
	indexDir := t.TempDir()
	// focusedindex imports store, so this package cannot import its admission
	// wrapper. Use the same existing native flock primitive and path here;
	// the command integration separately proves that wrapper is actually used.
	acquire := func(callCtx context.Context, exclusive bool) (func(), error) {
		lock := flock.New(filepath.Join(indexDir, ".phebs-index-publication.lock"), flock.SetPermissions(0o600))
		var ok bool
		var err error
		if exclusive {
			ok, err = lock.TryLockContext(callCtx, 10*time.Millisecond)
		} else {
			ok, err = lock.TryRLockContext(callCtx, 10*time.Millisecond)
		}
		if err != nil || !ok {
			_ = lock.Close()
			if err == nil {
				err = errors.New("mutation custody was not acquired")
			}
			return nil, err
		}
		return func() { _ = lock.Unlock(); _ = lock.Close() }, nil
	}
	selected, err := fixture.store.SelectServiceRuntimeV3(ctx, ServiceRuntimeSelectionRequest{
		Repository: fixture.repository, Target: fixture.v3,
	})
	if err != nil {
		t.Fatal(err)
	}
	check := func(selected ServiceRuntimeSelector, want ServiceStateV3PreimageDrain, reads, writes uint64, wantErr bool) {
		t.Helper()
		release, err := acquire(ctx, true)
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		metered, ledger, err := readaccounting.Start(ctx, readaccounting.Counts{
			StoreReadAttempts:  ServiceStateV3PreimageHandoffStoreReadMaximum,
			StoreWriteAttempts: ServiceStateV3PreimageHandoffStoreWriteMaximum,
		})
		if err != nil {
			t.Fatal(err)
		}
		got, err := fixture.store.DrainServiceStateV3PreimageHandoff(metered, selected)
		counts, ledgerErr := ledger.Finish()
		if (err != nil) != wantErr || got != want || ledgerErr != nil ||
			counts != (readaccounting.Counts{StoreReadAttempts: reads, StoreWriteAttempts: writes}) {
			t.Fatalf("handoff=%+v/%v want=%+v error=%t counts=%+v/%v", got, err, want, wantErr, counts, ledgerErr)
		}
	}
	check(selected, ServiceStateV3PreimageDrain{Done: true}, 3, 0, false)
	services := make([]servicecatalog.Service, 18)
	for index := range services {
		services[index] = servicecatalog.Service{
			Key: fmt.Sprintf("service-%02d", index), DisplayName: fmt.Sprintf("Service %02d", index),
			Disposition: servicecatalog.DispositionAccepted, Origin: servicecatalog.OriginBase,
		}
	}
	// Keep the original member, so the next change does not create tombstones.
	services[0].Key, services[0].DisplayName = "orders", "Orders V3"
	target := preimageHandoffTarget(t, fixture, "handoff-b", services)
	check(selected, ServiceStateV3PreimageDrain{Protected: true}, 2, 0, true)
	prior := selected
	selected, err = fixture.store.SelectServiceRuntimeV3(ctx, ServiceRuntimeSelectionRequest{
		Repository: fixture.repository, ExpectedControlRevision: prior.ControlRevision,
		ExpectedDigest: prior.Digest, Target: target,
	})
	if err != nil {
		t.Fatal(err)
	}
	check(prior, ServiceStateV3PreimageDrain{}, 1, 0, true)
	// The unchanged original service needs only its old repository summary.
	check(selected, ServiceStateV3PreimageDrain{Deleted: 1, Done: true}, 9, 1, false)
	for index := range services {
		services[index].DisplayName += " changed"
	}
	target = preimageHandoffTarget(t, fixture, "handoff-c", services)
	prior = selected
	selected, err = fixture.store.SelectServiceRuntimeV3(ctx, ServiceRuntimeSelectionRequest{
		Repository: fixture.repository, ExpectedControlRevision: prior.ControlRevision,
		ExpectedDigest: prior.Digest, Target: target,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Stage a real delete under exclusive mutation custody. The admitted CAS
	// path must be blocked through commit, then succeed after lock release.
	// A raw CAS that bypasses custody is deliberately not promised isolation:
	// native evidence established that a no-op UPDATE is not a write fence.
	raceTarget := target
	raceTarget.RelationshipGenerationDigest = selectorTestDigest("8")
	raceTarget.RelationshipRootDigest = selectorTestDigest("9")
	if err := fixture.store.PinServiceCatalogV3RelationshipReference(ctx, ServiceCatalogV3RelationshipReference{
		Repository: fixture.repository, RelationshipGenerationDigest: raceTarget.RelationshipGenerationDigest,
		RelationshipRootDigest: raceTarget.RelationshipRootDigest, CatalogRootDigest: raceTarget.CatalogRootDigest,
		CatalogControlRevision: raceTarget.CatalogControlRevision, StateControlRevision: raceTarget.StateControlRevision,
		StateSummaryDigest: raceTarget.StateSummaryDigest,
	}); err != nil {
		t.Fatal(err)
	}
	releaseExclusive, err := acquire(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if releaseExclusive != nil {
			releaseExclusive()
		}
	}()
	tx, err := storeBegin(ctx, nil, fixture.store.db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = storeCancel(context.Background(), nil, tx) }()
	if err := fixture.store.fenceServiceStateV3PreimageHandoff(ctx, tx, selected); err != nil {
		t.Fatal(err)
	}
	snapshot, err := fixture.store.serviceStateV3HandoffInventory(ctx, tx, fixture.repository)
	if err != nil || snapshot == nil {
		t.Fatalf("race snapshot: %+v/%v", snapshot, err)
	}
	owner := serviceCatalogV3LifecycleRecord(t, fixture.store, snapshot.CatalogGeneration)
	if deleted, err := fixture.store.drainServiceStateV3PreimagesTx(ctx, tx, owner, 1, false); err != nil || deleted != 1 {
		t.Fatalf("race staged deletes=%d/%v", deleted, err)
	}
	advance := func(callCtx context.Context) (ServiceRuntimeSelector, error) {
		release, err := acquire(callCtx, false)
		if err != nil {
			return ServiceRuntimeSelector{}, err
		}
		defer release()
		return fixture.store.SelectServiceRuntimeV3(callCtx, ServiceRuntimeSelectionRequest{
			Repository: fixture.repository, ExpectedControlRevision: selected.ControlRevision,
			ExpectedDigest: selected.Digest, Target: raceTarget,
		})
	}
	blockedCtx, cancelBlocked := context.WithTimeout(ctx, 50*time.Millisecond)
	_, blockedErr := advance(blockedCtx)
	cancelBlocked()
	if !errors.Is(blockedErr, context.DeadlineExceeded) {
		t.Fatalf("exclusive custody admitted concurrent selector CAS: %v", blockedErr)
	}
	if err := storeCommit(ctx, nil, tx); err != nil {
		t.Fatalf("exclusive-custody delete commit: %v", err)
	}
	releaseExclusive()
	releaseExclusive = nil
	selected, err = advance(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Reopen the same actual engine through the selected SDK factory. Its
	// native startup definitions are measured separately from each handoff.
	runtime, err := ReadLocalRuntime(directory)
	if err != nil {
		t.Fatal(err)
	}
	selectedCtx, sdkOwner, controller := storeAccountingFixture(t, 40, 2)
	accounted, err := openExistingLocalWithOwner(selectedCtx, runtime.Endpoint, "root", "root", "phebs", "phebs", sdkOwner)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = accounted.Close(context.Background()) })
	fixture.store = accounted
	before, err := controller.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	check(selected, ServiceStateV3PreimageDrain{Deleted: 16}, 7, 1, false)
	after, err := controller.Snapshot()
	if err != nil || after.Transactions-before.Transactions != 1 || after.Rows-before.Rows != 16 || after.Producers[0].Calls != 0 {
		t.Fatalf("selected full-turn charge before=%+v after=%+v err=%v", before, after, err)
	}
	before = after
	check(selected, ServiceStateV3PreimageDrain{Deleted: 2, Done: true}, 9, 2, false)
	after, err = controller.Snapshot()
	if err != nil || after.Transactions-before.Transactions != 1 || after.Rows-before.Rows != 2 || after.Producers[0].Calls != 0 {
		t.Fatalf("selected final-turn charge before=%+v after=%+v err=%v", before, after, err)
	}
	before = after
	check(selected, ServiceStateV3PreimageDrain{Done: true}, 3, 0, false)
	after, err = controller.Snapshot()
	if err != nil || after.Transactions-before.Transactions != 1 || after.Rows-before.Rows != 0 || after.Producers[0].Calls != 0 {
		t.Fatalf("selected empty-turn charge before=%+v after=%+v err=%v", before, after, err)
	}
	if err := controller.Fence(); err != nil {
		t.Fatal(err)
	}
	if err := sdkOwner.checkpoint(selectedCtx); err != nil {
		t.Fatal(err)
	}
	// Independent readback is outside the selected handoff accounting claim.
	fixture.store = ordinary
	actual, err := fixture.store.GetServiceRuntimeSelector(ctx, fixture.repository)
	if err != nil || actual != selected {
		t.Fatalf("handoff changed selector: %+v/%v", actual, err)
	}
	if _, err := fixture.store.ValidateServiceCatalogV3Precious(ctx); err != nil {
		t.Fatalf("handoff damaged retained authority/plans: %v", err)
	}
}

func preimageHandoffTarget(t *testing.T, fixture serviceRuntimeSelectorFixture, source string, services []servicecatalog.Service) ServiceRuntimeTarget {
	t.Helper()
	ctx := t.Context()
	generation := serviceStateV3Generation(t, fixture.repository, strings.Repeat("7", 40), source, services)
	if err := fixture.store.PublishServiceCatalogV3Candidate(ctx, generation); err != nil {
		t.Fatal(err)
	}
	reconcile, err := fixture.store.BeginServiceStateV3Reconcile(ctx, fixture.repository)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, fixture.store, reconcile)
	activation, err := fixture.store.BeginServiceStateV3Activation(ctx, fixture.repository, fixture.v3.SearchGenerationDigest)
	if err != nil {
		t.Fatal(err)
	}
	runServiceStateV3Plan(t, fixture.store, activation)
	pointer, err := fixture.store.GetServiceCatalogV3CandidatePointer(ctx, fixture.repository)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := fixture.store.GetServiceStateV3SummaryPoint(ctx, fixture.repository)
	if err != nil {
		t.Fatal(err)
	}
	target := ServiceRuntimeTarget{
		CatalogRootDigest: pointer.RootDigest, CatalogControlRevision: pointer.ControlRevision,
		StateControlRevision: summary.ControlRevision, StateSummaryDigest: summary.SummaryDigest,
		SearchGenerationDigest:       fixture.v3.SearchGenerationDigest,
		RelationshipGenerationDigest: generation.Root.Digest, RelationshipRootDigest: generation.Root.Digest,
	}
	if err := fixture.store.PinServiceCatalogV3RelationshipReference(ctx, ServiceCatalogV3RelationshipReference{
		Repository: fixture.repository, RelationshipGenerationDigest: target.RelationshipGenerationDigest,
		RelationshipRootDigest: target.RelationshipRootDigest, CatalogRootDigest: target.CatalogRootDigest,
		CatalogControlRevision: target.CatalogControlRevision, StateControlRevision: target.StateControlRevision,
		StateSummaryDigest: target.StateSummaryDigest,
	}); err != nil {
		t.Fatal(err)
	}
	return target
}
