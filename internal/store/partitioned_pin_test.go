package store

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/connection"
	connectionhttp "github.com/surrealdb/surrealdb.go/pkg/connection/http"
	"github.com/surrealdb/surrealdb.go/pkg/models"
	"github.com/surrealdb/surrealdb.go/surrealcbor"
)

type partitionedPinConnection struct {
	*failingOpenConnection
	codec             *surrealcbor.Codec
	read              []surrealdb.QueryResult[[]models.RecordID]
	readErr, writeErr error
	writeRows         []evidencePinRec
	cancel            context.CancelFunc
	reads, writes     int
}

func (conn *partitionedPinConnection) Send(_ context.Context, method string, params ...any) (*connection.RPCResponse[cbor.RawMessage], error) {
	if method != "query" || len(params) != 2 {
		return nil, errors.New("unexpected pin test RPC")
	}
	want := map[string]any{
		"run_rid": extractionRunID("pin-test"), "run_id": "pin-test",
		"pin_rid": evidencePinRecordID("pin-test", "owner-test"),
		"pin_key": hashIdentity("pin_", "pin-test", "owner-test"), "owner": "owner-test",
	}
	if !reflect.DeepEqual(params[1], want) {
		return nil, errors.New("pin test identity changed")
	}
	var value any
	switch params[0] {
	case existingPartitionedExtractionPinSQL:
		conn.reads++
		if conn.cancel != nil {
			conn.cancel()
		}
		if conn.readErr != nil {
			return nil, conn.readErr
		}
		value = conn.read
	case pinPartitionedExtractionRunSQL:
		conn.writes++
		if conn.reads != 1 {
			return nil, errors.New("pin write omitted read-only check")
		}
		if conn.writeErr != nil {
			return nil, conn.writeErr
		}
		value = []surrealdb.QueryResult[[]evidencePinRec]{{Status: "OK", Result: conn.writeRows}}
	default:
		return nil, errors.New("unexpected pin test SQL")
	}
	body, err := conn.codec.Marshal(value)
	if err != nil {
		return nil, err
	}
	raw := cbor.RawMessage(body)
	return &connection.RPCResponse[cbor.RawMessage]{Result: &raw}, nil
}

func TestPartitionedPinReadOnlyAndFallback(t *testing.T) {
	records := func(ids ...models.RecordID) []surrealdb.QueryResult[[]models.RecordID] {
		return []surrealdb.QueryResult[[]models.RecordID]{{Status: "OK", Result: ids}}
	}
	read := func(value bool) []surrealdb.QueryResult[[]models.RecordID] {
		if value {
			return records(evidencePinRecordID("pin-test", "owner-test"))
		}
		return records()
	}
	failure := errors.New("neutral pin transport failure")
	for _, test := range []struct {
		name              string
		read              []surrealdb.QueryResult[[]models.RecordID]
		readErr, writeErr error
		writeOK, cancel   bool
		writes            int
		want              error
		invalid           bool
	}{
		{name: "existing", read: read(true)},
		{name: "new", read: read(false), writeOK: true, writes: 1},
		{name: "unrooted missing", read: read(false), writes: 1, want: ErrConflict},
		{name: "read error", readErr: failure, want: failure},
		{name: "write error", read: read(false), writeErr: failure, writes: 1, want: failure},
		{name: "empty response", read: []surrealdb.QueryResult[[]models.RecordID]{}, invalid: true},
		{name: "multiple responses", read: append(read(false), read(true)...), invalid: true},
		{name: "multiple IDs", read: records(evidencePinRecordID("pin-test", "owner-test"), evidencePinRecordID("pin-test", "owner-test")), invalid: true},
		{name: "wrong ID", read: records(evidencePinRecordID("different", "owner-test")), invalid: true},
		{name: "wrong table", read: records(models.NewRecordID("extraction_run", "pin-test")), invalid: true},
		{name: "numeric ID", read: records(models.NewRecordID("evidence_pin", 1)), invalid: true},
		{name: "cancel existing", read: read(true), cancel: true, want: context.Canceled},
		{name: "cancel before fallback", read: read(false), cancel: true, want: context.Canceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			codec := surrealcbor.New()
			conn := &partitionedPinConnection{
				failingOpenConnection: &failingOpenConnection{Connection: connectionhttp.New(&connection.Config{Unmarshaler: codec})},
				codec:                 codec, read: test.read, readErr: test.readErr, writeErr: test.writeErr,
			}
			if test.writeOK {
				conn.writeRows = []evidencePinRec{{RunID: "pin-test", Kind: "owner-test"}}
			}
			if test.cancel {
				conn.cancel = cancel
			}
			db, err := surrealdb.FromConnection(t.Context(), conn)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := db.Close(context.Background()); err != nil {
					t.Error(err)
				}
			})
			s := &Surreal{db: db}
			err = s.PinPartitionedExtractionRun(ctx, "pin-test", "owner-test")
			if (!test.invalid && !errors.Is(err, test.want)) || (test.invalid && err == nil) || conn.reads != 1 || conn.writes != test.writes {
				t.Fatalf("pin error=%v reads=%d writes=%d", err, conn.reads, conn.writes)
			}
		})
	}
}

