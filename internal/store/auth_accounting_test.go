//go:build darwin || linux

package store

import (
	"context"
	"encoding/base64"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/connection"
	"github.com/surrealdb/surrealdb.go/pkg/models"

	"github.com/bmeddeb/phebs/internal/storeaccounting"
)

func TestAuthAccountingFixedRecipes(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	userRID, keyRID := userID("user"), apiKeyID("key")
	userRows := []userRec{{RecID: &userRID, Email: "user@example.com", NormalizedEmail: "user@example.com"}}
	keyRows := []apiKeyRec{{RecID: &keyRID, Capabilities: []APIKeyCapability{}}}
	for _, test := range []struct {
		name  string
		call  func(context.Context, *Surreal) error
		rows  []any
		rid   models.RecordID
		write bool
		want  error
	}{
		{"stats", func(ctx context.Context, s *Surreal) error { _, err := s.AuthStats(ctx); return err }, []any{[]map[string]any{{"users": 1}}, []any{}}, models.RecordID{}, false, nil},
		{"create_user", func(ctx context.Context, s *Surreal) error {
			_, err := s.CreateUser(ctx, User{ID: "user", CreatedAt: now})
			return err
		}, []any{userRows}, userRID, true, nil},
		{"user_id", func(ctx context.Context, s *Surreal) error { _, err := s.GetUserByID(ctx, "user"); return err }, []any{userRows}, userRID, false, nil},
		{"user_email", func(ctx context.Context, s *Surreal) error {
			_, err := s.GetUserByEmail(ctx, "user@example.com")
			return err
		}, []any{userRows}, models.RecordID{}, false, nil},
		{"mark_login", func(ctx context.Context, s *Surreal) error { return s.MarkUserLogin(ctx, "user", now) }, []any{userRows}, userRID, true, nil},
		{"mark_absent", func(ctx context.Context, s *Surreal) error { return s.MarkUserLogin(ctx, "user", now) }, []any{[]userRec{}}, userRID, true, ErrNotFound},
		{"create_key", func(ctx context.Context, s *Surreal) error {
			_, err := s.CreateAPIKey(ctx, APIKey{ID: "key", CreatedAt: now})
			return err
		}, []any{keyRows}, keyRID, true, nil},
		{"get_key", func(ctx context.Context, s *Surreal) error { _, err := s.GetAPIKey(ctx, "key"); return err }, []any{keyRows}, keyRID, false, nil},
		{"list_keys", func(ctx context.Context, s *Surreal) error { _, err := s.ListAPIKeys(ctx, "user"); return err }, []any{keyRows}, models.RecordID{}, false, nil},
		{"revoke_key", func(ctx context.Context, s *Surreal) error { return s.RevokeAPIKey(ctx, "key", "user", now) }, []any{keyRows}, keyRID, true, nil},
		{"revoke_no_match", func(ctx context.Context, s *Surreal) error { return s.RevokeAPIKey(ctx, "key", "user", now) }, []any{[]apiKeyRec{}}, keyRID, true, ErrNotFound},
		{"touch_key", func(ctx context.Context, s *Surreal) error { return s.TouchAPIKey(ctx, "key", now) }, []any{nil}, keyRID, true, nil},
		{"legacy_upsert", func(ctx context.Context, s *Surreal) error { return s.SetLegacyAPIKey(ctx, "hash", now) }, []any{nil}, apiKeyID(legacyAPIKeyID), true, nil},
		{"legacy_delete", func(ctx context.Context, s *Surreal) error { return s.SetLegacyAPIKey(ctx, "", now) }, []any{nil}, apiKeyID(legacyAPIKeyID), true, nil},
		{"commit_session", func(ctx context.Context, s *Surreal) error {
			return s.CommitAuthSession(ctx, "session", []byte("data"), now)
		}, []any{nil}, authSessionID("session"), true, nil},
		{"find_session", func(ctx context.Context, s *Surreal) error {
			_, _, err := s.FindAuthSession(ctx, "session", now)
			return err
		}, []any{[]authSessionRec{{Data: base64.RawStdEncoding.EncodeToString([]byte("data")), Expiry: now}}}, authSessionID("session"), false, nil},
		{"delete_session", func(ctx context.Context, s *Surreal) error { return s.DeleteAuthSession(ctx, "session") }, []any{nil}, authSessionID("session"), true, nil},
		{"empty_expiry", func(ctx context.Context, s *Surreal) error {
			_, err := s.DeleteExpiredAuthSessions(ctx, now)
			return err
		}, []any{[]models.RecordID{}}, models.RecordID{}, false, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, selected := range []bool{false, true} {
				t.Run(map[bool]string{false: "ordinary", true: "selected"}[selected], func(t *testing.T) {
					ctx := t.Context()
					var owner *storeCallOwner
					var controller *storeaccounting.Controller
					if selected {
						ctx, owner, controller = storeAccountingFixture(t, 40, 2)
					}
					db, native := storeAccountingDB(t, ctx, owner)
					s := &Surreal{db: db, accounting: owner}
					index := 0
					native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
						if request.Method != "query" || len(request.Params) != 2 || index >= len(test.rows) {
							return nil, errors.New("unexpected auth submission")
						}
						if test.rid.Table != "" {
							var vars map[string]any
							if selected {
								raw, ok := request.Params[1].(cbor.RawMessage)
								if !ok {
									return nil, errors.New("selected auth did not own its payload snapshot")
								}
								if err := native.codec.Unmarshal(raw, &vars); err != nil {
									return nil, err
								}
							} else {
								vars = request.Params[1].(map[string]any)
							}
							if !reflect.DeepEqual(vars["rid"], test.rid) {
								return nil, errors.New("auth submission changed its actual record operand")
							}
						}
						if selected {
							// The actual SA01 ACK must precede this native boundary.
							prefix, err := controller.Snapshot()
							want := uint64(0)
							if test.write {
								want = 1
							}
							if err != nil || prefix.Transactions != want || prefix.Rows != want || prefix.MaximumRows != want {
								return nil, errors.New("auth forwarded before exact submitted-target accounting")
							}
						}
						rows := test.rows[index]
						index++
						return []surrealdb.QueryResult[any]{{Status: "OK", Result: rows}}, nil
					}
					if err := test.call(ctx, s); !errors.Is(err, test.want) || index != len(test.rows) {
						t.Fatalf("auth calls=%d error=%v; want %d/%v", index, err, len(test.rows), test.want)
					}
					if selected {
						if err := controller.Fence(); err != nil {
							t.Fatal(err)
						}
						if err := owner.checkpoint(ctx); err != nil {
							t.Fatalf("auth left an active typed SDK call: %v", err)
						}
					}
				})
			}
		})
	}
}

