package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/connection"
	connectionhttp "github.com/surrealdb/surrealdb.go/pkg/connection/http"
	"github.com/surrealdb/surrealdb.go/pkg/models"
	"github.com/surrealdb/surrealdb.go/surrealcbor"
)

type serviceStateV3CensusConnection struct {
	*failingOpenConnection
	codec   *surrealcbor.Codec
	replies [][]surrealdb.QueryResult[any]
	calls   int
}

func (conn *serviceStateV3CensusConnection) Send(_ context.Context, method string, params ...any) (*connection.RPCResponse[cbor.RawMessage], error) {
	if method != "query" || len(params) != 2 || conn.calls >= len(conn.replies) {
		return nil, errors.New("unexpected census test call")
	}
	sql, ok := params[0].(string)
	if !ok || !strings.HasPrefix(strings.TrimSpace(sql), "SELECT ") || strings.Contains(sql, "BEGIN;") {
		return nil, errors.New("census test forwarded a write")
	}
	body, err := conn.codec.Marshal(conn.replies[conn.calls])
	conn.calls++
	if err != nil {
		return nil, err
	}
	raw := cbor.RawMessage(body)
	return &connection.RPCResponse[cbor.RawMessage]{Result: &raw}, nil
}

func TestServiceStateV3PreimageCensusEnvelope(t *testing.T) {
	plan, _, _ := serviceStateV3HeadroomFixture(t, serviceStateV3Activate, 0)
	selector := ServiceRuntimeSelector{
		Schema: ServiceRuntimeSelectorSchema, Repository: plan.Repository, Backend: ServiceRuntimeV3,
		CatalogRootDigest: plan.CatalogRoot, CatalogControlRevision: plan.CatalogControlRevision,
		StateControlRevision: plan.SummaryControlRevision, StateSummaryDigest: plan.SummaryDigest,
		SearchGenerationDigest: plan.SearchGeneration, RelationshipGenerationDigest: selectorTestDigest("c"),
		RelationshipRootDigest: selectorTestDigest("d"), ControlRevision: 1, ChangedAt: plan.CreatedAt,
	}
	selector.Digest = serviceRuntimeSelectorDigest(selector)
	selectorRow := serviceRuntimeSelectorContent(selector)
	selectorRow["id"] = serviceRuntimeSelectorID(plan.Repository)
	for _, test := range []struct {
		name   string
		mutate func([]surrealdb.QueryResult[any]) []surrealdb.QueryResult[any]
	}{
		{"real empty arrays", nil},
		{"null current", func(rows []surrealdb.QueryResult[any]) []surrealdb.QueryResult[any] {
			rows[0].Result = nil
			return rows
		}},
		{"null preimages", func(rows []surrealdb.QueryResult[any]) []surrealdb.QueryResult[any] {
			rows[1].Result = nil
			return rows
		}},
		{"null summaries", func(rows []surrealdb.QueryResult[any]) []surrealdb.QueryResult[any] {
			rows[3].Result = nil
			return rows
		}},
		{"unknown status", func(rows []surrealdb.QueryResult[any]) []surrealdb.QueryResult[any] {
			rows[1].Status = "unknown"
			return rows
		}},
		{"missing statement", func(rows []surrealdb.QueryResult[any]) []surrealdb.QueryResult[any] { return rows[:3] }},
		{"extra statement", func(rows []surrealdb.QueryResult[any]) []surrealdb.QueryResult[any] { return append(rows, rows[0]) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			rows := make([]surrealdb.QueryResult[any], 4)
			for index := range rows {
				rows[index] = surrealdb.QueryResult[any]{Status: "OK", Result: []serviceStateV3TargetRec{}}
			}
			rows[2].Result = []serviceStateV3TargetRec{{ControlRevision: plan.SummaryControlRevision, SummaryDigest: plan.SummaryDigest}}
			if test.mutate != nil {
				rows = test.mutate(rows)
			}
			codec := surrealcbor.New()
			conn := &serviceStateV3CensusConnection{
				failingOpenConnection: &failingOpenConnection{Connection: connectionhttp.New(&connection.Config{Unmarshaler: codec})},
				codec:                 codec, replies: [][]surrealdb.QueryResult[any]{
					{{Status: "OK", Result: []map[string]any{selectorRow}}}, rows,
				},
			}
			db, err := surrealdb.FromConnection(t.Context(), conn)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close(t.Context()) })
			targets, err := (&Surreal{db: db}).serviceStateV3WriteTargetCensus(t.Context(), plan, nil, true)
			if conn.calls != 2 || test.mutate != nil && !errors.Is(err, ErrInvalidServiceStateV3) ||
				test.mutate == nil && (err != nil || !targets.summaryPreimage) {
				t.Fatalf("census=%+v error=%v calls=%d", targets, err, conn.calls)
			}
		})
	}
}