func TestPartitionedPinRefusesBeforeSDK(t *testing.T) {
	var absent *Surreal
	for _, test := range []struct{ run, owner string }{
		{"", "owner"}, {" run", "owner"}, {"run", ""}, {"run", "owner "},
		{strings.Repeat("x", maxEvidenceIdentityBytes+1), "owner"},
		{"run", strings.Repeat("x", maxEvidenceIdentityBytes+1)},
	} {
		if err := absent.PinPartitionedExtractionRun(t.Context(), test.run, test.owner); err == nil {
			t.Fatal("invalid identity reached SDK")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := absent.PinPartitionedExtractionRun(ctx, "run", "owner"); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled pin = %v", err)
	}
}

func TestPartitionedPinNativeAcquisition(t *testing.T) {
	if _, err := exec.LookPath("surreal"); err != nil {
		t.Skip("surreal binary not installed")
	}
	deadline := time.Now().Add(2 * time.Minute)
	if outer, ok := t.Deadline(); ok && outer.Add(-time.Minute).Before(deadline) {
		deadline = outer.Add(-time.Minute)
	}
	ctx, cancel := context.WithDeadline(t.Context(), deadline)
	t.Cleanup(cancel)
	if err := ctx.Err(); err != nil {
		t.Fatal(err)
	}
	runtime, stop, err := startEngine(ctx, "memory")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	s, err := Open(ctx, runtime.Endpoint, "root", "root", submissionProbeScope, submissionProbeScope)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancelCleanup := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelCleanup()
		if err := s.Close(cleanup); err != nil {
			t.Error(err)
		}
	})
	const owner = "relationship:sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	t.Run("new repeated and valid unrooted", func(t *testing.T) {
		run, root := partitionedPinNativeFixture(ctx, t, s, "repeat")
		if err := s.PinPartitionedExtractionRun(ctx, run, owner); err != nil {
			t.Fatal(err)
		}
		// A fixed old timestamp makes renewal detection independent of clock
		// resolution and of how quickly the two native calls complete.
		partitionedPinNativeQuery(ctx, t, s, "UPDATE $rid SET created_at = $at", map[string]any{
			"rid": evidencePinRecordID(run, owner), "at": time.Unix(1, 0).UTC(),
		})
		before := partitionedPinNativeTimestamp(ctx, t, s, run, owner)
		if err := s.PinPartitionedExtractionRun(ctx, run, owner); err != nil {
			t.Fatal(err)
		}
		if got := partitionedPinNativeTimestamp(ctx, t, s, run, owner); !got.Equal(before) {
			t.Fatal("reacquisition renewed timestamp")
		}
		partitionedPinNativeQuery(ctx, t, s, "DELETE $rid", map[string]any{"rid": root})
		if err := s.PinPartitionedExtractionRun(ctx, run, owner); err != nil {
			t.Fatalf("valid existing unrooted pin: %v", err)
		}
		if got := partitionedPinNativeTimestamp(ctx, t, s, run, owner); !got.Equal(before) {
			t.Fatal("unrooted reacquisition renewed timestamp")
		}
		// This second owner is not protected by the first owner's exact pin.
		if err := s.PinPartitionedExtractionRun(ctx, run, "different-owner"); !errors.Is(err, ErrConflict) {
			t.Fatalf("unrooted different owner = %v", err)
		}
	})
	t.Run("same values at wrong record ID do not pin", func(t *testing.T) {
		run, root := partitionedPinNativeFixture(ctx, t, s, "wrong-id")
		partitionedPinNativeQuery(ctx, t, s, `
DELETE $root;
CREATE $other CONTENT {pin_key: $key, run_id: $run, kind: $owner, created_at: $at};`, map[string]any{
			"root": root, "other": models.NewRecordID("evidence_pin", "wrong-record-id"),
			"key": hashIdentity("pin_", run, owner), "run": run, "owner": owner, "at": time.Unix(1, 0).UTC(),
		})
		if err := s.PinPartitionedExtractionRun(ctx, run, owner); !errors.Is(err, ErrConflict) {
			t.Fatalf("unrooted pin alias = %v", err)
		}
	})
	for _, test := range []struct{ name, mutation string }{
		{"aborted", "status = 'aborted'"},
		{"unsealed", "partition_sealed = false"},
		{"quarantined", "retention_quarantined = true"},
		{"mismatched run ID", "run_id = 'different-run'"},
	} {
		t.Run(test.name, func(t *testing.T) {
			run, _ := partitionedPinNativeFixture(ctx, t, s, strings.ReplaceAll(test.name, " ", "-"))
			if err := s.PinPartitionedExtractionRun(ctx, run, owner); err != nil {
				t.Fatal(err)
			}
			before := partitionedPinNativeTimestamp(ctx, t, s, run, owner)
			partitionedPinNativeQuery(ctx, t, s, "UPDATE $rid SET "+test.mutation, map[string]any{"rid": extractionRunID(run)})
			if err := s.PinPartitionedExtractionRun(ctx, run, owner); !errors.Is(err, ErrConflict) {
				t.Fatalf("invalid run with existing pin = %v", err)
			}
			if got := partitionedPinNativeTimestamp(ctx, t, s, run, owner); !got.Equal(before) {
				t.Fatal("invalid run changed pin")
			}
		})
	}
	t.Run("mismatched pin repair retains native admission", func(t *testing.T) {
		run, root := partitionedPinNativeFixture(ctx, t, s, "repair")
		if err := s.PinPartitionedExtractionRun(ctx, run, owner); err != nil {
			t.Fatal(err)
		}
		partitionedPinNativeQuery(ctx, t, s, "UPDATE $rid SET pin_key = 'wrong-pin-key'", map[string]any{"rid": evidencePinRecordID(run, owner)})
		partitionedPinNativeQuery(ctx, t, s, "DELETE $rid", map[string]any{"rid": root})
		// The existing native run/owner fence permits repair even without a root.
		if err := s.PinPartitionedExtractionRun(ctx, run, owner); err != nil {
			t.Fatal(err)
		}
		rows, err := surrealdb.Query[[]struct {
			Key string `json:"pin_key"`
		}](ctx, s.db,
			"SELECT pin_key FROM $rid", map[string]any{"rid": evidencePinRecordID(run, owner)})
		if err != nil || rows == nil || len(*rows) != 1 || len((*rows)[0].Result) != 1 || (*rows)[0].Result[0].Key != hashIdentity("pin_", run, owner) {
			t.Fatalf("native pin repair = %+v, %v", rows, err)
		}
	})
	t.Run("concurrent unpin retains explicit owner ordering", func(t *testing.T) {
		run, root := partitionedPinNativeFixture(ctx, t, s, "unpin")
		if err := s.PinPartitionedExtractionRun(ctx, run, owner); err != nil {
			t.Fatal(err)
		}
		partitionedPinNativeQuery(ctx, t, s, "DELETE $rid", map[string]any{"rid": root})
		var pinErr, unpinErr error
		var joins sync.WaitGroup
		joins.Go(func() { pinErr = s.PinPartitionedExtractionRun(ctx, run, owner) })
		joins.Go(func() { unpinErr = s.UnpinPartitionedExtractionRun(ctx, run, owner) })
		joins.Wait()
		// Success linearizes before the explicit unpin; a later check refuses.
		// A native conflict is also permitted by the unchanged, unretried API.
		if unpinErr != nil || (pinErr != nil && !errors.Is(pinErr, ErrConflict) && !isRetryable(pinErr)) {
			t.Fatalf("overlap pin=%v unpin=%v", pinErr, unpinErr)
		}
		if err := s.PinPartitionedExtractionRun(ctx, run, owner); !errors.Is(err, ErrConflict) {
			t.Fatalf("reacquisition after joined unpin = %v", err)
		}
	})
	if os.Getenv("PHEBS_TEST_PARTITIONED_PIN_METRICS") == "1" {
		t.Run("completed write metric", func(t *testing.T) {
			if runtime.Surreal.Version != "3.2.0" {
				t.Fatalf("unexpected native version %q", runtime.Surreal.Version)
			}
			partitionedPinNativeMetrics(ctx, t, s, runtime.Endpoint, owner)
		})
	}
}

