//go:build darwin || linux

package store

import (
	"context"
	"testing"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/connection"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

// Scripted envelopes traverse the pinned SDK's real CBOR/WebSocket decoder.
// They discriminate shapes; none is claimed to be V20's actual server reply.
func TestGenerationTransitionPinnedSDKEnvelopeShapes(t *testing.T) {
	for _, test := range []struct {
		name          string
		chunk         any
		omit          bool
		want          readaccounting.TransitionReadIDKind
		decodeFailure bool
	}{
		{name: "none_chunk", chunk: models.None, want: readaccounting.TransitionReadIDMissing},
		{name: "null_chunk", chunk: nil, want: readaccounting.TransitionReadIDMissing},
		{name: "omitted_chunk", omit: true, want: readaccounting.TransitionReadIDMissing},
		{name: "empty_chunk", chunk: map[string]any{}, want: readaccounting.TransitionReadIDMissing},
		{name: "omitted_id", chunk: map[string]any{"identity": "test-owned"}, want: readaccounting.TransitionReadIDMissing},
		{name: "null_id", chunk: map[string]any{"id": nil}, want: readaccounting.TransitionReadIDMissing},
		{name: "valid_id", chunk: map[string]any{"id": models.NewRecordID("generation_schedule_chunk", "test-owned")}, want: readaccounting.TransitionReadIDValid},
		{name: "wrong_table", chunk: map[string]any{"id": models.NewRecordID("other", "test-owned")}, want: readaccounting.TransitionReadIDWrongTable},
		{name: "integer_id", chunk: map[string]any{"id": models.NewRecordID("generation_schedule_chunk", int64(1))}, want: readaccounting.TransitionReadIDWrongType},
		{name: "string_instead_of_record_id", chunk: map[string]any{"id": "test-owned"}, decodeFailure: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, owner, _ := storeAccountingFixture(t, 40, 2)
			db, native := storeAccountingDB(t, ctx, owner)
			native.call = func(context.Context, *connection.RPCRequest) (any, error) {
				envelope := map[string]any{"current_schedule_digest": "test-owned"}
				if !test.omit {
					envelope["chunk"] = test.chunk
				}
				return []surrealdb.QueryResult[any]{{Status: "OK", Result: []any{envelope}}}, nil
			}
			results, err := storeQuery[[]generationStaleLeaseChunkFenceRec](ctx, owner, db, generationTransitionReadSQL, nil, storeWrite(0))
			if test.decodeFailure {
				if err == nil {
					t.Fatal("malformed scalar gained typed reply")
				}
				return
			}
			if err != nil || results == nil || len(*results) != 1 || len((*results)[0].Result) != 1 {
				t.Fatal("controlled envelope refused", err)
			}
			record := (*results)[0].Result[0].Chunk
			if generationTransitionDecodedID(record) != test.want {
				t.Fatal("decoded ID classification differs")
			}
			_, failure := record.chunk()
			if test.want == readaccounting.TransitionReadIDValid && failure != nil || test.want != readaccounting.TransitionReadIDValid && failure == nil {
				t.Fatal("existing record-ID gate changed")
			}
		})
	}
}