func TestServiceStateV3PreimageTargetPacking(t *testing.T) {
	for _, test := range []struct {
		name       string
		phase      string
		changed    int
		preserved  int
		summary    bool
		wantWrites []int
		wantRows   []int
	}{
		{"selected reconcile", serviceStateV3Reconcile, 512, 512, true, []int{255, 255, 2}, []int{512, 511, 5}},
		{"selected activation", serviceStateV3Activate, 512, 512, true, []int{254, 255, 3}, []int{511, 512, 8}},
		{"existing summary", serviceStateV3Activate, 512, 512, false, []int{255, 255, 2}, []int{512, 512, 6}},
		{"one preserved and new rows", serviceStateV3Reconcile, 512, 1, true, []int{509, 3}, []int{512, 4}},
		{"cold reconcile", serviceStateV3Reconcile, 512, 0, false, []int{511, 1}, []int{512, 2}},
		{"cold activation", serviceStateV3Activate, 512, 0, false, []int{510, 2}, []int{512, 4}},
		{"logical member9", serviceStateV3Activate, 1, 0, false, []int{1}, []int{3}},
		{"no-op", serviceStateV3Activate, 0, 0, true, []int{0}, []int{1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan, changes, summary := serviceStateV3HeadroomFixture(t, test.phase, test.changed)
			flags := make([]bool, len(changes))
			for index := range test.preserved {
				flags[index] = true
			}
			writes, count, err := serviceStateV3ChunkWrites(t.Context(), plan, changes, 512, "last", summary, flags, test.summary)
			if err != nil || count != len(test.wantWrites) {
				t.Fatalf("planned writes=%d error=%v", count, err)
			}
			written := 0
			for index, write := range writes[:count] {
				written += len(write.updates)
				if len(write.updates) != test.wantWrites[index] || write.payloadRecords != test.wantRows[index] ||
					write.payloadRecords > 512 || write.plan.RowsWritten != int64(written) ||
					write.plan.Digest != plan.Digest || write.plan.ScheduleDigest != plan.ScheduleDigest {
					t.Fatalf("prefix%d = %+v", index, write)
				}
				wantNext, wantRead := plan.NextChunk, int64(0)
				if index == count-1 {
					wantNext++
					wantRead = 512
				}
				if write.plan.NextChunk != wantNext || write.plan.RowsRead != wantRead ||
					index != 0 && write.summaryPreimage {
					t.Fatal("intermediate prefix advanced the member or recreated its summary preimage")
				}
			}
		})
	}
}