// The fixture uses a real admitted run and directly installs only the two
// native pin predicates. It is not a complete extraction publication fixture.
func partitionedPinNativeFixture(ctx context.Context, t *testing.T, s *Surreal, name string) (string, models.RecordID) {
	t.Helper()
	repository := "synthetic.invalid/pin-" + name
	run, err := s.BeginPartitionedExtractionRun(ctx, ExtractionScope{
		Repository: repository, Commit: strings.Repeat("a", 40), Domain: "proto-contract",
	}, "pin-test", "sha256:"+strings.Repeat("b", 64), "sha256:"+strings.Repeat("c", 64), "", PartitionedExtractionRunLimits{Facts: 1, Rows: 2, References: 1})
	if err != nil {
		t.Fatal(err)
	}
	root := partitionedDomainID(repository, "proto-contract")
	partitionedPinNativeQuery(ctx, t, s, `
UPDATE $run SET partition_active = false, partition_sealed = true;
CREATE $root CONTENT {repository: $repository, domain: 'proto-contract', run_id: $run_id};`,
		map[string]any{"run": extractionRunID(run.ID), "root": root, "repository": repository, "run_id": run.ID})
	return run.ID, root
}

func partitionedPinNativeQuery(ctx context.Context, t *testing.T, s *Surreal, sql string, vars map[string]any) {
	t.Helper()
	if _, err := surrealdb.Query[any](ctx, s.db, sql, vars); err != nil {
		t.Fatal(err)
	}
}

