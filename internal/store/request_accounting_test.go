//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/connection"
	"github.com/surrealdb/surrealdb.go/pkg/models"

	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

func TestRequestStoreAccounting(t *testing.T) {
	now := time.Now().UTC()
	for _, test := range []struct {
		name        string
		call        func(context.Context, *Surreal) error
		results     []any
		rid         models.RecordID
		unsupported bool
	}{
		{"audit_append", func(ctx context.Context, s *Surreal) error {
			return s.AppendAuditEvent(ctx, AuditEvent{ID: "audit", Action: "neutral", CreatedAt: now})
		}, []any{[]auditEventRec{{}}}, auditEventID("audit"), false},
		{"usage_append", func(ctx context.Context, s *Surreal) error {
			return s.RecordUsageEvent(ctx, UsageEvent{ID: "usage", Kind: "neutral", CreatedAt: now})
		}, []any{[]usageEventRec{{}}}, usageEventID("usage"), false},
		{"audit_read", func(ctx context.Context, s *Surreal) error {
			_, err := s.ListAuditEvents(ctx, 0, 1)
			return err
		}, []any{[]auditEventRec{}}, models.RecordID{}, false},
		{"usage_read", func(ctx context.Context, s *Surreal) error {
			_, err := s.ListUsageEvents(ctx, now)
			return err
		}, []any{[]usageEventRec{}}, models.RecordID{}, false},
		{"permissions_read", func(ctx context.Context, s *Surreal) error {
			_, err := s.ListPermittedRepos(ctx, []string{"neutral"})
			return err
		}, []any{[]repoPermissionRec{}}, models.RecordID{}, false},
		{"prune_empty", func(ctx context.Context, s *Surreal) error {
			_, err := s.PruneAuditEvents(ctx, now)
			return err
		}, []any{[]map[string]any{{"n": 0}}}, models.RecordID{}, false},
		{"prune_positive", func(ctx context.Context, s *Surreal) error {
			_, err := s.PruneUsageEvents(ctx, now)
			return err
		}, []any{[]map[string]any{{"n": 1}}, nil}, models.RecordID{}, true},
		{"permissions_replace", func(ctx context.Context, s *Surreal) error {
			return s.SetRepoPermissions(ctx, "neutral", []string{"user"})
		}, []any{nil}, models.RecordID{}, true},
		{"permissions_delete", func(ctx context.Context, s *Surreal) error {
			return s.DeleteRepoPermissions(ctx, "neutral")
		}, []any{nil}, models.RecordID{}, true},
	} {
		for _, selected := range []bool{false, true} {
			t.Run(test.name+map[bool]string{false: "/ordinary", true: "/selected"}[selected], func(t *testing.T) {
				ctx := t.Context()
				var owner *storeCallOwner
				var controller *storeaccounting.Controller
				if selected {
					ctx, owner, controller = storeAccountingFixture(t, 40, 2)
				}
				db, native := storeAccountingDB(t, ctx, owner)
				s := &Surreal{db: db, accounting: owner}
				calls, wantCalls := 0, len(test.results)
				want := uint64(0)
				if test.rid.Table != "" {
					want = 1
				}
				if selected && test.unsupported {
					wantCalls--
				}
				native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
					if calls >= wantCalls || request.Method != "query" || len(request.Params) != 2 {
						return nil, errors.New("unexpected request-store forwarding")
					}
					if selected {
						prefix, err := controller.Snapshot()
						if err != nil || prefix.Transactions != want || prefix.Rows != want || prefix.MaximumRows != want {
							return nil, errors.New("request forwarded before exact prefix ACK")
						}
						if want != 0 {
							raw, ok := request.Params[1].(cbor.RawMessage)
							var vars map[string]any
							if !ok || native.codec.Unmarshal(raw, &vars) != nil || !reflect.DeepEqual(vars["rid"], test.rid) {
								return nil, errors.New("request changed its actual record operand")
							}
						}
					}
					result := test.results[calls]
					calls++
					return []surrealdb.QueryResult[any]{{Status: "OK", Result: result}}, nil
				}
				err := test.call(ctx, s)
				if selected && test.unsupported {
					if !errors.Is(err, storeaccounting.ErrDescriptor) {
						t.Fatalf("unsupported request: %v", err)
					}
					prefix, _ := controller.Snapshot()
					if prefix.Transactions != 0 || prefix.Rows != 0 || prefix.Complete {
						t.Fatalf("unsupported request invented completion: %+v", prefix)
					}
				} else if err != nil {
					t.Fatal(err)
				} else if selected {
					if err := controller.Fence(); err != nil {
						t.Fatal(err)
					}
					if err := owner.checkpoint(ctx); err != nil {
						t.Fatalf("request left an active SDK call: %v", err)
					}
				}
				if calls != wantCalls {
					t.Fatalf("native calls=%d want=%d", calls, wantCalls)
				}
			})
		}
	}
}