// One engine exercises three ordinary sources of selected preimages, including
// a durable prefix followed by a genuinely new lease. The only private seam is
// selecting one already-planned prefix so its persisted boundary is observable.
func TestServiceStateV3PreimageTargetNative(t *testing.T) {
	deadline := time.Now().Add(2 * time.Minute)
	if outer, ok := t.Deadline(); ok && outer.Add(-time.Minute).Before(deadline) {
		deadline = outer.Add(-time.Minute)
	}
	ctx, cancel := context.WithDeadline(t.Context(), deadline)
	t.Cleanup(cancel)
	if err := ctx.Err(); err != nil {
		t.Fatal(err)
	}
	s, err := OpenLocalMemory(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		if err := s.Close(cleanup); err != nil {
			t.Error(err)
		}
	})
	for _, phase := range []string{"reconcile", "activate", "remove"} {
		t.Run(phase, func(t *testing.T) {
			repository, commit := "example.com/acme/preimage-"+phase, strings.Repeat("7", 40)
			seedServiceCatalogV3RepoContext(ctx, t, s, repository, commit)
			services := serviceStateV3HeadroomServices(512)
			generation := serviceStateV3Generation(t, repository, commit, "selected-a", services)
			if err := s.PublishServiceCatalogV3Candidate(ctx, generation); err != nil {
				t.Fatal(err)
			}
			begin, err := s.BeginServiceStateV3Reconcile(ctx, repository)
			if err != nil {
				t.Fatal(err)
			}
			runServiceStateV3PlanContext(ctx, t, s, begin)
			search := selectorTestDigest("8")
			activation, err := s.BeginServiceStateV3Activation(ctx, repository, search)
			if err != nil {
				t.Fatal(err)
			}
			runServiceStateV3PlanContext(ctx, t, s, activation)
			selected := serviceStateV3PreimageSelect(ctx, t, s, repository, search)
			old, err := s.GetServiceStateV3PointSnapshot(ctx, repository, services[0].Key,
				selected.StateControlRevision, selected.StateSummaryDigest)
			if err != nil {
				t.Fatal(err)
			}
			if phase == "activate" {
				begin, err = s.BeginServiceStateV3Activation(ctx, repository, selectorTestDigest("b"))
			} else {
				for index := range services {
					services[index].DisplayName += " B"
				}
				if phase == "remove" {
					services = serviceStateV3HeadroomServices(1)
					services[0].Key = "zz-new"
				}
				generation = serviceStateV3Generation(t, repository, commit, "selected-b", services)
				if err := s.PublishServiceCatalogV3Candidate(ctx, generation); err != nil {
					t.Fatal(err)
				}
				begin, err = s.BeginServiceStateV3Reconcile(ctx, repository)
			}
			if err != nil {
				t.Fatal(err)
			}
			expandServiceStateV3PlanContext(ctx, t, s, begin)
			offset := int64(0)
			if phase == "remove" {
				member := serviceStateV3HeadroomClaimContext(ctx, t, s, *begin.Plan, 0)
				if _, err := s.ProcessServiceStateV3Chunk(ctx, member); err != nil {
					t.Fatal(err)
				}
				if err := s.CompleteGenerationChunk(ctx, member); err != nil {
					t.Fatal(err)
				}
				offset = 1
			}
			plan, err := s.getServiceStateV3Plan(ctx, begin.Plan.Digest)
			if err != nil {
				t.Fatal(err)
			}
			chunk := serviceStateV3HeadroomClaimContext(ctx, t, s, *plan, offset)
			changes, read, cursor, summary := serviceStateV3HeadroomChangesContext(ctx, t, s, *plan)
			updates := make([]serviceStateUpdate, 0, len(changes))
			for _, change := range changes {
				updates = append(updates, change.update)
			}
			targets, err := s.serviceStateV3WriteTargetCensus(ctx, *plan, updates, summary != nil)
			if err != nil || len(targets.preimages) != 512 || slices.Contains(targets.preimages, false) || !targets.summaryPreimage {
				t.Fatalf("actual selected target census = %+v, %v", targets, err)
			}
			writes, count, err := serviceStateV3ChunkWrites(ctx, *plan, changes, read, cursor, summary,
				targets.preimages, targets.summaryPreimage)
			if err != nil || count != 3 {
				t.Fatalf("dense target prefixes=%d error=%v", count, err)
			}
			assertCounts := func(wantRows, wantSummaries int) {
				t.Helper()
				rows, err := surrealdb.Query[[]models.RecordID](ctx, s.db, `
SELECT VALUE id FROM service_state_v3_preimage WHERE repository = $repository;
SELECT VALUE id FROM service_state_v3_repository_preimage WHERE repository = $repository;`, map[string]any{"repository": repository})
				if err != nil || rows == nil || len(*rows) != 2 || len((*rows)[0].Result) != wantRows || len((*rows)[1].Result) != wantSummaries {
					t.Fatalf("actual preimage inventory=%+v error=%v; want%d/%d", rows, err, wantRows, wantSummaries)
				}
			}
			assertUnchanged := func() {
				t.Helper()
				after, err := s.getServiceStateV3Plan(ctx, plan.Digest)
				if err != nil || !reflect.DeepEqual(plan, after) {
					t.Fatalf("refusal changed plan: %+v, %v", after, err)
				}
				assertCounts(0, 0)
			}
			// The previous current+plan(+summary)-only packing really exceeds512.
			oversized, err := nextServiceStateV3ChunkWrite(*plan, changes, read, cursor, summary)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.commitServiceStateV3Chunk(ctx, chunk, *plan, oversized.plan, oversized.updates,
				summary, oversized.summary, false); !errors.Is(err, ErrInvalidServiceStateV3) {
				t.Fatalf("unaccounted native preimages reached write: %v", err)
			}
			assertUnchanged()
			first := writes[0]
			// Change only the visibility metadata after the census. Revision and
			// digest still match, so the target predicate itself must reject it.
			rid := serviceStateV3ID(repository, first.updates[0].State.ServiceKey)
			visibility, err := surrealdb.Query[[]uint64](ctx, s.db, "SELECT VALUE visible_from FROM $rid;", map[string]any{"rid": rid})
			if err != nil || visibility == nil || len(*visibility) != 1 || len((*visibility)[0].Result) != 1 {
				t.Fatalf("actual visibility=%+v error=%v", visibility, err)
			}
			originalVisibility := (*visibility)[0].Result[0]
			for index, value := range []uint64{selected.StateControlRevision + 1, originalVisibility} {
				if _, err := surrealdb.Query[any](ctx, s.db, "UPDATE $rid SET visible_from = $value RETURN NONE;",
					map[string]any{"rid": rid, "value": value}); err != nil {
					t.Fatal(err)
				}
				if index == 0 {
					if err := s.commitServiceStateV3TargetChunk(ctx, chunk, *plan, first.plan, first.updates,
						summary, first.summary, false, targets, first.preimages, first.summaryPreimage, first.payloadRecords); err == nil {
						t.Fatal("stale visibility census was admitted")
					}
					assertUnchanged()
				}
			}
			// A caller cannot omit one actually missing image or its summary from
			// the payload: both exact predicates are checked before any mutation.
			wrong := slices.Clone(first.preimages)
			wrong[0] = false
			if err := s.commitServiceStateV3TargetChunk(ctx, chunk, *plan, first.plan, first.updates,
				summary, first.summary, false, targets, wrong, first.summaryPreimage, first.payloadRecords-1); err == nil {
				t.Fatal("missing row-preimage target was admitted")
			}
			assertUnchanged()
			if err := s.commitServiceStateV3TargetChunk(ctx, chunk, *plan, first.plan, first.updates,
				summary, first.summary, false, targets, first.preimages, false, first.payloadRecords-1); err == nil {
				t.Fatal("missing summary-preimage target was admitted")
			}
			assertUnchanged()
			if err := s.commitServiceStateV3TargetChunk(ctx, chunk, *plan, first.plan, first.updates,
				summary, first.summary, false, targets, first.preimages, first.summaryPreimage, first.payloadRecords); err != nil {
				t.Fatal(err)
			}
			assertCounts(len(first.updates), 1)
			canceled, stop := context.WithCancel(ctx)
			stop()
			nextSummary := summary
			if first.summary != nil {
				nextSummary = first.summary
			}
			if err := s.commitServiceStateV3Changes(canceled, chunk, first.plan, changes[len(first.updates):], read, cursor, nextSummary); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if err := s.ReleaseGenerationChunk(ctx, chunk, "selected prefix interruption"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ProcessServiceStateV3Chunk(ctx, chunk); err == nil {
				t.Fatal("released prefix lease continued")
			}
			retry := serviceStateV3HeadroomClaimContext(ctx, t, s, first.plan, offset)
			if retry.ID != chunk.ID || retry.LeaseToken == chunk.LeaseToken {
				t.Fatal("continuation did not use a new lease for the same member")
			}
			result, err := s.ProcessServiceStateV3Chunk(ctx, retry)
			wantRead := 512
			if phase == "remove" {
				wantRead = 512 - len(first.updates) + 1 // Remaining old rows plus the retained new key.
			}
			if err != nil || result.Applied != 512-len(first.updates) || result.Read != wantRead {
				t.Fatalf("same-member continuation=%+v error=%v", result, err)
			}
			if err := s.CompleteGenerationChunk(ctx, retry); err != nil {
				t.Fatal(err)
			}
			runServiceStateV3PlanContext(ctx, t, s, begin)
			assertCounts(512, 1)
			prior, err := s.GetServiceStateV3PointSnapshot(ctx, repository, old.ServiceKey,
				selected.StateControlRevision, selected.StateSummaryDigest)
			if err != nil || prior.StateDigest != old.StateDigest {
				t.Fatalf("selected state changed: %+v, %v", prior, err)
			}
			priorSummary, err := s.GetServiceStateV3SummarySnapshot(ctx, repository,
				selected.StateControlRevision, selected.StateSummaryDigest)
			if err != nil || priorSummary.SummaryDigest != selected.StateSummaryDigest {
				t.Fatalf("selected summary changed: %+v, %v", priorSummary, err)
			}
		})
	}
}