func partitionedPinNativeTimestamp(ctx context.Context, t *testing.T, s *Surreal, run, owner string) time.Time {
	t.Helper()
	rows, err := surrealdb.Query[[]struct {
		At time.Time `json:"created_at"`
	}](ctx, s.db,
		"SELECT created_at FROM $rid", map[string]any{"rid": evidencePinRecordID(run, owner)})
	if err != nil || rows == nil || len(*rows) != 1 || len((*rows)[0].Result) != 1 || (*rows)[0].Result[0].At.IsZero() {
		t.Fatalf("native pin timestamp = %+v, %v", rows, err)
	}
	return (*rows)[0].Result[0].At
}

func partitionedPinNativeMetrics(ctx context.Context, t *testing.T, s *Surreal, endpoint, owner string) {
	t.Helper()
	run, _ := partitionedPinNativeFixture(ctx, t, s, "metrics")
	bearer, err := s.db.SignIn(ctx, surrealdb.Auth{Username: "root", Password: "root"})
	if err != nil || bearer == "" {
		t.Fatalf("metrics token unavailable: %v", err)
	}
	transport := &http.Transport{}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	url := "http://" + strings.TrimPrefix(endpoint, "ws://") + "/metrics"
	snapshot := func() idleClaimMetricSnapshot {
		t.Helper()
		value, err := readSubmissionProbeMetrics(ctx, client, url, bearer)
		if err != nil {
			t.Fatalf("pin metrics unavailable: %v", err)
		}
		return value
	}
	control := func() (idleClaimMetricSnapshot, idleClaimMetricSnapshot) {
		t.Helper()
		before, after := snapshot(), snapshot()
		if delta, err := submissionProbeWriteDelta(before, after); err != nil || delta != 0 {
			t.Fatalf("pin scrape-only write control unavailable: delta=%d error=%v", delta, err)
		}
		return before, after
	}
	variables := map[string]any{
		"run_rid": extractionRunID(run), "run_id": run,
		"pin_rid": evidencePinRecordID(run, owner), "pin_key": hashIdentity("pin_", run, owner), "owner": owner,
	}
	for _, name := range []string{"lookup", "original_upsert", "public_reacquisition"} {
		_, before := control()
		switch name {
		case "lookup":
			rows, err := surrealdb.Query[[]models.RecordID](ctx, s.db, existingPartitionedExtractionPinSQL, variables)
			if err != nil || rows == nil || len(*rows) != 1 || len((*rows)[0].Result) != 0 {
				t.Fatalf("initial exact lookup = %+v, %v", rows, err)
			}
		case "original_upsert":
			rows, err := surrealdb.Query[[]evidencePinRec](ctx, s.db, pinPartitionedExtractionRunSQL, variables)
			if err != nil || len(firstDomainRows(rows)) != 1 {
				t.Fatalf("original pin acquisition = %+v, %v", rows, err)
			}
		case "public_reacquisition":
			if err := s.PinPartitionedExtractionRun(ctx, run, owner); err != nil {
				t.Fatal(err)
			}
		}
		after, _ := control()
		delta, err := submissionProbeWriteDelta(before, after)
		if err != nil {
			t.Fatalf("%s completed write measurement unavailable: %v", name, err)
		}
		t.Logf("%s completed_native_writes=%d observed_read_snapshots=%d/%d read_attribution=unavailable; not attempted-prefix proof", name, delta, before.read, after.read)
		if name == "original_upsert" {
			// This is deliberately measured, not inferred as one from a source
			// method/BEGIN. Native bookkeeping can own further transactions.
			if delta == 0 {
				t.Fatal("original pin UPSERT had no observed native write")
			}
		} else if delta != 0 {
			t.Fatalf("%s added %d completed native writes; want zero", name, delta)
		}
	}
}
