//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/storeaccounting"
	"github.com/fxamacker/cbor/v2"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/connection"
)

// These SDK/CBOR scripts prove submitted operands and pre-forward SA01 ACKs.
// The existing T407 native tests separately evaluate SQL and replay semantics.
func TestEvidenceChunkAccountingOperands(t *testing.T) {
	for _, tc := range []struct {
		name     string
		facts    int
		ordinary bool
	}{
		{"new", 1, false}, {"normalized_duplicates", 2, false}, {"replay", 1, false},
		{"false_guard", 1, false}, {"retry", 1, false}, {"exhausted", 1, false},
		{"known_failure", 1, false}, {"unknown_failure", 1, false}, {"canceled", 1, false},
		{"169_facts", 169, false}, {"170_facts", 170, false},
		{"256_facts", 256, false}, {"ordinary_256", 256, true}, {"exact_512", 169, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base, owner, controller := storeAccountingFixture(t, 40, 2)
			if tc.ordinary {
				owner = nil
			}
			ctx, cancel := context.WithCancel(base)
			defer cancel()
			db, native := storeAccountingDB(t, base, owner)
			run := extractionRunRec{RunID: "neutral-run", Repo: "example.com/neutral/evidence", Commit: strings.Repeat("a", 40), Status: "staged"}
			var atoms []EvidenceAtom
			var assocs []SnapshotEvidence
			var asserts []Assertion
			for i := range tc.facts {
				if tc.name == "normalized_duplicates" {
					i = 0
				}
				a, e, x := t407Batch(run.Repo, run.Commit, i)
				atoms, assocs, asserts = append(atoms, a...), append(assocs, e...), append(asserts, x...)
			}
			wantRows := uint64(3*tc.facts + 3)
			if tc.name == "normalized_duplicates" {
				wantRows = 6
			}
			if tc.name == "exact_512" {
				for i := range 2 {
					extra := asserts[0]
					extra.Object += fmt.Sprintf("/extra-%d", i)
					asserts = append(asserts, extra)
				}
				wantRows = 512 // AddEvidence permits unequal self-contained vectors.
			}
			chunk := internalCallerDigest('b')
			reads, writes := 0, 0
			var submitted normalizedEvidenceBatch
			native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
				sql := request.Params[0].(string)
				if strings.HasPrefix(sql, "SELECT * FROM $rid") {
					reads++
					if tc.name == "canceled" {
						cancel()
					}
					return queueAccountingOK([]extractionRunRec{run}), nil
				}
				writes++
				if sql != addEvidenceSQL || strings.Count(sql, "UPDATE $run SET") != 2 ||
					strings.Count(sql, "INSERT INTO") != 3 || strings.Count(sql, "CREATE $chunk_rid CONTENT") != 1 {
					return nil, errors.New("evidence append changed supplied SQL bodies")
				}
				var payload struct {
					Atoms     []map[string]any `json:"atoms"`
					Assocs    []map[string]any `json:"assocs"`
					Asserts   []map[string]any `json:"asserts"`
					FactCount int              `json:"fact_count"`
					Chunk     string           `json:"chunk_id"`
					Digest    string           `json:"content_digest"`
					Now       time.Time        `json:"now"`
				}
				vars, err := native.codec.Marshal(request.Params[1])
				if err != nil {
					return nil, err
				}
				if err := native.codec.Unmarshal(vars, &payload); err != nil {
					return nil, err
				}
				expectedRun := run.run()
				expected, err := normalizeEvidenceBatch(&expectedRun, atoms, assocs, asserts, payload.Now)
				if err != nil {
					return nil, err
				}
				digest, err := evidenceBatchDigest(expected)
				if err != nil {
					return nil, err
				}
				// Canonical SDK decoding normalizes map number/array representations.
				for _, pair := range []struct{ got, want []map[string]any }{
					{payload.Atoms, expected.atoms}, {payload.Assocs, expected.assocs}, {payload.Asserts, expected.asserts},
				} {
					raw, err := native.codec.Marshal(pair.want)
					if err != nil {
						return nil, err
					}
					var decoded []map[string]any
					if err := native.codec.Unmarshal(raw, &decoded); err != nil {
						return nil, err
					}
					if !reflect.DeepEqual(pair.got, decoded) {
						return nil, errors.New("actual normalized evidence operands differ")
					}
				}
				wantFacts, wantChunk := tc.facts, chunk
				if tc.name == "exact_512" {
					wantFacts, wantChunk = 171, digest
				}
				if payload.FactCount != wantFacts || payload.Chunk != wantChunk || payload.Digest != digest ||
					uint64(len(payload.Atoms)+len(payload.Assocs)+len(payload.Asserts)+3) != wantRows {
					return nil, errors.New("chunk identity or normalized row charge differs")
				}
				submitted = normalizedEvidenceBatch{atoms: payload.Atoms, assocs: payload.Assocs, asserts: payload.Asserts}
				if !tc.ordinary {
					prefix, err := controller.Snapshot()
					if err != nil || prefix.Transactions != uint64(writes) || prefix.Rows != uint64(writes)*wantRows || prefix.MaximumRows != wantRows {
						return nil, fmt.Errorf("native append preceded exact ACK: %+v %v", prefix, err)
					}
				}
				switch tc.name {
				case "known_failure":
					return nil, &surrealdb.QueryError{Message: "phebs-permanent: conflicting evidence chunk replay"}
				case "unknown_failure":
					return nil, context.DeadlineExceeded
				case "false_guard":
					return queueAccountingOK([]extractionRunRec{}), nil
				case "retry", "exhausted":
					if writes == 1 || tc.name == "exhausted" {
						return nil, &surrealdb.QueryError{Message: "phebs-conflict: neutral append retry"}
					}
				}
				return queueAccountingOK([]extractionRunRec{run}), nil
			}
			s := &Surreal{db: db, accounting: owner}
			var err error
			if tc.name == "exact_512" {
				err = s.AddEvidence(ctx, run.RunID, atoms, assocs, asserts)
			} else {
				err = s.AddEvidenceChunk(ctx, run.RunID, chunk, tc.facts, atoms, assocs, asserts)
			}
			if tc.name == "replay" && err == nil {
				err = s.AddEvidenceChunk(ctx, run.RunID, chunk, tc.facts, atoms, assocs, asserts)
			}
			wantReads, wantWrites := 1, 1
			if tc.name == "false_guard" || tc.name == "replay" {
				wantReads = 2
			}
			if tc.name == "retry" || tc.name == "replay" {
				wantWrites = 2
			}
			if tc.name == "exhausted" {
				wantWrites = maxQueueRetries
			}
			refused := wantRows > 512 && !tc.ordinary
			if refused || tc.name == "canceled" {
				wantWrites = 0
			}
			wantOK := !refused && tc.name != "canceled" && tc.name != "false_guard" && tc.name != "known_failure" && tc.name != "unknown_failure" && tc.name != "exhausted"
			prefix, _ := controller.Snapshot()
			if (err == nil) != wantOK || reads != wantReads || writes != wantWrites || !tc.ordinary &&
				(prefix.Transactions != uint64(wantWrites) || prefix.Rows != uint64(wantWrites)*wantRows ||
					(prefix.Producers[0].Calls != 0) != (tc.name == "unknown_failure")) {
				t.Fatalf("reads/writes=%d/%d want=%d/%d rows=%d prefix=%+v error=%v", reads, writes, wantReads, wantWrites, wantRows, prefix, err)
			}
			if refused && !errors.Is(err, storeaccounting.ErrDescriptor) {
				t.Fatalf("bound refusal classification: %v", err)
			}
			if tc.name == "normalized_duplicates" && (len(submitted.atoms) != 1 || len(submitted.assocs) != 1 || len(submitted.asserts) != 1) {
				t.Fatal("duplicate payload was charged before normalization")
			}
		})
	}
}