func serviceStateV3PreimageSelect(ctx context.Context, t *testing.T, s *Surreal, repository, search string) ServiceRuntimeSelector {
	t.Helper()
	pointer, err := s.GetServiceCatalogV3CandidatePointer(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := s.GetServiceStateV3SummaryPoint(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	reference := ServiceCatalogV3RelationshipReference{
		Repository: repository, RelationshipGenerationDigest: fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(repository))),
		RelationshipRootDigest: fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(repository+"-root"))),
		CatalogRootDigest:      pointer.RootDigest, CatalogControlRevision: pointer.ControlRevision,
		StateControlRevision: summary.ControlRevision, StateSummaryDigest: summary.SummaryDigest,
	}
	if err := s.PinServiceCatalogV3RelationshipReference(ctx, reference); err != nil {
		t.Fatal(err)
	}
	selected, err := s.SelectServiceRuntimeV3(ctx, ServiceRuntimeSelectionRequest{
		Repository: repository,
		Target: ServiceRuntimeTarget{
			CatalogRootDigest: pointer.RootDigest, CatalogControlRevision: pointer.ControlRevision,
			StateControlRevision: summary.ControlRevision, StateSummaryDigest: summary.SummaryDigest,
			SearchGenerationDigest: search, RelationshipGenerationDigest: reference.RelationshipGenerationDigest,
			RelationshipRootDigest: reference.RelationshipRootDigest,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return selected
}