func TestAuthAccountingUnsupportedRecipes(t *testing.T) {
	for _, name := range []string{"first_user", "oidc", "positive_expiry"} {
		t.Run(name, func(t *testing.T) {
			for _, selected := range []bool{false, true} {
				t.Run(map[bool]string{false: "ordinary", true: "selected"}[selected], func(t *testing.T) {
					ctx := t.Context()
					var owner *storeCallOwner
					var controller *storeaccounting.Controller
					if selected {
						ctx, owner, controller = storeAccountingFixture(t, 40, 2)
					}
					db, native := storeAccountingDB(t, ctx, owner)
					s := &Surreal{db: db, accounting: owner}
					count := 0
					native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
						count++
						var rows any
						if name == "positive_expiry" {
							if count == 1 {
								rows = []models.RecordID{authSessionID("expired")}
							} else {
								rows = []authSessionRec{{}, {}}
							}
						} else {
							rid := userID("created")
							rows = []userRec{{RecID: &rid}}
						}
						return []surrealdb.QueryResult[any]{{Status: "OK", Result: rows}}, nil
					}
					var err error
					switch name {
					case "first_user":
						_, err = s.CreateFirstUser(ctx, User{ID: "created"})
					case "oidc":
						_, err = s.UpsertOIDCUser(ctx, "issuer", "subject", "email", "email", "display", true)
					case "positive_expiry":
						var deleted int
						deleted, err = s.DeleteExpiredAuthSessions(ctx, time.Now())
						if !selected && deleted != 2 {
							t.Fatalf("ordinary bulk deletion changed its count: %d", deleted)
						}
					}
					wantCalls := 0
					if name == "positive_expiry" {
						wantCalls++
					}
					if !selected {
						wantCalls++
						if err != nil {
							t.Fatal(err)
						}
					} else {
						if !errors.Is(err, storeaccounting.ErrDescriptor) {
							t.Fatalf("unknown write was not refused: %v", err)
						}
						prefix, _ := controller.Snapshot()
						if prefix.Transactions != 0 || prefix.Rows != 0 || prefix.Complete {
							t.Fatalf("refusal invented a write prefix: %+v", prefix)
						}
						if _, err := s.GetAPIKey(ctx, "later"); err == nil {
							t.Fatal("unknown auth write did not latch failure")
						}
					}
					if count != wantCalls {
						t.Fatalf("native calls=%d; want %d", count, wantCalls)
					}
				})
			}
		})
	}
}