func TestEvidenceChunkAccountingReceiptRead(t *testing.T) {
	for _, mode := range []string{"valid", "missing", "wrong_identity", "invalid_digest", "negative_rows", "known_failure", "unknown_failure", "invalid_input"} {
		t.Run(mode, func(t *testing.T) {
			ctx, owner, controller := storeAccountingFixture(t, 40, 2)
			db, native := storeAccountingDB(t, ctx, owner)
			chunk := internalCallerDigest('a')
			receipt := evidenceChunkAccountingRec{RunID: "neutral", ChunkID: chunk, ContentDigest: internalCallerDigest('b'), FactCount: 169, RowDelta: 338, ReferenceDelta: 169}
			native.call = func(_ context.Context, request *connection.RPCRequest) (any, error) {
				if request.Params[0] != `SELECT run_id, chunk_id, content_digest, fact_count, row_delta, reference_delta
			FROM $rid LIMIT 1` {
					return nil, errors.New("receipt read SQL differs")
				}
				var payload struct {
					RID any `json:"rid"`
				}
				if err := native.codec.Unmarshal(request.Params[1].(cbor.RawMessage), &payload); err != nil {
					return nil, err
				}
				wantRaw, err := native.codec.Marshal(map[string]any{"rid": evidenceChunkRecordID("neutral", chunk)})
				if err != nil {
					return nil, err
				}
				var expected struct {
					RID any `json:"rid"`
				}
				if err := native.codec.Unmarshal(wantRaw, &expected); err != nil {
					return nil, err
				}
				if !reflect.DeepEqual(payload, expected) {
					return nil, errors.New("receipt lost point identity")
				}
				switch mode {
				case "missing":
					return queueAccountingOK([]evidenceChunkAccountingRec{}), nil
				case "wrong_identity":
					receipt.RunID = "other"
				case "invalid_digest":
					receipt.ContentDigest = "bad"
				case "negative_rows":
					receipt.RowDelta = -1
				case "known_failure":
					return nil, &surrealdb.QueryError{Message: "neutral receipt unavailable"}
				case "unknown_failure":
					return nil, context.DeadlineExceeded
				}
				return queueAccountingOK([]evidenceChunkAccountingRec{receipt}), nil
			}
			if mode == "invalid_input" {
				chunk = "bad"
			}
			result, err := (&Surreal{db: db, accounting: owner}).GetEvidenceChunkAccounting(ctx, "neutral", chunk)
			wantCalls := 1
			if mode == "invalid_input" {
				wantCalls = 0
			}
			prefix, _ := controller.Snapshot()
			if (err == nil) != (mode == "valid") || native.calls != wantCalls || prefix.Rows != 0 || prefix.Transactions != 0 || prefix.Producers[0].Calls != 0 {
				t.Fatalf("receipt result=%+v calls=%d prefix=%+v error=%v", result, native.calls, prefix, err)
			}
			if mode == "valid" && result != EvidenceChunkAccounting(receipt) {
				t.Fatal("receipt content changed")
			}
		})
	}
}