func TestAuthAccountingSourceCoverage(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "auth.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"AuthStats": {"storeRead", "storeRead"}, "CreateFirstUser": {"storeUnsupported"},
		"CreateUser": {"storeWrite"}, "GetUserByID": {"storeRead"}, "GetUserByEmail": {"storeRead"},
		"UpsertOIDCUser": {"storeUnsupported"}, "MarkUserLogin": {"storeWrite"}, "CreateAPIKey": {"storeWrite"},
		"GetAPIKey": {"storeRead"}, "ListAPIKeys": {"storeRead"}, "RevokeAPIKey": {"storeWrite"},
		"TouchAPIKey": {"storeWrite"}, "SetLegacyAPIKey": {"storeWrite", "storeWrite"},
		"CommitAuthSession": {"storeWrite"}, "FindAuthSession": {"storeRead"}, "DeleteAuthSession": {"storeWrite"},
		"DeleteExpiredAuthSessions": {"storeRead", "storeUnsupported"},
	}
	field := func(expr ast.Expr, name string) bool {
		selector, ok := expr.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != name {
			return false
		}
		receiver, ok := selector.X.(*ast.Ident)
		return ok && receiver.Name == "s"
	}
	sdkName := "surrealdb"
	for _, imported := range file.Imports {
		if imported.Path.Value == `"github.com/surrealdb/surrealdb.go"` && imported.Name != nil {
			sdkName = imported.Name.Name
			if sdkName == "." {
				t.Fatal("dot SDK import defeats closed auth source coverage")
			}
		}
	}
	ast.Inspect(file, func(node ast.Node) bool {
		if selector, ok := node.(*ast.SelectorExpr); ok {
			if receiver, ok := selector.X.(*ast.Ident); ok && receiver.Name == sdkName && selector.Sel.Name != "QueryResult" {
				t.Error("auth contains an unannotated SDK submission escape")
			}
		}
		return true
	})
	count := 0
	ownedConnections := make(map[ast.Expr]bool)
	for _, decl := range file.Decls {
		function, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		var got []string
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			target := call.Fun
			if indexed, ok := target.(*ast.IndexExpr); ok {
				target = indexed.X
			}
			name, ok := target.(*ast.Ident)
			if !ok || name.Name != "storeQuery" {
				return true
			}
			count++
			if len(call.Args) != 6 || !field(call.Args[1], "accounting") || !field(call.Args[2], "db") {
				t.Fatal("auth query lost its actual Surreal accounting owner/connection")
			}
			ownedConnections[call.Args[2]] = true
			recipe, ok := call.Args[5].(*ast.CallExpr)
			if !ok {
				t.Fatal("auth query recipe is not source-owned")
			}
			kind, ok := recipe.Fun.(*ast.Ident)
			if !ok {
				t.Fatal("auth query recipe is not a closed constructor")
			}
			got = append(got, kind.Name)
			if kind.Name == "storeWrite" {
				if len(recipe.Args) != 1 {
					t.Fatal("auth fixed-target write lost its row count")
				}
				rows, ok := recipe.Args[0].(*ast.BasicLit)
				if !ok || rows.Kind != token.INT || rows.Value != "1" {
					t.Fatal("auth fixed-target write guessed a non-one count")
				}
			} else if len(recipe.Args) != 0 {
				t.Fatal("auth read/unsupported recipe gained a caller argument")
			}
			return true
		})
		if !reflect.DeepEqual(got, want[function.Name.Name]) {
			t.Fatalf("auth %s recipes=%v; want %v", function.Name.Name, got, want[function.Name.Name])
		}
		delete(want, function.Name.Name)
	}
	if count != 20 || len(want) != 0 {
		t.Fatalf("auth annotation coverage=%d, missing=%v", count, want)
	}
	ast.Inspect(file, func(node ast.Node) bool {
		if selector, ok := node.(*ast.SelectorExpr); ok && selector.Sel.Name == "db" && !ownedConnections[selector] {
			t.Error("auth borrowed an SDK connection outside its annotated query")
		}
		return true
	})
}

func TestAuthAccountingFailedAttemptRetainsPrefix(t *testing.T) {
	for _, failure := range []string{"native_refusal", "transport", "typed_decode"} {
		t.Run(failure, func(t *testing.T) {
			ctx, owner, controller := storeAccountingFixture(t, 40, 2)
			db, native := storeAccountingDB(t, ctx, owner)
			s := &Surreal{db: db, accounting: owner}
			native.call = func(context.Context, *connection.RPCRequest) (any, error) {
				switch failure {
				case "native_refusal":
					return []surrealdb.QueryResult[any]{{Status: "ERR", Result: "unique auth target"}}, nil
				case "transport":
					return nil, context.DeadlineExceeded
				default:
					return []surrealdb.QueryResult[any]{{Status: "OK", Result: "not a user array"}}, nil
				}
			}
			if _, err := s.CreateUser(ctx, User{ID: "user"}); err == nil || failure == "native_refusal" && !errors.Is(err, ErrConflict) {
				t.Fatalf("auth failure classification=%v", err)
			}
			prefix, _ := controller.Snapshot()
			if prefix.Transactions != 1 || prefix.Rows != 1 || prefix.MaximumRows != 1 || prefix.Complete || native.calls != 1 {
				t.Fatalf("failed attempt lost its accepted prefix: %+v, calls=%d", prefix, native.calls)
			}
			if failure != "native_refusal" {
				if err := s.TouchAPIKey(ctx, "later", time.Now()); err == nil || native.calls != 1 {
					t.Fatal("uncertain auth attempt allowed later work")
				}
			}
		})
	}
}
